package graph

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"wbi/internal/model"
)

func task(id string, deps ...string) model.Task {
	t := model.Task{ID: id, Title: id, DependsOn: deps, Effort: 1}
	t.Normalize()
	return t
}

func TestCycleDetection(t *testing.T) {
	if c := FindCycle([]model.Task{task("A"), task("B", "A")}); c != nil {
		t.Fatalf("unexpected cycle %v", c)
	}
	c := FindCycle([]model.Task{task("A", "C"), task("B", "A"), task("C", "B")})
	if c == nil || c[0] != c[len(c)-1] {
		t.Fatalf("expected closed cycle, got %v", c)
	}
	found := false
	for _, i := range Validate([]model.Task{task("A", "B"), task("B", "A")}, nil) {
		found = found || strings.Contains(strings.ToLower(i.Message), "cycle")
	}
	if !found {
		t.Fatal("Validate should report the cycle")
	}
}

func TestValidateUnknowns(t *testing.T) {
	bad := task("A", "Z")
	bad.Consumes = []string{"X"}
	errs := 0
	for _, i := range Validate([]model.Task{bad}, nil) {
		if i.Severity == "error" {
			errs++
		}
	}
	if errs != 2 {
		t.Fatalf("want 2 errors, got %d", errs)
	}
}

func TestWavesCriticalPathDependents(t *testing.T) {
	ts := []model.Task{task("A"), task("B", "A"), task("C", "A"), task("D", "B", "C")}
	if got := Waves(ts); !reflect.DeepEqual(got, [][]string{{"A"}, {"B", "C"}, {"D"}}) {
		t.Fatalf("waves %v", got)
	}
	if got := len(CriticalPath(ts)); got != 3 {
		t.Fatalf("critical path len %d", got)
	}
	all := Dependents(ts, "A", true)
	sort.Strings(all)
	if !reflect.DeepEqual(all, []string{"B", "C", "D"}) {
		t.Fatalf("dependents %v", all)
	}
	direct := Dependents(ts, "A", false)
	sort.Strings(direct)
	if !reflect.DeepEqual(direct, []string{"B", "C"}) {
		t.Fatalf("direct dependents %v", direct)
	}
}

func TestReadyVsBlocked(t *testing.T) {
	a, b := task("A"), task("B", "A")
	st := map[string]string{"A": model.Todo, "B": model.Todo}
	get := func(id string) string { return st[id] }
	if DisplayStatus(a, get) != model.Ready || DisplayStatus(b, get) != model.Blocked {
		t.Fatal("A should be READY and B BLOCKED")
	}
	st["A"] = model.Done
	if DisplayStatus(b, get) != model.Ready {
		t.Fatal("B should be READY once A is DONE")
	}
}
