package gitx_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/gitx"
	"github.com/shriyashish-mishra/who-broke-it/internal/testutil"
)

func TestBasics(t *testing.T) {
	dir := testutil.MakeRepo(t, nil)
	if !gitx.IsRepo(dir) || gitx.IsRepo(t.TempDir()) {
		t.Fatal("IsRepo")
	}
	if gitx.CurrentBranch(dir) != "main" || gitx.DefaultBranch(dir) != "main" {
		t.Fatalf("branch %q / default %q", gitx.CurrentBranch(dir), gitx.DefaultBranch(dir))
	}
	if !gitx.RefExists(dir, "main") || gitx.RefExists(dir, "nope") {
		t.Fatal("RefExists")
	}
	if got := filepath.Base(gitx.Toplevel(dir)); got != "repo" {
		t.Fatalf("toplevel %q", got)
	}
	if !strings.HasSuffix(gitx.CommonDir(dir), ".git") {
		t.Fatalf("common dir %q", gitx.CommonDir(dir))
	}
	if _, err := gitx.Git(dir, "definitely-not-a-command"); err == nil {
		t.Fatal("bad git commands must error")
	}
	if _, ok := gitx.Try(dir, "rev-parse", "--verify", "--quiet", "missing"); ok {
		t.Fatal("Try must report failure")
	}
}

func TestDiffsLogsAndShow(t *testing.T) {
	dir := testutil.MakeRepo(t, map[string]string{"a.txt": "one\n"})
	testutil.Git(t, dir, "checkout", "-q", "-b", "feat")
	testutil.Put(t, dir, "a.txt", "one\ntwo\n")
	testutil.Put(t, dir, "src/b.go", "package b\nvar x = 1\n")
	testutil.CommitAll(t, dir, "add things\n\nWBI-Task: TASK-009")
	if got := gitx.ChangedFiles(dir, "main", "feat"); strings.Join(got, ",") != "a.txt,src/b.go" {
		t.Fatalf("changed files %v", got)
	}
	added := gitx.AddedLines(dir, "main", "feat")
	byFile := map[string][]string{}
	for _, f := range added {
		byFile[f.File] = f.Lines
	}
	if len(byFile["a.txt"]) != 1 || byFile["a.txt"][0] != "two" || len(byFile["src/b.go"]) != 2 {
		t.Fatalf("added lines %+v", byFile)
	}
	if c := gitx.CommitsAhead(dir, "main", "feat"); len(c) != 1 || c[0].Subject != "add things" {
		t.Fatalf("commits ahead %+v", c)
	}
	// log across branches reports which branch a commit lives on
	testutil.Git(t, dir, "checkout", "-q", "main")
	logs := gitx.LogFor(dir, "src/b.go", 10, nil, true)
	if len(logs) != 1 || logs[0].Ref != "feat" || !strings.Contains(logs[0].Body, "WBI-Task: TASK-009") {
		t.Fatalf("log %+v", logs)
	}
	if len(gitx.LogFor(dir, "src/b.go", 10, nil, false)) != 0 {
		t.Fatal("without branches=true the unmerged commit must not appear")
	}
	if body, ok := gitx.Show(dir, "feat", "a.txt"); !ok || body != "one\ntwo" {
		t.Fatalf("show %q %v", body, ok)
	}
	if _, ok := gitx.Show(dir, "main", "src/b.go"); ok {
		t.Fatal("file does not exist on main")
	}
	if m := gitx.Messages(dir, "main", "feat"); len(m) != 1 || !strings.Contains(m[0], "TASK-009") {
		t.Fatalf("messages %v", m)
	}
	files := gitx.LsFiles(dir)
	if len(files) != 1 || files[0] != "a.txt" {
		t.Fatalf("ls-files on main %v", files)
	}
}

func TestWorktreeFindsMain(t *testing.T) {
	dir := testutil.MakeRepo(t, nil)
	wt := filepath.Join(filepath.Dir(dir), "wt")
	testutil.Git(t, dir, "worktree", "add", "-q", "-b", "side", wt)
	main, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(gitx.MainWorktree(wt))
	if got != main {
		t.Fatalf("main worktree %q want %q", got, main)
	}
	if _, err := os.Stat(gitx.CommonDir(wt)); err != nil {
		t.Fatal(err)
	}
}
