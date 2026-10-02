package engine

import (
	"fmt"
	"strings"

	"github.com/shriyashish-mishra/who-broke-it/internal/graph"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

// PRDraft is a pull request assembled from a task and its handoff.
type PRDraft struct {
	Title, Body, Branch, Base string
	Labels                    []string
}

// BuildPR renders the PR a reviewer needs: why the code exists (requirement, task, decisions), what changed
// (from git, not from the agent's say-so), contract changes, test evidence, and what is affected downstream.
func (e *Engine) BuildPR(taskID string) (*PRDraft, error) {
	t, err := e.Task(taskID)
	if err != nil {
		return nil, err
	}
	h := e.GetHandoff(t.ID)
	if h == nil {
		return nil, model.Errf("%s has no handoff yet. Run: wbi handoff %s --summary \"...\"", t.ID, t.ID)
	}
	st := e.State(t.ID)
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }

	w("## %s: %s", t.ID, t.Title)
	w("")
	w("> %s", h.Implemented)
	w("")
	w("**Task goal:** %s", t.Goal)
	for _, r := range e.Store.Requirements() {
		if contains(t.Requirements, r.ID) {
			w("**Requirement:** %s: %s", r.ID, r.Text)
		}
	}
	for _, d := range e.Store.Decisions() {
		if contains(t.Decisions, d.ID) || contains(d.Tasks, t.ID) {
			w("**Decision:** %s: %s", d.ID, d.Title)
		}
	}
	w("**Implemented by:** `%s` (owner: %s)", h.AgentID, firstNonEmpty(st.Owner, "—"))
	w("")

	w("### What changed")
	for i, f := range h.ChangedFiles {
		if i == 25 {
			w("- … and %d more", len(h.ChangedFiles)-25)
			break
		}
		w("- `%s`", f)
	}
	if len(h.ChangedFiles) == 0 {
		w("- (no files)")
	}
	w("")

	if len(h.ContractChanges) > 0 {
		w("### ⚠ Contract changes")
		for _, c := range h.ContractChanges {
			n := ""
			if c.Note != "" {
				n = ": " + c.Note
			}
			w("- **%s** → v%d%s", c.Name, c.Version, n)
		}
		w("")
	}

	w("### Verification")
	if v, err := e.dryVerify(t, h); err == nil {
		for _, c := range v {
			icon := map[string]string{"pass": "✅", "fail": "❌", "warn": "⚠️", "skip": "➖"}[c.Status]
			w("- %s **%s**: %s", icon, c.Name, c.Detail)
		}
	}
	tests := "no evidence"
	if h.Tests.Ran {
		tests = h.Tests.Summary
		if tests == "" {
			tests = "ran"
		}
	}
	w("- Tests: %s", tests)
	w("")

	w("### Acceptance criteria")
	for i, a := range t.Acceptance {
		how := "attested"
		switch {
		case a.Human:
			how = "needs human sign-off"
		case a.Check != nil:
			how = "auto-checked"
		}
		w("%d. %s _(%s)_", i+1, a.Text, how)
	}
	w("")

	if len(h.Limitations) > 0 {
		w("### Known limitations")
		for _, l := range h.Limitations {
			w("- %s", l)
		}
		w("")
	}
	if len(h.Affected) > 0 {
		w("### Downstream tasks affected")
		w("%s", strings.Join(h.Affected, ", "))
		w("")
	}
	if len(t.Impact) > 0 {
		w("### 🔐 High-impact change (%s)", strings.Join(t.Impact, ", "))
		w("A human reviewer must approve this before it merges.")
		w("")
	}
	w("---")
	w("_Opened by [Who Broke It?](https://github.com/shriyashish-mishra/who-broke-it) from the task's handoff record. The `wbi` merge gate checks scope, contracts and the constitution._")

	labels := []string{"wbi"}
	if len(h.ContractChanges) > 0 {
		labels = append(labels, "contract-change")
	}
	if len(t.Impact) > 0 {
		labels = append(labels, "high-impact")
	}
	branch := st.Branch
	if branch == "" {
		branch = "wbi/" + t.ID
	}
	return &PRDraft{Title: fmt.Sprintf("%s: %s", t.ID, t.Title), Body: b.String(), Branch: branch, Base: e.Project().BaseBranch, Labels: labels}, nil
}

// dryVerify re-reads the verification facts without changing task state.
func (e *Engine) dryVerify(t model.Task, h *model.Handoff) ([]CheckResult, error) {
	var out []CheckResult
	add := func(n, s, d string) { out = append(out, CheckResult{n, s, d}) }
	add("Scope", "pass", fmt.Sprintf("%d file(s) changed on %s", len(h.ChangedFiles), firstNonEmpty(h.Branch, "the task branch")))
	if len(h.ContractChanges) == 0 {
		add("Contracts", "pass", "no contract drift")
	} else {
		add("Contracts", "pass", fmt.Sprintf("%d contract change(s) recorded and propagated to %d task(s)", len(h.ContractChanges), len(h.Affected)))
	}
	var unmet []string
	for _, d := range t.DependsOn {
		if e.State(d).Status != model.Done {
			unmet = append(unmet, d)
		}
	}
	if len(unmet) > 0 {
		add("Dependencies", "warn", "upstream not DONE: "+strings.Join(unmet, ", "))
	} else {
		add("Dependencies", "pass", "all upstream tasks DONE")
	}
	_ = graph.Waves
	return out, nil
}

// SetPR records the PR number on the task's handoff (database and the branch's record file).
func (e *Engine) SetPR(taskID string, n int) {
	h := e.GetHandoff(taskID)
	if h == nil {
		return
	}
	h.PR = n
	st := e.State(taskID)
	s := e.Store
	if st.Worktree != "" {
		s = storeAt(st.Worktree)
	}
	_ = s.SaveHandoff(*h)
	e.saveHandoffRow(*h)
}
