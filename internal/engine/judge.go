package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"wbi/internal/gitx"
	"wbi/internal/glob"
	"wbi/internal/graph"
	"wbi/internal/model"
	"wbi/internal/rules"
	"wbi/internal/store"
)

type HandoffOpts struct {
	AgentOpts
	Summary string
	// RunTests is a shell command run in the task's workdir; its exit code becomes the test evidence.
	RunTests     string
	TestsPassed  *bool
	TestsSummary string
	// Contracts are "Name" or "Name=note".
	Contracts   []string
	Limitations []string
	Attest      []int
	PR          int
}

type CheckResult struct{ Name, Status, Detail string } // Status: pass | fail | warn | skip

type Verification struct {
	TaskID        string
	Checks        []CheckResult
	Met, Total    int
	NeedsApproval bool
	Verdict       string // DONE | READY_FOR_REVIEW | FAILED
}

type HandoffResult struct {
	Handoff      model.Handoff
	Verification Verification
}

// sh runs a shell command and returns success plus the last lines of output.
func sh(cmd, cwd string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Dir = cwd
	out, err := c.CombinedOutput()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	tail := strings.Join(lines, " | ")
	if err != nil && tail == "" {
		tail = err.Error()
	}
	return err == nil, tail
}

func isWbi(f string) bool { return strings.HasPrefix(f, ".wbi/") }

