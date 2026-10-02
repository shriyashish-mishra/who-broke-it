package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/testutil"
)

// Many real processes (like many agents in many terminals) hammer one repo's SQLite state at once.
// No call may fail with "database is locked", and nothing may be lost or duplicated.
func TestManyProcessesShareOneDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	bin := filepath.Join(t.TempDir(), "wbi")
	if runtime := os.Getenv("GOOS"); runtime == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	dir := testutil.MakeRepo(t, nil)
	run := func(env []string, args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir = dir
		c.Env = append(os.Environ(), append([]string{"WBI_NO_SYNC=1", "WBI_NO_GH=1"}, env...)...)
		out, err := c.CombinedOutput()
		return string(out), err
	}
	if out, err := run(nil, "init", "--no-hook"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := run(nil, "plan", "Build a SaaS app with authentication, billing and analytics"); err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}

	const n = 24
	var wg sync.WaitGroup
	errs := make(chan string, n*3)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			env := []string{"WBI_AGENT=agent", fmt.Sprintf("WBI_DEVELOPER=dev%02d", i)}
			// a claim race on TASK-001: exactly one winner
			if out, err := run(env, "claim", "TASK-001"); err != nil && !strings.Contains(out, "already claimed") {
				errs <- fmt.Sprintf("claim %d: %v %s", i, err, out)
			}
			// everyone declares an intent: needs a claimed task, so use --task and accept warnings
			if out, err := run(env, "agents", "register"); err != nil {
				errs <- fmt.Sprintf("register %d: %v %s", i, err, out)
			}
			if out, err := run(env, "intent", "declare", "MODIFY", fmt.Sprintf("docs/f%02d.md", i), "--task", "TASK-001"); err != nil && !strings.Contains(out, "NOT registered") {
				errs <- fmt.Sprintf("intent %d: %v %s", i, err, out)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}

	out, _ := run(nil, "agents")
	if got := strings.Count(out, "agent "); got != n {
		t.Errorf("want %d registered agents, got %d:\n%s", n, got, out)
	}
	out, _ = run(nil, "tasks", "--status", "in_progress")
	if strings.Count(out, "TASK-001") != 1 {
		t.Errorf("exactly one agent may own TASK-001:\n%s", out)
	}
	out, _ = run(nil, "intent")
	if got := strings.Count(out, "MODIFY"); got != n {
		t.Errorf("want %d intents, got %d", n, got)
	}
}
