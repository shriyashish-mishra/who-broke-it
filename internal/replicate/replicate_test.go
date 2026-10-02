package replicate_test

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"wbi/internal/engine"
	"wbi/internal/model"
	"wbi/internal/replicate"
	"wbi/internal/testutil"
)

var (
	alice = engine.AgentOpts{Agent: "claude", As: "Alice"}
	bob   = engine.AgentOpts{Agent: "codex", As: "Bob"}
)

func mustSync(t *testing.T, e *engine.Engine) replicate.Report {
	t.Helper()
	r, err := e.Sync()
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	return r
}

func TestClaimOnOneMachineIsVisibleAndExclusiveOnTheOther(t *testing.T) {
	m := testutil.Team(t)
	if _, err := m.A.Claim("TASK-001", alice, false); err != nil {
		t.Fatal(err)
	}
	if r := mustSync(t, m.A); r.Pushed == 0 {
		t.Fatal("alice should have published her claim")
	}
	r := mustSync(t, m.B)
	if r.Applied == 0 {
		t.Fatal("bob should have received alice's events")
	}
	if got := m.B.State("TASK-001"); got.AgentID != "claude@alice" || got.Status != model.InProgress {
		t.Fatalf("bob sees %+v", got)
	}
	if _, err := m.B.Claim("TASK-001", bob, false); err == nil || !strings.Contains(err.Error(), "already claimed by claude@alice") {
		t.Fatalf("bob must not be able to take alice's task: %v", err)
	}
	var seen bool
	for _, a := range m.B.Agents() {
		seen = seen || a.ID == "claude@alice"
	}
	if !seen {
		t.Fatal("bob should see alice's agent")
	}
}

