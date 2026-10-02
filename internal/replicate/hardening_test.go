package replicate_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
	"github.com/shriyashish-mishra/who-broke-it/internal/replicate"
	"github.com/shriyashish-mishra/who-broke-it/internal/testutil"
)

// git with stdin and a throwaway index, to forge a commit the way an attacker with push access could.
func gitIn(t *testing.T, dir, stdin string, env []string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), append([]string{"GIT_AUTHOR_NAME=x", "GIT_AUTHOR_EMAIL=x@x", "GIT_COMMITTER_NAME=x", "GIT_COMMITTER_EMAIL=x@x"}, env...)...)
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestForgedBatchesAreRejectedEvenWithoutEnforcement(t *testing.T) {
	m := testutil.Team(t)
	if _, err := m.A.Claim("TASK-001", alice, false); err != nil {
		t.Fatal(err)
	}
	mustSync(t, m.A)

	// an attacker with push access edits alice's signed batch and publishes it on top of the log
	dir := m.DirB
	gitIn(t, dir, "", nil, "fetch", "-q", "origin", "+refs/wbi/sync:refs/wbi/forge")
	tip := gitIn(t, dir, "", nil, "rev-parse", "refs/wbi/forge")
	var file string
	for _, f := range strings.Fields(gitIn(t, dir, "", nil, "ls-tree", "-r", "--name-only", tip)) {
		if strings.HasPrefix(f, "log/") {
			file = f
		}
	}
	body := gitIn(t, dir, "", nil, "show", tip+":"+file) + "\n"
	forged := strings.ReplaceAll(body, `"agent_id":"claude@alice"`, `"agent_id":"mallory@evil"`)
	if forged == body {
		t.Fatal("test setup: nothing to tamper with")
	}
	blob := gitIn(t, dir, forged, nil, "hash-object", "-w", "--stdin")
	idx := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "idx")}
	gitIn(t, dir, "", idx, "read-tree", tip)
	gitIn(t, dir, "", idx, "update-index", "--add", "--cacheinfo", "100644,"+blob+",log/mallory/forged.jsonl")
	tree := gitIn(t, dir, "", idx, "write-tree")
	commit := gitIn(t, dir, "", idx, "commit-tree", tree, "-p", tip, "-m", "totally legit")
	gitIn(t, dir, "", nil, "push", "-q", "origin", commit+":refs/wbi/sync")

	r := mustSync(t, m.B)
	if len(r.Quarantined) != 1 || !strings.Contains(r.Quarantined[0], "does not verify") {
		t.Fatalf("the forged batch must be quarantined: %+v", r.Quarantined)
	}
	if got := m.B.State("TASK-001").AgentID; got != "claude@alice" {
		t.Fatalf("forged ownership must not be applied, got %q", got)
	}
	// alice's genuine batch from before is still applied
	if m.B.State("TASK-001").Status != model.InProgress {
		t.Fatal("the genuine claim must survive")
	}
}

func TestEnforcedTeamIgnoresOutsidersUntilTheyAreAuthorized(t *testing.T) {
	m := testutil.Team(t)
	if _, err := m.A.TeamJoin("Alice", true); err != nil {
		t.Fatal(err)
	}
	mustSync(t, m.A)

	// bob is not on the team: his signed events are real but not authorized
	if _, err := m.B.Claim("TASK-001", bob, false); err != nil {
		t.Fatal(err)
	}
	mustSync(t, m.B)
	r := mustSync(t, m.A)
	if len(r.Quarantined) == 0 || !strings.Contains(r.Quarantined[0], "not in .wbi/team.json") {
		t.Fatalf("alice must quarantine bob's batch: %+v", r.Quarantined)
	}
	if got := m.A.State("TASK-001").AgentID; got != "" {
		t.Fatalf("an unauthorized claim must not be applied, got %q", got)
	}

	// alice authorizes bob's key (in real life: a reviewed PR to .wbi/team.json); the next sync re-verifies the log
	id, _ := m.B.Identity()
	if err := m.A.TeamAdd("Bob", id.PubB64()); err != nil {
		t.Fatal(err)
	}
	r = mustSync(t, m.A)
	if len(r.Quarantined) != 0 {
		t.Fatalf("nothing should be quarantined now: %+v", r.Quarantined)
	}
	if got := m.A.State("TASK-001").AgentID; got != "codex@bob" {
		t.Fatalf("after authorization bob's earlier claim applies, got %q", got)
	}
	// removing him again stops applying his events (full re-verification on policy change)
	if err := m.A.TeamRemove("Bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.B.Claim("TASK-002", bob, true); err != nil {
		t.Fatal(err)
	}
	mustSync(t, m.B)
	if r := mustSync(t, m.A); len(r.Quarantined) == 0 {
		t.Fatal("a removed member's new batches must be quarantined")
	}
}

