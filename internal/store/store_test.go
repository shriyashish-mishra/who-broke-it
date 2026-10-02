package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/model"
	"github.com/shriyashish-mishra/who-broke-it/internal/store"
	"github.com/shriyashish-mishra/who-broke-it/internal/testutil"
)

func TestRoundTripAndJSONShape(t *testing.T) {
	dir := testutil.MakeRepo(t, nil)
	s := &store.Store{Root: dir}
	if err := s.SaveProject(model.Project{Version: 1, Name: "x", BaseBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	// nil slices must serialize as [] so every consumer (and the JSON schema) sees arrays
	if err := s.SaveTask(model.Task{ID: "TASK-001", Title: "t"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".wbi", "tasks", "TASK-001.json"))
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	for _, k := range []string{"dependsOn", "allowedPaths", "acceptance", "provides", "consumes", "impact"} {
		if arr, ok := m[k].([]any); !ok || len(arr) != 0 {
			t.Errorf("%s should be [], got %v", k, m[k])
		}
	}
	got, err := s.Task("TASK-001")
	if err != nil || got.Effort != 2 {
		t.Fatalf("task %+v %v", got, err)
	}
	if _, err := s.Task("TASK-404"); err == nil || !strings.Contains(err.Error(), "Unknown task") {
		t.Fatalf("unknown task: %v", err)
	}
	// contract names like "GET /api/billing" become safe file names
	if err := s.SaveContract(model.Contract{Name: "GET /api/billing", ProvidedBy: "TASK-001"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".wbi", "contracts", "GET_api_billing.json")); err != nil {
		t.Fatal(err)
	}
	if c := s.Contracts(); len(c) != 1 || c[0].Name != "GET /api/billing" || c[0].Version != 1 {
		t.Fatalf("contracts %+v", c)
	}
}

func TestFindWalksUpAndFallsBackToTheMainWorktree(t *testing.T) {
	dir := testutil.MakeRepo(t, nil)
	if _, err := store.Find(dir); err == nil {
		t.Fatal("no graph yet")
	}
	s := &store.Store{Root: dir}
	_ = s.SaveProject(model.Project{Version: 1, Name: "x", BaseBranch: "main"})
	deep := filepath.Join(dir, "a", "b", "c")
	_ = os.MkdirAll(deep, 0o755)
	got, err := store.Find(deep)
	if err != nil || got.Root != dir {
		t.Fatalf("walk-up: %v %v", got, err)
	}
	// a linked worktree whose checkout has no .wbi falls back to the main worktree's graph
	wt := filepath.Join(filepath.Dir(dir), "wt")
	testutil.Git(t, dir, "worktree", "add", "-q", "-b", "side", wt)
	got, err = store.Find(wt)
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	a, _ := filepath.EvalSymlinks(got.Root)
	b, _ := filepath.EvalSymlinks(dir)
	if a != b {
		t.Fatalf("fallback root %q want %q", a, b)
	}
}

func TestWritePlanReplacesEverything(t *testing.T) {
	dir := testutil.MakeRepo(t, nil)
	s := &store.Store{Root: dir}
	_ = s.SaveProject(model.Project{Version: 1, Name: "x", BaseBranch: "main"})
	p1 := model.Plan{Tasks: []model.Task{{ID: "TASK-001"}, {ID: "TASK-002"}}}
	p1.Normalize("now")
	_ = s.WritePlan(p1)
	p2 := model.Plan{Tasks: []model.Task{{ID: "TASK-009"}}}
	p2.Normalize("now")
	_ = s.WritePlan(p2)
	if ts := s.Tasks(); len(ts) != 1 || ts[0].ID != "TASK-009" {
		t.Fatalf("stale tasks survived a re-plan: %+v", ts)
	}
}
