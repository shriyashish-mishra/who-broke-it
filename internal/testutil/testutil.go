// Package testutil builds real throwaway git repos for tests.
package testutil

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
)

// quietGit stops git from spawning detached background maintenance (gc --auto) in test repos. That process can
// still be writing into .git when t.TempDir() cleanup runs, which fails the test with "directory not empty".
func quietGit(t testing.TB) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "gc.auto")
	t.Setenv("GIT_CONFIG_VALUE_0", "0")
	t.Setenv("GIT_CONFIG_KEY_1", "maintenance.auto")
	t.Setenv("GIT_CONFIG_VALUE_1", "false")
}

// Git runs git in dir and fails the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.name=Tester", "-c", "user.email=t@example.com"}, args...)...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// MakeRepo creates a git repo (inside t.TempDir()) with one commit.
func MakeRepo(t testing.TB, files map[string]string) string {
	t.Helper()
	quietGit(t)
	t.Setenv("WBI_NO_GH", "1")
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	Git(t, dir, "init", "-q", "-b", "main")
	if files == nil {
		files = map[string]string{"README.md": "# demo\n"}
	}
	for f, body := range files {
		Put(t, dir, f, body)
	}
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-qm", "initial")
	return dir
}

func Put(t testing.TB, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func CommitAll(t testing.TB, dir, msg string) {
	t.Helper()
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-qm", msg)
}

// PlannedRepo returns an initialized + planned repo (auth, billing, analytics) with the plan committed.
func PlannedRepo(t testing.TB) (string, *engine.Engine) {
	t.Helper()
	dir := MakeRepo(t, nil)
	if _, err := engine.Init(dir, engine.InitOpts{NoHook: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.PlanProject(dir, "Build a SaaS app with authentication, billing and analytics", engine.PlanOpts{}); err != nil {
		t.Fatal(err)
	}
	CommitAll(t, dir, "chore: wbi plan")
	e, err := engine.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return dir, e
}

func ReadFile(t testing.TB, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TeamEnv is two "machines": separate clones (each with its own runtime state) sharing a bare remote.
type TeamEnv struct {
	Remote     string
	DirA, DirB string
	A, B       *engine.Engine
}

// Team builds a bare remote, clone A (initialized, planned, sync enabled, pushed) and clone B (fresh clone).
func Team(t testing.TB) *TeamEnv {
	t.Helper()
	dirA := MakeRepo(t, nil)
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", "-q", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	Git(t, dirA, "remote", "add", "origin", remote)
	if _, err := engine.Init(dirA, engine.InitOpts{NoHook: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.PlanProject(dirA, "Build a SaaS app with authentication, billing and analytics", engine.PlanOpts{}); err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(dirA)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.EnableSync("origin", ""); err != nil {
		t.Fatal(err)
	}
	e.Close()
	CommitAll(t, dirA, "chore: wbi plan + sync")
	Git(t, dirA, "push", "-q", "origin", "main")

	dirB := filepath.Join(t.TempDir(), "bob")
	Git(t, filepath.Dir(dirB), "clone", "-q", remote, dirB)

	quiet := func(string) {}
	a, err := engine.New(dirA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := engine.New(dirB)
	if err != nil {
		t.Fatal(err)
	}
	a.Warn, b.Warn = quiet, quiet
	t.Cleanup(func() { a.Close(); b.Close() })
	return &TeamEnv{Remote: remote, DirA: dirA, DirB: dirB, A: a, B: b}
}

// EditJSON rewrites a JSON object file in place via fn.
func EditJSON(t testing.TB, path string, fn func(map[string]any)) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	fn(m)
	out, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// MakeRepoAt is MakeRepo at an exact path (for tests that need sibling repos).
func MakeRepoAt(t testing.TB, dir string) string {
	t.Helper()
	quietGit(t)
	t.Setenv("WBI_NO_GH", "1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	Git(t, dir, "init", "-q", "-b", "main")
	Put(t, dir, "README.md", "# "+filepath.Base(dir)+"\n")
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-qm", "initial")
	return dir
}
