package engine_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
	"github.com/shriyashish-mishra/who-broke-it/internal/testutil"
)

var (
	claude = engine.AgentOpts{Agent: "claude", As: "Maya"}
	codex  = engine.AgentOpts{Agent: "codex", As: "DevB"}
	cursor = engine.AgentOpts{Agent: "cursor", As: "DevC"}
)

func ptr[T any](v T) *T { return &v }

// finish runs a task end to end: start in a worktree, commit files, hand off with every criterion attested.
func finish(t *testing.T, e *engine.Engine, id string, who engine.AgentOpts, files map[string]string, tweak func(*engine.HandoffOpts)) engine.Verification {
	t.Helper()
	r, err := e.Start(id, who, true, false)
	if err != nil {
		t.Fatalf("start %s: %v", id, err)
	}
	for f, body := range files {
		testutil.Put(t, r.Workdir, f, body)
	}
	testutil.CommitAll(t, r.Workdir, fmt.Sprintf("feat: %s\n\nWBI-Task: %s\nWBI-Agent: %s@%s", id, id, who.Agent, strings.ToLower(who.As)))
	task, _ := e.Task(id)
	var attest []int
	for i := range task.Acceptance {
		attest = append(attest, i+1)
	}
	o := engine.HandoffOpts{AgentOpts: who, Summary: "did " + id, TestsPassed: ptr(true), TestsSummary: "5 passed", Attest: attest}
	if tweak != nil {
		tweak(&o)
	}
	res, err := e.SubmitHandoff(id, o)
	if err != nil {
		t.Fatalf("handoff %s: %v", id, err)
	}
	return res.Verification
}

func through(t *testing.T, e *engine.Engine, ids ...string) {
	t.Helper()
	for _, id := range ids {
		task, _ := e.Task(id)
		files := map[string]string{"docs/notes.md": "ok"}
		if task.Layer != "architecture" {
			files = map[string]string{strings.TrimSuffix(task.AllowedPaths[0], "/**") + "/index.ts": "export const x = 1;"}
		}
		v := finish(t, e, id, claude, files, nil)
		if v.Verdict == "READY_FOR_REVIEW" {
			if err := e.Approve(id, "tester"); err != nil {
				t.Fatal(err)
			}
		}
		if got := e.State(id).Status; got != model.Done {
			t.Fatalf("%s should be DONE after %s, is %s: %+v", id, v.Verdict, got, v.Checks)
		}
	}
}