// SubmitHandoff records completed work: changed files (from git), test evidence, contract changes
// (propagated to affected tasks), then runs verification. "Done" is decided by the verifier.
func (e *Engine) SubmitHandoff(taskID string, o HandoffOpts) (*HandoffResult, error) {
	t, err := e.Task(taskID)
	if err != nil {
		return nil, err
	}
	st := e.State(t.ID)
	ao := o.AgentOpts
	if ao.Agent == "" && st.AgentID != "" {
		ao.Agent = strings.SplitN(st.AgentID, "@", 2)[0]
	}
	agent, err := e.ResolveAgent(ao)
	if err != nil {
		return nil, err
	}
	if st.Status == model.Todo || st.Status == model.Done {
		return nil, model.Errf("%s is %s; claim it (wbi claim %s) before handing off.", t.ID, st.Status, t.ID)
	}
	workdir := st.Worktree
	if workdir == "" {
		workdir = e.Root()
	}
	branch := "HEAD"
	if st.Branch != "" && gitx.RefExists(e.Root(), st.Branch) {
		branch = st.Branch
	}
	base := e.Project().BaseBranch
	// Records are written into the task's own checkout so the contract bump and handoff travel with the PR.
	wstore := e.Store
	if workdir != e.Root() {
		wstore = &store.Store{Root: workdir}
	}

	var changed []string
	for _, f := range gitx.ChangedFiles(e.Root(), base, branch) {
		if !strings.HasPrefix(f, ".wbi/state/") && !strings.HasPrefix(f, ".wbi/handoffs/") {
			changed = append(changed, f)
		}
	}
	var commits []string
	for _, c := range gitx.CommitsAhead(e.Root(), base, branch) {
		commits = append(commits, c.SHA+" "+c.Subject)
	}

	tests := model.TestEvidence{}
	if o.RunTests != "" {
		ok, tail := sh(o.RunTests, workdir)
		summary := o.TestsSummary
		if summary == "" {
			summary = tail
		}
		tests = model.TestEvidence{Ran: true, Passed: &ok, Command: o.RunTests, Summary: summary}
	} else if o.TestsPassed != nil {
		tests = model.TestEvidence{Ran: true, Passed: o.TestsPassed, Summary: o.TestsSummary}
	}

	// Contract changes: bump versions, record live state, propagate to affected tasks.
	var changes []model.ContractChange
	known := e.Contracts()
	for _, spec := range o.Contracts {
		name, note := spec, ""
		if i := strings.Index(spec, "="); i >= 0 {
			name, note = spec[:i], spec[i+1:]
		}
		name, note = strings.TrimSpace(name), strings.TrimSpace(note)
		var c *model.Contract
		for i := range known {
			if known[i].Name == name {
				c = &known[i]
			}
		}
		if c == nil {
			var names []string
			for _, k := range known {
				names = append(names, k.Name)
			}
			return nil, model.Errf("Unknown contract %q. Known: %s", name, strings.Join(names, ", "))
		}
		version := e.EffectiveVersion(*c) + 1
		c.Version = version
		c.History = append(c.History, model.HistoryEntry{Version: version, TaskID: t.ID, At: time.Now().UTC().Format(time.RFC3339), Note: note})
		if err := wstore.SaveContract(*c); err != nil {
			return nil, err
		}
		_, _ = e.DB.Exec(`INSERT OR REPLACE INTO contract_state (name,version,task_id,ts,note) VALUES (?,?,?,?,?)`, name, version, t.ID, nowMs(), note)
		changes = append(changes, model.ContractChange{Name: name, Note: note, Version: version})
	}

	affected := map[string]bool{}
	for _, d := range graph.Dependents(e.Tasks(), t.ID, false) {
		affected[d] = true
	}
	for _, cc := range changes {
		b := e.Blast(cc.Name)
		states := e.States()
		all := append(append([]string{}, b.DirectTasks...), b.IndirectTasks...)
		for _, id := range all {
			if id == t.ID {
				continue
			}
			affected[id] = true
			why := b.Why[id]
			if why == "" {
				why = "is affected"
			}
			note := ""
			if cc.Note != "" {
				note = ": " + cc.Note
			}
			e.Notify(id, states[id].AgentID, fmt.Sprintf("%s changed to v%d by %s%s. This task %s.", cc.Name, cc.Version, t.ID, note, why), "contract:"+cc.Name)
		}
		e.Event("contract_changed", t.ID, agent.ID, map[string]any{"contract": cc.Name, "version": cc.Version, "affected": all, "risk": b.Risk})
	}
	aff := make([]string, 0, len(affected))
	for id := range affected {
		aff = append(aff, id)
	}
	sort.Strings(aff)

	implemented := o.Summary
	if implemented == "" {
		implemented = t.Title
	}
	h := model.Handoff{
		TaskID: t.ID, AgentID: agent.ID, At: time.Now().UTC().Format(time.RFC3339), Implemented: implemented, ChangedFiles: changed, ContractChanges: changes,
		Tests: tests, Limitations: o.Limitations, Attested: o.Attest, Affected: aff, Branch: st.Branch, PR: o.PR, Commits: commits,
	}
	h.Normalize()
	js, _ := json.Marshal(h)
	_, _ = e.DB.Exec(`INSERT OR REPLACE INTO handoffs (task_id,json,ts) VALUES (?,?,?)`, t.ID, string(js), nowMs())
	if err := wstore.SaveHandoff(h); err != nil {
		return nil, err
	}
	commitRecords(workdir, st.Branch, t.ID)
	for _, d := range graph.Dependents(e.Tasks(), t.ID, false) {
		e.Notify(d, e.State(d).AgentID, fmt.Sprintf("%s handed off: %s", t.ID, h.Implemented), "handoff")
	}
	e.SetState(t.ID, func(s *model.TaskState) { s.Status = model.Review })
	e.ReleaseIntents(t.ID)
	e.Event("handoff", t.ID, agent.ID, map[string]int{"files": len(changed)})

	v, err := e.VerifyTask(t.ID)
	if err != nil {
		return nil, err
	}
	return &HandoffResult{h, *v}, nil
}

