package engine_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
	"github.com/shriyashish-mishra/who-broke-it/internal/planner"
	"github.com/shriyashish-mishra/who-broke-it/internal/testutil"
)

// repoWithPlan creates a real git repo, inits wbi, and installs a hand-written plan.
func repoWithPlan(t *testing.T, root, name, planJSON string) *engine.Engine {
	t.Helper()
	dir := testutil.MakeRepoAt(t, filepath.Join(root, name))
	if _, err := engine.Init(dir, engine.InitOpts{Name: name, NoHook: true}); err != nil {
		t.Fatal(err)
	}
	pl, err := planner.ParsePlan([]byte(planJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err := planner.AssertValid(pl); err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	if err := e.Store.WritePlan(pl); err != nil {
		t.Fatal(err)
	}
	testutil.CommitAll(t, dir, "plan")
	return e
}

func TestCrossRepoContractsPropagateAcrossTheChain(t *testing.T) {
	root := t.TempDir()
	backend := repoWithPlan(t, root, "backend", `{"tasks":[{"id":"TASK-001","title":"Payments API","layer":"backend","allowedPaths":["src/**"],"provides":["PaymentStatus"],"impact":["public-api"]}],
		"contracts":[{"name":"PaymentStatus","kind":"type","providedBy":"TASK-001","shape":"'paid'|'due'","public":true}]}`)
	sdk := repoWithPlan(t, root, "sdk", `{"tasks":[{"id":"TASK-001","title":"SDK payment types","layer":"backend","allowedPaths":["lib/**"],"consumes":["backend:PaymentStatus"],"provides":["SdkPayment"]}],
		"contracts":[{"name":"SdkPayment","kind":"type","providedBy":"TASK-001","shape":"{status:string}"}]}`)
	web := repoWithPlan(t, root, "web", `{"tasks":[
		{"id":"TASK-001","title":"Checkout page","layer":"frontend","allowedPaths":["app/**"],"consumes":["sdk:SdkPayment"]},
		{"id":"TASK-002","title":"Checkout e2e","layer":"testing","allowedPaths":["e2e/**"],"dependsOn":["TASK-001"]}]}`)

	// linking validates that the other repo really has a wbi graph, and rejects nonsense
	if err := sdk.LinkAdd("backend", "../backend", "backend"); err != nil {
		t.Fatal(err)
	}
	if err := web.LinkAdd("sdk", "../sdk", "sdk"); err != nil {
		t.Fatal(err)
	}
	if err := sdk.LinkAdd("nope", "../does-not-exist", ""); err == nil {
		t.Fatal("linking to a path without a wbi graph must fail")
	}
	if err := sdk.LinkAdd("bad:name", "../backend", ""); err == nil {
		t.Fatal("link names must not contain ':'")
	}
	if err := backend.LinkAdd("sdk", "../sdk", "sdk"); err != nil { // the provider links to its consumers to see impact
		t.Fatal(err)
	}

	// nothing is stale yet; the external reference resolves
	ct, err := sdk.ExternalContract("backend:PaymentStatus")
	if err != nil || ct.Version != 1 {
		t.Fatalf("external contract: %+v %v", ct, err)
	}
	for _, u := range sdk.ExternalUses() {
		if u.Stale || u.Problem != "" {
			t.Fatalf("fresh link must be current: %+v", u)
		}
	}
	if _, err := sdk.ExternalContract("backend:Missing"); err == nil {
		t.Fatal("unknown external contract must error")
	}
	if _, err := sdk.ExternalContract("ghost:PaymentStatus"); err == nil {
		t.Fatal("unlinked repo must error")
	}

	// blast radius in the consumer repo for a contract that lives elsewhere
	b := sdk.Blast("backend:PaymentStatus")
	if b.Kind != "external-contract" || len(b.DirectTasks) != 1 || b.DirectTasks[0] != "TASK-001" || b.Risk == "" {
		t.Fatalf("sdk blast: %+v", b)
	}
	if wb := web.Blast("sdk:SdkPayment"); len(wb.DirectTasks) != 1 || len(wb.IndirectTasks) != 1 || wb.IndirectTasks[0] != "TASK-002" {
		t.Fatalf("web blast should reach its own dependents: %+v", wb)
	}

	// provider-side impact: who consumes my contract in linked repos?
	imp, err := backend.ImpactOnLinks("PaymentStatus")
	if err != nil || len(imp) != 1 || imp[0].Repo != "sdk" || strings.Join(imp[0].Tasks, ",") != "TASK-001" {
		t.Fatalf("impact: %+v %v", imp, err)
	}

	// backend changes the contract (real handoff flow) and commits it
	r, err := backend.Start("TASK-001", claude, true, true)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Put(t, r.Workdir, "src/pay.ts", "export type S = 'paid'|'due'|'paused'")
	testutil.CommitAll(t, r.Workdir, "feat\n\nWBI-Task: TASK-001")
	if _, err := backend.SubmitHandoff("TASK-001", engine.HandoffOpts{AgentOpts: claude, Summary: "paused", TestsPassed: ptr(true), Contracts: []string{"PaymentStatus=adds paused"}}); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, backend.Root(), "merge", "-q", "--no-edit", "wbi/TASK-001") // the change lands on the backend's main

	// the SDK repo now sees drift: banner, status attention, ack
	t1, _ := sdk.Task("TASK-001")
	st := sdk.StaleContracts(t1)
	if len(st) != 1 || st[0].Contract != "backend:PaymentStatus" || st[0].From != 1 || st[0].To != 2 || !strings.Contains(st[0].Note, "paused") {
		t.Fatalf("sdk staleness: %+v", st)
	}
	ctx, _ := sdk.Context("TASK-001")
	if !strings.Contains(ctx, "backend:PaymentStatus") || !strings.Contains(ctx, "v1 → v2") {
		t.Fatalf("sdk work packet needs the cross-repo banner:\n%s", ctx)
	}
	if !strings.Contains(strings.Join(sdk.Status().Attention, "\n"), "backend:PaymentStatus changed") {
		t.Fatalf("status attention: %v", sdk.Status().Attention)
	}
	if imp, _ := backend.ImpactOnLinks("PaymentStatus"); len(imp[0].Stale) != 1 {
		t.Fatalf("provider must see the consumer has not caught up: %+v", imp)
	}
	// the web repo is NOT affected yet: impact is one hop per repo until the SDK republishes its contract
	if w1, _ := web.Task("TASK-001"); len(web.StaleContracts(w1)) != 0 {
		t.Fatal("web only consumes the SDK, which has not changed")
	}
	if err := sdk.Ack("TASK-001"); err != nil {
		t.Fatal(err)
	}
	if len(sdk.StaleContracts(t1)) != 0 {
		t.Fatal("ack must clear cross-repo staleness")
	}
	if uses := sdk.ExternalUses(); len(uses) != 1 || uses[0].Stale || uses[0].Acked != 2 {
		t.Fatalf("uses after ack: %+v", uses)
	}
	var _ = model.Todo
}

func TestBrokenLinksAreReportedNotFatal(t *testing.T) {
	root := t.TempDir()
	a := repoWithPlan(t, root, "a", `{"tasks":[{"id":"TASK-001","title":"x","consumes":["b:Thing"]}]}`)
	if errs := a.ExternalUses(); len(errs) != 1 || errs[0].Problem == "" {
		t.Fatalf("an unlinked reference must be reported: %+v", errs)
	}
	if got := a.Status(); got.Progress.Total != 1 {
		t.Fatal("status must still work with a broken link")
	}
}
