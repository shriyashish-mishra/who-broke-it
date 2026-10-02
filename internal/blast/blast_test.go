package blast

import (
	"reflect"
	"sort"
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

func task(id, comp string, deps, provides, consumes []string, allowed ...string) model.Task {
	t := model.Task{ID: id, Component: comp, DependsOn: deps, Provides: provides, Consumes: consumes, AllowedPaths: allowed, Effort: 1}
	t.Normalize()
	return t
}

func world() Input {
	tasks := []model.Task{
		task("API", "api", nil, []string{"Status"}, nil, "src/api/**"),
		task("UI", "ui", []string{"API"}, nil, []string{"Status"}, "src/ui/**"),
		task("E2E", "e2e", []string{"UI"}, nil, nil, "tests/**"),
		task("OTHER", "other", nil, nil, nil, "src/other/**"),
	}
	st := map[string]model.TaskState{"UI": {AgentID: "g@x", Status: model.InProgress}}
	return Input{
		Tasks:     tasks,
		Contracts: []model.Contract{{Name: "Status", ProvidedBy: "API", Public: true}},
		Components: []model.Component{
			{ID: "api"}, {ID: "ui", DependsOn: []string{"api"}}, {ID: "e2e", DependsOn: []string{"ui"}}, {ID: "other"},
		},
		State: func(id string) (model.TaskState, bool) { s, ok := st[id]; return s, ok },
	}
}

func sorted(s []string) []string { c := append([]string{}, s...); sort.Strings(c); return c }

func TestContractBlast(t *testing.T) {
	r := Radius(world(), "Status")
	if r.Kind != "contract" || !reflect.DeepEqual(r.DirectTasks, []string{"UI"}) || !reflect.DeepEqual(r.IndirectTasks, []string{"E2E"}) {
		t.Fatalf("%+v", r)
	}
	if !reflect.DeepEqual(sorted(r.DirectComps), []string{"ui"}) || !reflect.DeepEqual(sorted(r.IndirectComps), []string{"e2e"}) {
		t.Fatalf("components %+v / %+v", r.DirectComps, r.IndirectComps)
	}
	if len(r.ActiveAgents) != 1 || r.ActiveAgents[0].AgentID != "g@x" || r.Why["UI"] != "consumes Status" {
		t.Fatalf("agents/why %+v %+v", r.ActiveAgents, r.Why)
	}
	if r.Risk != "HIGH" { // public contract with an active agent building on it
		t.Fatalf("risk %s %v", r.Risk, r.Reasons)
	}
}

func TestTaskAndPathBlast(t *testing.T) {
	r := Radius(world(), "API")
	if r.Kind != "task" || !reflect.DeepEqual(sorted(r.DirectTasks), []string{"UI"}) {
		t.Fatalf("task blast %+v", r)
	}
	p := Radius(world(), "src/api/status.ts")
	if p.Kind != "path" || !reflect.DeepEqual(p.DirectTasks, []string{"UI"}) {
		t.Fatalf("path blast follows the owner's contracts: %+v", p)
	}
	if o := Radius(world(), "src/other/x.ts"); len(o.DirectTasks)+len(o.IndirectTasks) != 0 || o.Risk != "LOW" {
		t.Fatalf("isolated path must be LOW and empty: %+v", o)
	}
	if u := Radius(world(), "nowhere/file.go"); u.Kind != "path" || u.Risk != "LOW" {
		t.Fatalf("unowned path: %+v", u)
	}
}

func TestDoneWorkRaisesRisk(t *testing.T) {
	in := world()
	in.State = func(id string) (model.TaskState, bool) {
		if id == "E2E" {
			return model.TaskState{Status: model.Done}, true
		}
		return model.TaskState{}, false
	}
	in.Contracts[0].Public = false
	r := Radius(in, "Status")
	if r.Risk != "HIGH" {
		t.Fatalf("a DONE affected task means rework: %s %v", r.Risk, r.Reasons)
	}
}