func TestRaceExactlyOneClaimWins(t *testing.T) {
	m := testutil.Team(t)
	// Both claim while offline from each other, so neither knows about the other's claim.
	if _, err := m.A.Claim("TASK-001", alice, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.B.Claim("TASK-001", bob, false); err != nil {
		t.Fatal(err)
	}
	mustSync(t, m.A) // alice pushes first: she wins
	rb := mustSync(t, m.B)
	if len(rb.Rejected) != 1 || !strings.Contains(rb.Rejected[0], "lost the claim to claude@alice") {
		t.Fatalf("bob's claim must be rejected, got %+v", rb.Rejected)
	}
	if got := m.B.State("TASK-001").AgentID; got != "claude@alice" {
		t.Fatalf("bob's view must converge on alice, got %q", got)
	}
	mustSync(t, m.A)
	if got := m.A.State("TASK-001").AgentID; got != "claude@alice" {
		t.Fatalf("alice keeps her claim, got %q", got)
	}
	if n := m.B.SyncStatus().Rejected; n != 1 {
		t.Fatalf("bob should record 1 lost claim, got %d", n)
	}
	// bob is no longer "working on" the task he lost, locally or in what he published
	mustSync(t, m.B)
	mustSync(t, m.A)
	for name, e := range map[string]*engine.Engine{"bob": m.B, "alice": m.A} {
		for _, a := range e.Agents() {
			if a.ID == "codex@bob" && (a.CurrentTask != "" || a.Status != "idle") {
				t.Fatalf("%s sees bob as %s/%s after he lost the claim", name, a.Status, a.CurrentTask)
			}
		}
	}
}

func TestConcurrentSyncedClaimsHaveExactlyOneWinner(t *testing.T) {
	m := testutil.Team(t)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, c := range []struct {
		e *engine.Engine
		o engine.AgentOpts
	}{{m.A, alice}, {m.B, bob}} {
		wg.Add(1)
		go func(i int, e *engine.Engine, o engine.AgentOpts) {
			defer wg.Done()
			errs[i] = e.Synced(true, func() error { _, err := e.Claim("TASK-001", o, false); return err })
		}(i, c.e, c.o)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one claim must win, got %d (errs: %v)", wins, errs)
	}
	mustSync(t, m.A)
	mustSync(t, m.B)
	if a, b := m.A.State("TASK-001").AgentID, m.B.State("TASK-001").AgentID; a != b || a == "" {
		t.Fatalf("machines must converge on one owner: %q vs %q", a, b)
	}
}

func TestContractChangeReachesTheOtherMachine(t *testing.T) {
	m := testutil.Team(t)
	// Bob's UI work builds on BillingStatus (started early against the published contract).
	if _, err := m.B.Claim("TASK-008", bob, true); err != nil {
		t.Fatal(err)
	}
	mustSync(t, m.B)
	mustSync(t, m.A)

	// Alice changes the contract in a real worktree and hands off.
	r, err := m.A.Start("TASK-007", alice, true, true)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Put(t, r.Workdir, "src/api/billing/status.ts", "export type S = 'paused'")
	testutil.CommitAll(t, r.Workdir, "feat: paused\n\nWBI-Task: TASK-007")
	if _, err := m.A.SubmitHandoff("TASK-007", engine.HandoffOpts{AgentOpts: alice, Summary: "paused", Contracts: []string{"BillingStatus=now supports paused"}}); err != nil {
		t.Fatal(err)
	}
	mustSync(t, m.A)

	mustSync(t, m.B)
	t8, _ := m.B.Task("TASK-008")
	stale := m.B.StaleContracts(t8)
	if len(stale) != 1 || stale[0].Contract != "BillingStatus" || stale[0].To != 2 {
		t.Fatalf("bob must see the contract as stale: %+v", stale)
	}
	var told bool
	for _, n := range m.B.Inbox("codex@bob", false) {
		told = told || strings.Contains(n.Msg, "BillingStatus changed to v2 by TASK-007")
	}
	if !told {
		t.Fatal("bob's inbox must carry the change notice")
	}
	if b := m.B.Blast("BillingStatus"); len(b.ActiveAgents) != 1 || b.ActiveAgents[0].AgentID != "codex@bob" {
		t.Fatalf("blast on bob's machine must see his own agent: %+v", b.ActiveAgents)
	}
	if h := m.B.GetHandoff("TASK-007"); h == nil || h.Implemented != "paused" {
		t.Fatalf("alice's handoff must replicate: %+v", h)
	}
	if got := len(m.B.Blame("src/api/billing/", 5).ContractAlerts); got == 0 {
		t.Fatal("blame on bob's machine should know about the contract change")
	}

	// Bob adapts and acknowledges; alice's machine learns it.
	if err := m.B.Ack("TASK-008"); err != nil {
		t.Fatal(err)
	}
	mustSync(t, m.B)
	mustSync(t, m.A)
	t8a, _ := m.A.Task("TASK-008")
	if s := m.A.StaleContracts(t8a); len(s) != 0 {
		t.Fatalf("bob's ack must replicate to alice: %+v", s)
	}
}

func TestIntentsReplicateSoConflictsAreSeenAcrossMachines(t *testing.T) {
	m := testutil.Team(t)
	if _, err := m.A.Claim("TASK-001", alice, false); err != nil {
		t.Fatal(err)
	}
	if res, err := m.A.DeclareIntent(alice, "TASK-001", model.Modify, "docs/architecture.md", ""); err != nil || res.Blocked {
		t.Fatalf("declare: %v %+v", err, res)
	}
	mustSync(t, m.A)
	mustSync(t, m.B)
	if n := len(m.B.Intents(false)); n != 1 {
		t.Fatalf("bob should see 1 active intent, got %d", n)
	}
	// Bob force-claims a later task whose scope is elsewhere, then probes alice's area.
	if _, err := m.B.Claim("TASK-002", bob, true); err != nil {
		t.Fatal(err)
	}
	res, err := m.B.DeclareIntent(bob, "TASK-002", model.Modify, "docs/architecture.md", "")
	if err != nil {
		t.Fatal(err)
	}
	var overlap bool
	for _, c := range res.Conflicts {
		overlap = overlap || (c.Code == "OVERLAP" && c.AgentID == "claude@alice")
	}
	if !overlap {
		t.Fatalf("bob must be warned about alice's intent: %+v", res.Conflicts)
	}
	// Releasing on A (handoff releases intents) reaches B.
	m.A.ReleaseIntents("TASK-001")
	mustSync(t, m.A)
	mustSync(t, m.B)
	for _, i := range m.B.Intents(false) {
		if i.AgentID == "claude@alice" {
			t.Fatalf("alice's released intent must disappear for bob: %+v", i)
		}
	}
}

func TestOfflineWorkSyncsLater(t *testing.T) {
	m := testutil.Team(t)
	hidden := m.Remote + ".away"
	if err := os.Rename(m.Remote, hidden); err != nil {
		t.Fatal(err)
	}
	if _, err := m.A.Claim("TASK-001", alice, false); err != nil {
		t.Fatalf("claiming must work offline: %v", err)
	}
	_, err := m.A.Sync()
	var off *replicate.ErrOffline
	if !errors.As(err, &off) {
		t.Fatalf("want ErrOffline, got %v", err)
	}
	if m.A.SyncStatus().Pending == 0 {
		t.Fatal("offline changes must stay queued")
	}
	if err := os.Rename(hidden, m.Remote); err != nil {
		t.Fatal(err)
	}
	if r := mustSync(t, m.A); r.Pushed == 0 {
		t.Fatal("queued changes must publish once back online")
	}
	mustSync(t, m.B)
	if got := m.B.State("TASK-001").AgentID; got != "claude@alice" {
		t.Fatalf("bob should now see the claim made offline, got %q", got)
	}
	if m.A.SyncStatus().Pending != 0 {
		t.Fatal("outbox must drain")
	}
}

func TestMachinesConvergeAndSyncIsIdempotent(t *testing.T) {
	m := testutil.Team(t)
	for _, step := range []func() error{
		func() error { _, e := m.A.Claim("TASK-001", alice, false); return e },
		func() error { _, e := m.B.Claim("TASK-003", bob, true); return e },
		func() error { _, e := m.A.DeclareIntent(alice, "TASK-001", model.Modify, "docs/x.md", ""); return e },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		mustSync(t, m.A)
		mustSync(t, m.B)
	}
	sa, sb := m.A.States(), m.B.States()
	for id, a := range sa {
		b := sb[id]
		if a.Status != b.Status || a.AgentID != b.AgentID {
			t.Errorf("%s diverged: A=%+v B=%+v", id, a, b)
		}
	}
	if len(sa) != len(sb) {
		t.Fatalf("different task sets: %d vs %d", len(sa), len(sb))
	}
	r := mustSync(t, m.A)
	if r.Applied != 0 || r.Pushed != 0 {
		t.Fatalf("a quiet sync must be a no-op, got %+v", r)
	}
	if n := len(m.A.Intents(false)); n != len(m.B.Intents(false)) {
		t.Fatalf("intent counts differ: %d vs %d", n, len(m.B.Intents(false)))
	}
}

func TestSyncIsOffUntilEnabled(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	if _, ok, why := e.SyncConfig(); ok || !strings.Contains(why, "wbi sync init") {
		t.Fatalf("sync must be off by default: ok=%v why=%q", ok, why)
	}
	if _, err := e.Sync(); err == nil {
		t.Fatal("explicit sync without config must explain itself")
	}
	ran := false
	if err := e.Synced(true, func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("Synced must just run the function when sync is off: %v", err)
	}
}

// The decisive case: bob publishes inside alice's fetch→push window. Only git's atomic ref update (compare-and-swap)
// stops alice from overwriting bob's history, and the remote's order, not the local clock, decides who owns the task.
func TestPushRaceInsideTheWindowIsDecidedByTheRemote(t *testing.T) {
	m := testutil.Team(t)
	if _, err := m.A.Claim("TASK-001", alice, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.B.Claim("TASK-001", bob, false); err != nil {
		t.Fatal(err)
	}
	fired := false
	replicate.BeforePush = func() {
		if fired {
			return
		}
		fired = true
		mustSync(t, m.B) // bob wins the race to the remote
	}
	defer func() { replicate.BeforePush = nil }()

	ra := mustSync(t, m.A)
	if ra.Attempts < 2 {
		t.Fatalf("alice's first push must be rejected and retried, attempts=%d", ra.Attempts)
	}
	if len(ra.Rejected) != 1 || !strings.Contains(ra.Rejected[0], "lost the claim to codex@bob") {
		t.Fatalf("alice must learn she lost: %+v", ra.Rejected)
	}
	mustSync(t, m.B)
	for name, e := range map[string]*engine.Engine{"alice": m.A, "bob": m.B} {
		if got := e.State("TASK-001").AgentID; got != "codex@bob" {
			t.Fatalf("%s must see bob as owner, got %q", name, got)
		}
	}
	// bob's history must still be on the remote: nothing was overwritten
	out := testutil.Git(t, m.DirB, "log", "--oneline", "refs/wbi/tracking/wbi_sync")
	if strings.Count(out, "wbi sync") < 1 {
		t.Fatalf("log history lost: %s", out)
	}
}
