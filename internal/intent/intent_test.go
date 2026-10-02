package intent

import (
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

func tk(id string, allowed, restricted []string) model.Task {
	t := model.Task{ID: id, AllowedPaths: allowed, RestrictedPaths: restricted}
	t.Normalize()
	return t
}

func ctx(task model.Task, others []model.Task, active []model.Intent, st map[string]model.TaskState) Ctx {
	return Ctx{
		Task: task, Tasks: append([]model.Task{task}, others...), AgentID: "me@x", Active: active,
		Contracts: []model.Contract{{Name: "C", ProvidedBy: "T2"}},
		State:     func(id string) (model.TaskState, bool) { s, ok := st[id]; return s, ok },
		ConsumersActive: func(string) []ActiveConsumer {
			return []ActiveConsumer{{TaskID: "T3", AgentID: "other@y"}, {TaskID: "T9", AgentID: "me@x"}}
		},
		ProtectedPaths: []string{"migrations/**"},
	}
}

func codes(cs []model.Conflict) map[string]model.Severity {
	m := map[string]model.Severity{}
	for _, c := range cs {
		m[c.Code] = c.Severity
	}
	return m
}

func TestPathIntents(t *testing.T) {
	me := tk("T1", []string{"src/a/**"}, []string{"src/b/**"})
	owner := tk("T2", []string{"src/b/**"}, nil)
	st := map[string]model.TaskState{"T2": {AgentID: "other@y", Status: model.InProgress}}

	if c := Detect(model.Modify, "src/a/x.ts", ctx(me, []model.Task{owner}, nil, st)); len(c) != 0 {
		t.Fatalf("in-scope must be clean: %+v", c)
	}
	c := codes(Detect(model.Modify, "src/b/x.ts", ctx(me, []model.Task{owner}, nil, st)))
	if c["RESTRICTED_PATH"] != model.Block || c["OWNERSHIP_CONFLICT"] != model.Warn {
		t.Fatalf("restricted + active owner: %+v", c)
	}
	if c := codes(Detect(model.Modify, "docs/x.md", ctx(me, nil, nil, st))); c["OUT_OF_SCOPE"] != model.Warn || len(c) != 1 {
		t.Fatalf("out of scope is soft: %+v", c)
	}
	if c := codes(Detect(model.Create, "migrations/1.sql", ctx(me, nil, nil, st))); c["RISKY_PATH"] != model.Warn {
		t.Fatalf("migrations are risky: %+v", c)
	}
}

func TestOverlapRules(t *testing.T) {
	me := tk("T1", []string{"src/**"}, nil)
	other := func(kind, target string) []model.Intent {
		return []model.Intent{{AgentID: "other@y", TaskID: "T5", Kind: kind, Target: target}}
	}
	cases := []struct {
		mine, theirsKind, theirs, want string
		sev                            model.Severity
	}{
		{model.Modify, model.Modify, "src/a/*.ts", "OVERLAP", model.Warn},
		{model.Create, model.Create, "src/a/new.ts", "DUPLICATE_WORK", model.Block},
		{model.Modify, model.Delete, "src/a/**", "DELETE_CONFLICT", model.Block},
		{model.Delete, model.Modify, "src/a/**", "DELETE_CONFLICT", model.Block},
	}
	for _, tc := range cases {
		got := codes(Detect(tc.mine, "src/a/new.ts", ctx(me, nil, other(tc.theirsKind, tc.theirs), nil)))
		if got[tc.want] != tc.sev {
			t.Errorf("%s vs %s %s: want %s/%s, got %+v", tc.mine, tc.theirsKind, tc.theirs, tc.want, tc.sev, got)
		}
	}
	// my own intents never conflict with me
	mine := []model.Intent{{AgentID: "me@x", TaskID: "T1", Kind: model.Modify, Target: "src/**"}}
	if c := Detect(model.Modify, "src/a.ts", ctx(me, nil, mine, nil)); len(c) != 0 {
		t.Fatalf("self conflict: %+v", c)
	}
}

func TestContractAndDependencyIntents(t *testing.T) {
	me := tk("T1", nil, nil)
	owner := tk("T2", nil, nil)
	c := codes(Detect(model.ChangeContract, "C", ctx(me, []model.Task{owner}, nil, nil)))
	if c["CONTRACT_NOT_OWNED"] != model.Block || c["ACTIVE_CONSUMER"] != model.Warn {
		t.Fatalf("not owner + consumer: %+v", c)
	}
	mineC := tk("T2", nil, nil)
	got := Detect(model.ChangeContract, "C", ctx(mineC, nil, []model.Intent{{AgentID: "other@y", TaskID: "T7", Kind: model.ChangeContract, Target: "C"}}, nil))
	if codes(got)["CONTRACT_COLLISION"] != model.Block {
		t.Fatalf("collision: %+v", got)
	}
	// only the *other* consumer is reported, not me
	n := 0
	for _, x := range got {
		if x.Code == "ACTIVE_CONSUMER" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("consumers reported %d times", n)
	}
	if c := codes(Detect(model.ChangeContract, "Nope", ctx(me, nil, nil, nil))); c["UNKNOWN_CONTRACT"] != model.Block {
		t.Fatalf("unknown contract: %+v", c)
	}
	if c := codes(Detect(model.DependOn, "T404", ctx(me, nil, nil, nil))); c["UNKNOWN_TASK"] != model.Block {
		t.Fatalf("unknown task: %+v", c)
	}
	st := map[string]model.TaskState{"T2": {Status: model.Done}}
	if c := Detect(model.DependOn, "T2", ctx(me, []model.Task{owner}, nil, st)); len(c) != 0 {
		t.Fatalf("done dependency is fine: %+v", c)
	}
	if c := codes(Detect(model.DependOn, "T2", ctx(me, []model.Task{owner}, nil, nil))); c["UNMET_DEPENDENCY"] != model.Warn {
		t.Fatalf("unmet dependency: %+v", c)
	}
}
