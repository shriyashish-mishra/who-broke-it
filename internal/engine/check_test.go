package engine_test

import (
	"strings"
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
	"github.com/shriyashish-mishra/who-broke-it/internal/testutil"
)

func codesOf(r *engine.CheckReport, level string) map[string]bool {
	m := map[string]bool{}
	for _, f := range r.Findings {
		if f.Level == level {
			m[f.Code] = true
		}
	}
	return m
}

// worktreeFor starts a task and returns its workdir; the branch is wbi/<id>, exactly what a PR would be.
func worktreeFor(t *testing.T, e *engine.Engine, id string, who engine.AgentOpts) string {
	t.Helper()
	r, err := e.Start(id, who, true, true)
	if err != nil {
		t.Fatal(err)
	}
	return r.Workdir
}

func check(t *testing.T, e *engine.Engine, o engine.CheckOpts) *engine.CheckReport {
	t.Helper()
	o.Base = "main"
	rep, err := e.Check(o)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestCheckPassesACleanTaskPR(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	wd := worktreeFor(t, e, "TASK-001", claude)
	testutil.Put(t, wd, "docs/notes.md", "ok")
	testutil.CommitAll(t, wd, "docs")
	rep := check(t, e, engine.CheckOpts{Head: "wbi/TASK-001"})
	if !rep.OK || rep.Task != "TASK-001" {
		t.Fatalf("clean PR must pass: %+v", rep)
	}
}

func TestCheckBlocksScopeAndConstitutionViolations(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	through(t, e, "TASK-001", "TASK-002", "TASK-004", "TASK-007")
	wd := worktreeFor(t, e, "TASK-010", codex)
	testutil.Put(t, wd, "src/api/analytics/summary.ts", "export const f = (db) => db.billing.update({})") // C6
	testutil.Put(t, wd, "src/api/billing/hack.ts", "x")                                                   // outside scope
	testutil.CommitAll(t, wd, "oops")
	rep := check(t, e, engine.CheckOpts{Head: "wbi/TASK-010"})
	errs := codesOf(rep, "error")
	if rep.OK || !errs["CONSTITUTION"] || !errs["SCOPE"] {
		t.Fatalf("want CONSTITUTION and SCOPE errors, got %+v", rep.Findings)
	}
}

func TestCheckContractRules(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	through(t, e, "TASK-001", "TASK-002", "TASK-004")

	// 1. a proper contract change through the real handoff flow passes
	good := finishWith(t, e, "TASK-007", claude, "src/api/billing/status.ts", "export type S = 'paused'", "BillingStatus=now supports paused")
	if rep := check(t, e, engine.CheckOpts{Head: good}); !rep.OK {
		t.Fatalf("handoff-backed contract bump must pass: %+v", rep.Findings)
	}

	// 2. bumping a contract by hand, with no handoff record, is caught
	wd := worktreeFor(t, e, "TASK-010", codex)
	editContract(t, wd, "AnalyticsEvent", func(c map[string]any) { c["version"] = 2.0 })
	testutil.CommitAll(t, wd, "sneaky bump")
	rep := check(t, e, engine.CheckOpts{Head: "wbi/TASK-010"})
	if !codesOf(rep, "error")["CONTRACT_NO_HANDOFF"] {
		t.Fatalf("want CONTRACT_NO_HANDOFF: %+v", rep.Findings)
	}

	// 3. changing someone else's contract is caught even with a bump
	wd = worktreeFor(t, e, "TASK-013", codex)
	editContract(t, wd, "BillingStatus", func(c map[string]any) { c["version"] = 9.0; c["shape"] = "'x'" })
	testutil.CommitAll(t, wd, "steal")
	if rep := check(t, e, engine.CheckOpts{Head: "wbi/TASK-013"}); !codesOf(rep, "error")["CONTRACT_NOT_OWNED"] {
		t.Fatalf("want CONTRACT_NOT_OWNED: %+v", rep.Findings)
	}

	// 4. changing a shape without bumping the version is caught
	wd = worktreeFor(t, e, "TASK-011", cursor)
	editContract(t, wd, "AnalyticsEvent", func(c map[string]any) { c["shape"] = "{ different: true }" })
	testutil.CommitAll(t, wd, "silent shape change")
	if rep := check(t, e, engine.CheckOpts{Head: "wbi/TASK-011"}); !codesOf(rep, "error")["CONTRACT_UNVERSIONED"] {
		t.Fatalf("want CONTRACT_UNVERSIONED: %+v", rep.Findings)
	}
}

func TestCheckTaskLinkageHandoffAndApprovalGates(t *testing.T) {
	dir, e := testutil.PlannedRepo(t)
	// a branch with no task at all
	testutil.Git(t, dir, "checkout", "-q", "-b", "feature/misc")
	testutil.Put(t, dir, "x.txt", "x")
	testutil.CommitAll(t, dir, "misc")
	testutil.Git(t, dir, "checkout", "-q", "main")
	rep := check(t, e, engine.CheckOpts{Head: "feature/misc", Branch: "feature/misc"})
	if !rep.OK || !hasCode(rep, "NO_TASK") {
		t.Fatalf("an unlinked PR warns by default: %+v", rep.Findings)
	}
	if rep := check(t, e, engine.CheckOpts{Head: "feature/misc", Branch: "feature/misc", RequireTask: true}); rep.OK {
		t.Fatal("--require-task must fail an unlinked PR")
	}

	// a high-impact task (migration) without/with approval, and the handoff requirement
	wd := worktreeFor(t, e, "TASK-002", codex)
	testutil.Put(t, wd, "migrations/001.sql", "create table t();")
	testutil.CommitAll(t, wd, "sql\n\nWBI-Task: TASK-002")
	strict := engine.CheckOpts{Head: "wbi/TASK-002", RequireApproval: true, RequireHandoff: true}
	rep = check(t, e, strict)
	if rep.OK || !codesOf(rep, "error")["NEEDS_APPROVAL"] || !codesOf(rep, "error")["NO_HANDOFF"] {
		t.Fatalf("want NEEDS_APPROVAL + NO_HANDOFF: %+v", rep.Findings)
	}
	strict.Approved = true
	rep = check(t, e, engine.CheckOpts{Head: "wbi/TASK-002", Approved: true, RequireApproval: true})
	if codesOf(rep, "error")["NEEDS_APPROVAL"] {
		t.Fatalf("approval must satisfy the gate: %+v", rep.Findings)
	}
}

func TestCheckFindsTheTaskFromCommitTrailers(t *testing.T) {
	dir, e := testutil.PlannedRepo(t)
	testutil.Git(t, dir, "checkout", "-q", "-b", "some-random-branch")
	testutil.Put(t, dir, "docs/a.md", "x")
	testutil.CommitAll(t, dir, "docs\n\nWBI-Task: TASK-001")
	testutil.Git(t, dir, "checkout", "-q", "main")
	rep := check(t, e, engine.CheckOpts{Head: "some-random-branch", Branch: "some-random-branch"})
	if rep.Task != "TASK-001" {
		t.Fatalf("trailer must identify the task, got %q", rep.Task)
	}
}

func hasCode(r *engine.CheckReport, code string) bool {
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

// finishWith runs a task through the real start → commit → handoff flow, with a contract change.
func finishWith(t *testing.T, e *engine.Engine, id string, who engine.AgentOpts, file, body, contract string) string {
	t.Helper()
	r, err := e.Start(id, who, true, true)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Put(t, r.Workdir, file, body)
	testutil.CommitAll(t, r.Workdir, "feat\n\nWBI-Task: "+id)
	if _, err := e.SubmitHandoff(id, engine.HandoffOpts{AgentOpts: who, Summary: "did it", TestsPassed: ptr(true), Contracts: []string{contract}}); err != nil {
		t.Fatal(err)
	}
	return "wbi/" + id
}

var _ = strings.TrimSpace

func editContract(t *testing.T, workdir, name string, fn func(map[string]any)) {
	t.Helper()
	testutil.EditJSON(t, workdir+"/.wbi/contracts/"+name+".json", fn)
}

// A real import crosses task areas with no declared dependency: blast radius, drift and the merge gate all see it.
func TestUndeclaredCouplingIsSeenEverywhere(t *testing.T) {
	dir, e := testutil.PlannedRepo(t)
	testutil.Put(t, dir, "src/api/billing/status.ts", "export type BillingStatus = 'active'\n")
	testutil.Put(t, dir, "src/web/auth/login.tsx", "import { BillingStatus } from '../../api/billing/status'\nexport const L = (s: BillingStatus) => s\n")
	testutil.CommitAll(t, dir, "code")

	// 1. blast radius of the billing file includes the auth UI, found by import scan only (TASK-005 never declared it)
	b := e.Blast("src/api/billing/status.ts")
	found := false
	for _, id := range b.DirectTasks {
		if id == "TASK-005" && strings.Contains(b.Why[id], "imports") {
			found = true
		}
	}
	if !found || len(b.CodeDependents) != 1 || b.CodeDependents[0] != "src/web/auth/login.tsx" {
		t.Fatalf("code-level dependents missing: direct=%v why=%v files=%v", b.DirectTasks, b.Why, b.CodeDependents)
	}

	// 2. drift lists the pair
	d := e.Drift()
	if len(d.Coupling) != 1 || d.Coupling[0].From != "TASK-005" || d.Coupling[0].To != "TASK-007" {
		t.Fatalf("coupling: %+v", d.Coupling)
	}

	// 3. a PR for TASK-005 that touches the importing file is warned
	wd := worktreeFor(t, e, "TASK-005", claude)
	testutil.Put(t, wd, "src/web/auth/login.tsx", "import { BillingStatus } from '../../api/billing/status'\nexport const L = (s: BillingStatus) => s + '!'\n")
	testutil.Put(t, wd, "src/api/billing/status.ts", "export type BillingStatus = 'active'\n")
	testutil.CommitAll(t, wd, "ui\n\nWBI-Task: TASK-005")
	rep := check(t, e, engine.CheckOpts{Head: "wbi/TASK-005"})
	if !hasCode(rep, "UNDECLARED_COUPLING") {
		t.Fatalf("merge gate must warn: %+v", rep.Findings)
	}

	// 4. declaring the dependency removes the warning and the drift entry
	t5, _ := e.Task("TASK-005")
	t5.DependsOn = append(t5.DependsOn, "TASK-007")
	if err := e.Store.SaveTask(t5); err != nil {
		t.Fatal(err)
	}
	if got := e.UndeclaredCoupling(nil); len(got) != 0 {
		t.Fatalf("declared dependency must silence it: %+v", got)
	}
}
