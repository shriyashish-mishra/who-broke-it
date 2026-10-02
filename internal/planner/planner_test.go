package planner

import (
	"strings"
	"testing"

	"wbi/internal/glob"
	"wbi/internal/graph"
	"wbi/internal/model"
)

func TestHeuristicPlanIsValidDAGWithDisjointParallelScopes(t *testing.T) {
	facts := RepoFacts{Name: "x", TestCmd: "npm test", Roots: Roots{API: "src/api", Web: "src/web", DB: "src/db", Migrations: "migrations", Tests: "tests"}}
	plan := HeuristicPlan("Build a multi-tenant SaaS dashboard with authentication, billing, analytics and an AI assistant", facts)
	for _, i := range graph.Validate(plan.Tasks, plan.Contracts) {
		if i.Severity == "error" {
			t.Fatalf("invalid plan: %s", i.Message)
		}
	}
	if len(plan.Tasks) < 14 {
		t.Fatalf("expected >=14 tasks, got %d", len(plan.Tasks))
	}
	for i, a := range plan.Tasks {
		for _, b := range plan.Tasks[i+1:] {
			if !graph.Concurrent(plan.Tasks, a.ID, b.ID) {
				continue
			}
			for _, pa := range a.AllowedPaths {
				for _, pb := range b.AllowedPaths {
					if glob.Overlap(pa, pb) {
						t.Errorf("%s and %s overlap on %s / %s", a.ID, b.ID, pa, pb)
					}
				}
			}
		}
	}
	for _, task := range plan.Tasks {
		if task.Layer == "database" && task.Impact[0] != model.ImpactMigration {
			t.Error("database task must be marked as a migration")
		}
	}
}

func TestUnknownGoalStillPlans(t *testing.T) {
	plan := HeuristicPlan("Build a recipe sharing site", RepoFacts{Roots: Roots{API: "src/api", Web: "src/web", DB: "src/db", Migrations: "migrations", Tests: "tests"}})
	if err := AssertValid(plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Tasks) < 5 {
		t.Fatalf("got %d tasks", len(plan.Tasks))
	}
}

func TestParsePlanAcceptsSparseJSONAndRejectsCycles(t *testing.T) {
	plan, err := ParsePlan([]byte(`{"tasks":[{"id":"TASK-001","title":"a"},{"id":"TASK-002","title":"b","dependsOn":["TASK-001"],"acceptance":["works"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := AssertValid(plan); err != nil {
		t.Fatal(err)
	}
	if plan.Tasks[1].Acceptance[0].Text != "works" {
		t.Fatalf("string criterion not accepted: %+v", plan.Tasks[1].Acceptance)
	}
	cyc, _ := ParsePlan([]byte(`{"tasks":[{"id":"A","dependsOn":["B"]},{"id":"B","dependsOn":["A"]}]}`))
	if err := AssertValid(cyc); err == nil || !strings.Contains(strings.ToLower(err.Error()), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
	if _, err := ParsePlan([]byte(`{}`)); err == nil {
		t.Fatal("empty plan must be rejected")
	}
}