func TestBlockedTasksAreExplainedAndClaimsAreExclusive(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	if _, err := e.Claim("TASK-002", claude, false); err == nil || !strings.Contains(err.Error(), "Waiting for") || !strings.Contains(err.Error(), "TASK-001") {
		t.Fatalf("expected blocked explanation, got %v", err)
	}
	if _, err := e.Claim("TASK-001", claude, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Claim("TASK-001", codex, false); err == nil || !strings.Contains(err.Error(), "already claimed by claude@maya") {
		t.Fatalf("expected exclusive claim, got %v", err)
	}
	task, _ := e.Task("TASK-001")
	if got := e.Display(task, e.States()); got != model.InProgress {
		t.Fatalf("display %s", got)
	}
}

func TestJudgeSendsScopeViolationsBack(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	v := finish(t, e, "TASK-001", claude, map[string]string{"src/api/billing/oops.ts": "x"}, nil)
	if v.Verdict != "FAILED" {
		t.Fatalf("verdict %s", v.Verdict)
	}
	var scope string
	for _, c := range v.Checks {
		if c.Name == "Scope" {
			scope = c.Detail
		}
	}
	if !strings.Contains(scope, "outside allowed") && !strings.Contains(scope, "restricted") {
		t.Fatalf("scope detail %q", scope)
	}
	if e.State("TASK-001").Status != model.InProgress {
		t.Fatal("failed task must return to IN_PROGRESS")
	}
	found := false
	for _, n := range e.Inbox("claude@maya", false) {
		found = found || strings.Contains(n.Msg, "Verification of TASK-001 failed")
	}
	if !found {
		t.Fatal("agent should be notified of the failed verification")
	}
}

func TestUnattestedCriterionCannotBeDone(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	// TASK-002's criteria are agent-attestable. Attesting only one of two must fail verification.
	r, err := e.Start("TASK-002", codex, true, true)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Put(t, r.Workdir, "migrations/001.sql", "create table t();")
	testutil.CommitAll(t, r.Workdir, "sql")
	res, err := e.SubmitHandoff("TASK-002", engine.HandoffOpts{AgentOpts: codex, Summary: "s", TestsPassed: ptr(true), Attest: []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verification.Verdict != "FAILED" {
		t.Fatalf("verdict %s", res.Verification.Verdict)
	}
	for _, c := range res.Verification.Checks {
		if c.Name == "Acceptance criteria" && (!strings.Contains(c.Detail, "1/2") || !strings.Contains(c.Detail, "not attested")) {
			t.Fatalf("detail %q", c.Detail)
		}
	}
}

func TestHumanOnlyCriteriaCannotBeAttestedByAnAgent(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	r, _ := e.Start("TASK-001", claude, true, false)
	testutil.Put(t, r.Workdir, "docs/a.md", "x")
	testutil.CommitAll(t, r.Workdir, "docs")
	// Even if the agent claims to attest everything, human criteria are not its to sign.
	res, err := e.SubmitHandoff("TASK-001", engine.HandoffOpts{AgentOpts: claude, Summary: "s", Attest: []int{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	v := res.Verification
	if v.Verdict != "READY_FOR_REVIEW" || len(v.PendingHuman) != 2 || e.State("TASK-001").Status != model.Review {
		t.Fatalf("want READY_FOR_REVIEW awaiting 2 human sign-offs, got %s %+v / %s", v.Verdict, v.PendingHuman, e.State("TASK-001").Status)
	}
	for _, c := range v.Checks {
		if c.Name == "Acceptance criteria" && !strings.Contains(c.Detail, "awaiting human sign-off") {
			t.Fatalf("detail %q", c.Detail)
		}
	}
	if err := e.Approve("TASK-001", "maya"); err != nil || e.State("TASK-001").Status != model.Done {
		t.Fatalf("a human approval must finish it: %v", err)
	}
}

func TestHighImpactNeedsAHumanThenUnblocksDependents(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	through(t, e, "TASK-001")
	v := finish(t, e, "TASK-002", codex, map[string]string{"migrations/001.sql": "create table t();"}, nil)
	if v.Verdict != "READY_FOR_REVIEW" || e.State("TASK-002").Status != model.Review {
		t.Fatalf("want READY_FOR_REVIEW/REVIEW, got %s/%s", v.Verdict, e.State("TASK-002").Status)
	}
	t4, _ := e.Task("TASK-004")
	if e.Display(t4, e.States()) != model.Blocked {
		t.Fatal("TASK-004 must stay blocked until approval")
	}
	if err := e.Approve("TASK-002", "maya"); err != nil {
		t.Fatal(err)
	}
	if e.Display(t4, e.States()) != model.Ready {
		t.Fatal("TASK-004 must be READY after approval")
	}
}

func codes(cs []model.Conflict) map[string]bool {
	m := map[string]bool{}
	for _, c := range cs {
		m[c.Code] = true
	}
	return m
}

func TestIntentRegistryCatchesConflictsBeforeCodeExists(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	through(t, e, "TASK-001", "TASK-002", "TASK-004")
	must := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.Claim("TASK-007", claude, false))
	must(e.Claim("TASK-003", cursor, false))
	r, err := e.DeclareIntent(claude, "TASK-007", model.Modify, "src/api/billing/**", "")
	if err != nil || r.Blocked {
		t.Fatalf("own scope must not block: %v %+v", err, r)
	}
	wander, _ := e.DeclareIntent(cursor, "TASK-003", model.Modify, "src/api/billing/plans.ts", "")
	c := codes(wander.Conflicts)
	if !wander.Blocked || !c["RESTRICTED_PATH"] || !c["OWNERSHIP_CONFLICT"] {
		t.Fatalf("wander: %+v", wander.Conflicts)
	}
	contract, _ := e.DeclareIntent(cursor, "TASK-003", model.ChangeContract, "BillingStatus", "")
	if !codes(contract.Conflicts)["CONTRACT_NOT_OWNED"] || !contract.Blocked {
		t.Fatalf("contract: %+v", contract.Conflicts)
	}
	must(e.Claim("TASK-010", engine.AgentOpts{Agent: "codex", As: "DevB"}, true))
	dep, _ := e.DeclareIntent(codex, "TASK-010", model.DependOn, "TASK-007", "")
	if !codes(dep.Conflicts)["UNMET_DEPENDENCY"] {
		t.Fatalf("dep: %+v", dep.Conflicts)
	}
	if n := len(e.IntentCollisions()); n != 0 {
		t.Fatalf("collisions %d", n)
	}
}

func TestContractChangePropagatesBlastSeesAgentsAckClearsDrift(t *testing.T) {
	dir, e := testutil.PlannedRepo(t)
	through(t, e, "TASK-001", "TASK-002", "TASK-004")
	if _, err := e.Claim("TASK-008", cursor, true); err != nil { // UI builds against the published contract early
		t.Fatal(err)
	}
	t8, _ := e.Task("TASK-008")
	if len(e.StaleContracts(t8)) != 0 {
		t.Fatal("no staleness before the change")
	}
	before := e.Blast("BillingStatus")
	if before.Risk != "HIGH" || len(before.ActiveAgents) != 1 || before.ActiveAgents[0].AgentID != "cursor@devc" {
		t.Fatalf("blast before: %+v", before)
	}
	has := func(s []string, v string) bool {
		for _, x := range s {
			if x == v {
				return true
			}
		}
		return false
	}
	if !has(before.DirectTasks, "TASK-008") || !has(before.DirectTasks, "TASK-009") || !has(before.IndirectTasks, "TASK-013") {
		t.Fatalf("blast tasks: %+v", before)
	}

	v := finish(t, e, "TASK-007", claude, map[string]string{"src/api/billing/status.ts": "export type S = 'paused'"}, func(o *engine.HandoffOpts) {
		o.Contracts = []string{"BillingStatus=now supports paused"}
	})
	if v.Verdict != "READY_FOR_REVIEW" {
		t.Fatalf("verdict %s %+v", v.Verdict, v.Checks)
	}
	if err := e.Approve("TASK-007", "maya"); err != nil {
		t.Fatal(err)
	}

	stale := e.StaleContracts(t8)
	if len(stale) != 1 || stale[0].Contract != "BillingStatus" || stale[0].To != 2 {
		t.Fatalf("stale: %+v", stale)
	}
	found := false
	for _, n := range e.Inbox("cursor@devc", false) {
		found = found || strings.Contains(n.Msg, "BillingStatus changed to v2 by TASK-007")
	}
	if !found {
		t.Fatal("cursor must be notified")
	}
	ctx, _ := e.Context("TASK-008")
	if !strings.Contains(ctx, "Contract changes since you started") || !strings.Contains(ctx, "v1 → v2") {
		t.Fatal("work packet must carry the change banner")
	}
	attention := strings.Join(e.Status().Attention, "\n")
	if !strings.Contains(attention, "BillingStatus changed") {
		t.Fatalf("attention: %s", attention)
	}
	drifted := false
	for _, c := range e.Drift().Context {
		drifted = drifted || c.Task == "TASK-008"
	}
	if !drifted {
		t.Fatal("context drift must list TASK-008")
	}
	if err := e.Ack("TASK-008"); err != nil {
		t.Fatal(err)
	}
	if len(e.StaleContracts(t8)) != 0 {
		t.Fatal("ack must clear staleness")
	}
	out := testutil.Git(t, dir, "show", "wbi/TASK-007:.wbi/contracts/BillingStatus.json")
	var c model.Contract
	if err := json.Unmarshal([]byte(out), &c); err != nil || c.Version != 2 {
		t.Fatalf("the version bump must travel with the PR branch: %v %+v", err, c)
	}
}

func TestConstitutionForbidRulesFailVerificationAndShowAsBranchDrift(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	through(t, e, "TASK-001", "TASK-002", "TASK-004", "TASK-007")
	bad := finish(t, e, "TASK-010", codex, map[string]string{"src/api/analytics/summary.ts": "export const f = (db) => db.billing.update({})"}, nil)
	if bad.Verdict != "FAILED" {
		t.Fatalf("verdict %s", bad.Verdict)
	}
	for _, c := range bad.Checks {
		if c.Name == "Constitution" && !strings.Contains(c.Detail, "C6") {
			t.Fatalf("constitution detail %q", c.Detail)
		}
	}
	ok := false
	for _, v := range e.Drift().Architecture {
		ok = ok || (v.Rule.ID == "C6" && v.Branch == "wbi/TASK-010")
	}
	if !ok {
		t.Fatal("drift must flag the violation on the unmerged branch")
	}
}

func TestBlameAndWhySeeUnmergedAgentWork(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	through(t, e, "TASK-001", "TASK-002", "TASK-004")
	b := e.Blame("src/api/auth/", 5)
	if len(b.Entries) == 0 {
		t.Fatal("no blame entries")
	}
	first := b.Entries[0]
	if first.TaskID != "TASK-004" || first.Provider != "claude" || !first.IsAgent || first.Ref != "wbi/TASK-004" {
		t.Fatalf("entry %+v", first)
	}
	w := e.Why("src/api/auth/index.ts")
	if w.Task == nil || w.Task.ID != "TASK-004" || len(w.Reqs) == 0 || w.Reqs[0].ID != "REQ-001" || w.Executor != "claude@maya" {
		t.Fatalf("why %+v", w)
	}
}

func TestDriftProductMatrixReflectsRealState(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	through(t, e, "TASK-001", "TASK-002")
	d := e.Drift()
	req := d.Product[0]
	states := map[string]string{}
	for _, l := range req.Layers {
		states[l.Layer] = l.State
	}
	if states["database"] != "done" || states["backend"] != "todo" || req.Satisfied {
		t.Fatalf("matrix %+v", states)
	}
}

func TestSimulateWavesCriticalPathAndMissingDeps(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	s := e.Simulate(3)
	if s.Waves[0][0].ID != "TASK-001" || len(s.CriticalPath) < 5 || len(s.MissingDeps) != 0 || len(s.Conflicts) != 0 {
		t.Fatalf("sim %+v", s)
	}
	t5, _ := e.Task("TASK-005")
	t5.DependsOn = nil
	if err := e.Store.SaveTask(t5); err != nil {
		t.Fatal(err)
	}
	broken := false
	for _, m := range e.Simulate(3).MissingDeps {
		broken = broken || strings.Contains(m, "TASK-005")
	}
	if !broken {
		t.Fatal("removing the dependency must be reported")
	}
}

func TestVerifyWithoutHandoffErrorsAndReleaseFreesTask(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	if _, err := e.VerifyTask("TASK-001"); err == nil || !strings.Contains(err.Error(), "no handoff") {
		t.Fatalf("got %v", err)
	}
	if _, err := e.Claim("TASK-001", claude, false); err != nil {
		t.Fatal(err)
	}
	if err := e.Release("TASK-001"); err != nil {
		t.Fatal(err)
	}
	t1, _ := e.Task("TASK-001")
	if e.Display(t1, e.States()) != model.Ready {
		t.Fatal("released task must be READY")
	}
}

func TestInitRefusesNonGitAndDoubleInit(t *testing.T) {
	if _, err := engine.Init(t.TempDir(), engine.InitOpts{NoHook: true}); err == nil {
		t.Fatal("init outside git must fail")
	}
	dir := testutil.MakeRepo(t, nil)
	if _, err := engine.Init(dir, engine.InitOpts{NoHook: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Init(dir, engine.InitOpts{NoHook: true}); err == nil {
		t.Fatal("double init must fail without --force")
	}
}

func TestAdaptersAreIdempotentAndPreserveUserContent(t *testing.T) {
	dir := testutil.MakeRepo(t, map[string]string{"CLAUDE.md": "# my notes\nkeep me\n"})
	for i := 0; i < 2; i++ {
		if _, err := engine.InstallGuidance(dir, "claude"); err != nil {
			t.Fatal(err)
		}
	}
	b := testutil.ReadFile(t, dir, "CLAUDE.md")
	if !strings.Contains(b, "keep me") || strings.Count(b, "wbi:begin") != 1 {
		t.Fatalf("CLAUDE.md:\n%s", b)
	}
	if _, err := engine.InstallGuidance(dir, "nope"); err == nil {
		t.Fatal("unknown adapter must error")
	}
}

// Task branches have a fixed, documented name. Asserted as a literal on purpose: a test that merely reads
// back whatever the code produced cannot catch a corrupted prefix (this once shipped in v0.1.0).
func TestTaskBranchNameIsWbiSlashTaskID(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	r, err := e.Claim("TASK-001", claude, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.State.Branch != "wbi/TASK-001" {
		t.Fatalf("branch must be wbi/TASK-001, got %q", r.State.Branch)
	}
	ctx, _ := e.Context("TASK-001")
	if !strings.Contains(ctx, "**Branch:** wbi/TASK-001") {
		t.Fatal("work packet must show the real branch")
	}
	s, err := e.Start("TASK-001", claude, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, s.Workdir, "rev-parse", "--abbrev-ref", "HEAD"); strings.TrimSpace(got) != "wbi/TASK-001" {
		t.Fatalf("worktree is on %q", got)
	}
}

func TestBuildPRFromHandoff(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	if _, err := e.BuildPR("TASK-007"); err == nil || !strings.Contains(err.Error(), "no handoff") {
		t.Fatalf("a PR needs a handoff first: %v", err)
	}
	through(t, e, "TASK-001", "TASK-002", "TASK-004")
	finish(t, e, "TASK-007", claude, map[string]string{"src/api/billing/status.ts": "export type S = 'paused'"}, func(o *engine.HandoffOpts) {
		o.Contracts = []string{"BillingStatus=now supports paused"}
		o.Limitations = []string{"Refund API not implemented"}
	})
	d, err := e.BuildPR("TASK-007")
	if err != nil {
		t.Fatal(err)
	}
	if d.Branch != "wbi/TASK-007" || d.Base != "main" || d.Title != "TASK-007: Billing API" {
		t.Fatalf("draft header: %+v", d)
	}
	for _, want := range []string{"**Requirement:** REQ-002", "`src/api/billing/status.ts`", "**BillingStatus** → v2: now supports paused", "Refund API not implemented", "High-impact change (security, public-api)", "claude@maya", "Downstream tasks affected"} {
		if !strings.Contains(d.Body, want) {
			t.Errorf("PR body missing %q:\n%s", want, d.Body)
		}
	}
	got := strings.Join(d.Labels, ",")
	if !strings.Contains(got, "contract-change") || !strings.Contains(got, "high-impact") {
		t.Fatalf("labels %s", got)
	}
}