func TestCompactionKeepsStateAndShrinksTheLog(t *testing.T) {
	m := testutil.Team(t)
	steps := []func(){
		func() { m.A.Claim("TASK-001", alice, false) },
		func() { m.B.Claim("TASK-003", bob, true) },
		func() { m.A.DeclareIntent(alice, "TASK-001", model.Modify, "docs/a.md", "") },
		func() { m.B.DeclareIntent(bob, "TASK-003", model.Modify, "src/web/shell/x.ts", "") },
	}
	for _, s := range steps {
		s()
		mustSync(t, m.A)
		mustSync(t, m.B)
	}
	count := func(e *engine.Engine, dir string) string {
		return strings.TrimSpace(testutil.Git(t, dir, "rev-list", "--count", "refs/wbi/tracking/wbi_sync"))
	}
	if n := count(m.A, m.DirA); n == "1" || n == "0" {
		t.Fatalf("log should have several commits before compaction, has %s", n)
	}
	if _, err := m.A.Compact(100); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if n := count(m.A, m.DirA); n != "1" {
		t.Fatalf("log must be one commit after compaction, has %s", n)
	}

	// bob has an unpublished change when the history is rewritten under him
	if _, err := m.B.Claim("TASK-008", bob, true); err != nil {
		t.Fatal(err)
	}
	mustSync(t, m.B)
	mustSync(t, m.A)

	// a brand-new clone bootstraps from the snapshot alone
	dirC := filepath.Join(t.TempDir(), "carol")
	testutil.Git(t, filepath.Dir(dirC), "clone", "-q", m.Remote, dirC)
	c, err := engine.New(dirC)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Warn = func(string) {}
	mustSync(t, c)

	for name, e := range map[string]*engine.Engine{"A": m.A, "B": m.B, "C": c} {
		want := map[string]string{"TASK-001": "claude@alice", "TASK-003": "codex@bob", "TASK-008": "codex@bob"}
		for id, agent := range want {
			if got := e.State(id).AgentID; got != agent {
				t.Errorf("%s: %s owner %q want %q", name, id, got, agent)
			}
		}
		if n := len(e.Intents(false)); n != 2 {
			t.Errorf("%s: want 2 active intents, got %d", name, n)
		}
	}
}

func TestCompactionNeverClobbersAConcurrentPush(t *testing.T) {
	m := testutil.Team(t)
	if _, err := m.A.Claim("TASK-001", alice, false); err != nil {
		t.Fatal(err)
	}
	mustSync(t, m.A)
	mustSync(t, m.B)
	if _, err := m.B.Claim("TASK-003", bob, true); err != nil {
		t.Fatal(err)
	}
	fired := false // a flag, not sync.Once: bob's own sync re-enters this hook
	replicate.BeforePush = func() {
		if fired {
			return
		}
		fired = true
		mustSync(t, m.B) // bob publishes while alice is compacting
	}
	defer func() { replicate.BeforePush = nil }()

	if _, err := m.A.Compact(100); err != nil {
		t.Fatalf("compaction must retry, not fail: %v", err)
	}
	mustSync(t, m.B)
	mustSync(t, m.A)
	for name, e := range map[string]*engine.Engine{"A": m.A, "B": m.B} {
		if got := e.State("TASK-003").AgentID; got != "codex@bob" {
			t.Fatalf("%s lost bob's concurrent claim during compaction: %q", name, got)
		}
	}
}

func TestWatchDeliversTeammatesChanges(t *testing.T) {
	m := testutil.Team(t)
	got := make(chan replicate.Report, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- m.A.Watch(ctx, 80*time.Millisecond, func(r replicate.Report) { got <- r })
	}()
	time.Sleep(200 * time.Millisecond)
	if _, err := m.B.Claim("TASK-001", bob, false); err != nil {
		t.Fatal(err)
	}
	mustSync(t, m.B)
	select {
	case r := <-got:
		if r.Applied == 0 {
			t.Fatalf("watch should report applied events: %+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watch never noticed bob's claim")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch must stop when its context is cancelled")
	}
	if m.A.State("TASK-001").AgentID != "codex@bob" {
		t.Fatal("alice's database should now know about bob's claim")
	}
}