// commitRecords commits `.wbi/contracts` + `.wbi/handoffs` on the task branch (only when the workdir is on it).
func commitRecords(workdir, branch, taskID string) {
	if branch == "" || gitx.CurrentBranch(workdir) != branch {
		return
	}
	if _, err := gitx.Git(workdir, "add", "--", ".wbi/contracts", ".wbi/handoffs"); err != nil {
		return
	}
	if _, ok := gitx.Try(workdir, "diff", "--cached", "--quiet"); ok {
		return // nothing staged
	}
	// A missing git identity just leaves the records in the working tree for the agent to commit.
	_, _ = gitx.Git(workdir, "commit", "-q", "-m", fmt.Sprintf("chore(wbi): handoff %s\n\nWBI-Task: %s", taskID, taskID), "--", ".wbi/contracts", ".wbi/handoffs")
}

// VerifyTask is the Agent Judge: it checks a "done" claim against scope, contracts, constitution, tests
// and acceptance criteria.
func (e *Engine) VerifyTask(taskID string) (*Verification, error) {
	t, err := e.Task(taskID)
	if err != nil {
		return nil, err
	}
	h := e.GetHandoff(t.ID)
	if h == nil {
		return nil, model.Errf("%s has no handoff yet. Run: wbi handoff %s", t.ID, t.ID)
	}
	st := e.State(t.ID)
	workdir := st.Worktree
	if workdir == "" {
		workdir = e.Root()
	}
	var checks []CheckResult
	add := func(name, status, detail string) { checks = append(checks, CheckResult{name, status, detail}) }

	var code []string
	for _, f := range h.ChangedFiles {
		if !isWbi(f) {
			code = append(code, f)
		}
	}
	switch {
	case len(code) > 0:
		add("Implementation", "pass", fmt.Sprintf("%d file(s) changed", len(code)))
	case t.Layer == "architecture":
		add("Implementation", "pass", "planning task")
	default:
		add("Implementation", "fail", "no code changes found on the task branch")
	}

	allowed := append(append([]string{}, t.AllowedPaths...), ".wbi/**")
	var restricted, outside []string
	for _, f := range code {
		switch {
		case glob.MatchesAny(t.RestrictedPaths, f):
			restricted = append(restricted, f)
		case !glob.MatchesAny(allowed, f):
			outside = append(outside, f)
		}
	}
	switch {
	case len(restricted) > 0:
		add("Scope", "fail", "touched restricted path(s): "+strings.Join(first(restricted, 4), ", "))
	case len(outside) > 0:
		add("Scope", "fail", "outside allowed paths: "+strings.Join(first(outside, 4), ", "))
	default:
		add("Scope", "pass", "all changes inside allowed paths")
	}

	var foreign []string
	for _, c := range h.ContractChanges {
		if !contains(t.Provides, c.Name) {
			foreign = append(foreign, c.Name)
		}
	}
	stale := e.StaleContracts(t)
	switch {
	case len(foreign) > 0:
		add("Contracts", "fail", "changed contract(s) owned by another task: "+strings.Join(foreign, ", "))
	case len(stale) > 0:
		var ss []string
		for _, s := range stale {
			ss = append(ss, fmt.Sprintf("%s v%d→v%d", s.Contract, s.From, s.To))
		}
		add("Contracts", "warn", fmt.Sprintf("built against stale contract(s): %s; run wbi ack %s", strings.Join(ss, ", "), t.ID))
	case len(h.ContractChanges) > 0:
		var cs []string
		for _, c := range h.ContractChanges {
			cs = append(cs, fmt.Sprintf("%s → v%d", c.Name, c.Version))
		}
		add("Contracts", "pass", strings.Join(cs, ", ")+" recorded and propagated")
	default:
		add("Contracts", "pass", "no contract drift")
	}

	var unmet []string
	for _, d := range t.DependsOn {
		if e.State(d).Status != model.Done {
			unmet = append(unmet, d)
		}
	}
	if len(unmet) > 0 {
		add("Dependencies", "warn", "built before "+strings.Join(unmet, ", ")+" finished")
	} else {
		add("Dependencies", "pass", "all upstream tasks DONE")
	}

	base := e.Project().BaseBranch
	head := "HEAD"
	if st.Branch != "" && gitx.RefExists(e.Root(), st.Branch) {
		head = st.Branch
	}
	violations := rules.CheckLines(rules.Load(e.Root()), gitx.AddedLines(e.Root(), base, head))
	if len(violations) > 0 {
		var vs []string
		for _, v := range first2(violations, 3) {
			vs = append(vs, fmt.Sprintf("%s in %s: %s", v.Rule.ID, v.File, v.Excerpt))
		}
		add("Constitution", "fail", strings.Join(vs, "; "))
	} else {
		add("Constitution", "pass", "no machine-checkable rule violated")
	}

	hasCmd := false
	for _, a := range t.Acceptance {
		if a.Check != nil && a.Check.Type == "command" {
			hasCmd = true
		}
	}
	switch {
	case h.Tests.Ran:
		passed := h.Tests.Passed != nil && *h.Tests.Passed
		detail := h.Tests.Summary
		if detail == "" {
			detail = map[bool]string{true: "passed", false: "failed"}[passed]
		}
		add("Tests", map[bool]string{true: "pass", false: "fail"}[passed], detail)
	case hasCmd:
		add("Tests", "skip", "covered by command-checked acceptance criteria")
	case t.Layer == "architecture":
		add("Tests", "skip", "planning task")
	default:
		add("Tests", "fail", `no test evidence in handoff (use --tests "<cmd>")`)
	}

	met := 0
	var unmetCrit []string
	for i, c := range t.Acceptance {
		ok := false
		switch {
		case c.Check == nil:
			ok = containsInt(h.Attested, i+1)
		case c.Check.Type == "command":
			ok, _ = sh(c.Check.Cmd, workdir)
		case c.Check.Type == "file-exists":
			_, err := os.Stat(filepath.Join(workdir, c.Check.Path))
			ok = err == nil
		case c.Check.Type == "contains":
			if b, err := os.ReadFile(filepath.Join(workdir, c.Check.Path)); err == nil {
				ok, _ = regexp.MatchString(c.Check.Pattern, string(b))
			}
		}
		if ok {
			met++
		} else if c.Check == nil {
			unmetCrit = append(unmetCrit, fmt.Sprintf("%d (not attested)", i+1))
		} else {
			unmetCrit = append(unmetCrit, fmt.Sprint(i+1))
		}
	}
	detail := fmt.Sprintf("%d/%d", met, len(t.Acceptance))
	if len(unmetCrit) > 0 {
		detail += "; unmet: " + strings.Join(unmetCrit, ", ")
	}
	status := "pass"
	if met != len(t.Acceptance) {
		status = "fail"
	}
	add("Acceptance criteria", status, detail)

	needsApproval := len(t.Impact) > 0
	failed := false
	var failures []string
	for _, c := range checks {
		if c.Status == "fail" {
			failed = true
			failures = append(failures, c.Name+": "+c.Detail)
		}
	}
	verdict := "DONE"
	switch {
	case failed:
		verdict = "FAILED"
		e.SetState(t.ID, func(s *model.TaskState) { s.Status = model.InProgress })
		e.Notify(t.ID, st.AgentID, fmt.Sprintf("Verification of %s failed: %s", t.ID, strings.Join(failures, "; ")), "verify")
	case needsApproval:
		verdict = "READY_FOR_REVIEW"
		e.SetState(t.ID, func(s *model.TaskState) { s.Status = model.Review })
	default:
		e.SetState(t.ID, func(s *model.TaskState) { s.Status = model.Done })
		if st.AgentID != "" {
			e.SetAgent(st.AgentID, "idle", "")
		}
		e.NotifyUnblocked(t.ID)
	}
	e.Event("verify", t.ID, st.AgentID, map[string]string{"verdict": verdict})
	return &Verification{TaskID: t.ID, Checks: checks, Met: met, Total: len(t.Acceptance), NeedsApproval: needsApproval, Verdict: verdict}, nil
}

func first(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func first2(s []rules.Violation, n int) []rules.Violation {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
