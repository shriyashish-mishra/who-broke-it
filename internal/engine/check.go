package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/shriyashish-mishra/who-broke-it/internal/gitx"
	"github.com/shriyashish-mishra/who-broke-it/internal/glob"
	"github.com/shriyashish-mishra/who-broke-it/internal/graph"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
	"github.com/shriyashish-mishra/who-broke-it/internal/rules"
)

// CheckOpts configures `wbi check`, the merge gate. It needs only the repository (no runtime state),
// so it runs on a fresh CI checkout.
type CheckOpts struct {
	Base, Head string // compared as base...head, like a pull request
	Branch     string // used to find the task (wbi/TASK-n); defaults to the current branch
	Task       string // explicit task, overrides detection

	RequireTask     bool // fail (not warn) when the change is not linked to a task
	RequireHandoff  bool // fail when the task branch carries no handoff record
	RequireApproval bool // fail high-impact changes unless Approved
	Approved        bool // a human reviewer approved (supplied by CI)
}

type Finding struct {
	Level   string `json:"level"` // error | warn | notice
	Code    string `json:"code"`
	Message string `json:"message"`
	File    string `json:"file,omitempty"`
}

type CheckReport struct {
	Task     string    `json:"task,omitempty"`
	Base     string    `json:"base"`
	Head     string    `json:"head"`
	Files    []string  `json:"files"`
	Findings []Finding `json:"findings"`
	OK       bool      `json:"ok"`
}

var branchTaskRe = regexp.MustCompile(`(?:^|/)wbi/(TASK-\d+)`)
var trailerTaskRe = regexp.MustCompile(`(?mi)^WBI-Task:\s*(TASK-\d+)`)

func (r *CheckReport) add(level, code, msg, file string) {
	r.Findings = append(r.Findings, Finding{level, code, msg, file})
}

// Check enforces the Engineering Graph on a diff: scope, contract ownership and versioning, the
// constitution, plan integrity and (optionally) handoff + human approval for high-impact work.
func (e *Engine) Check(o CheckOpts) (*CheckReport, error) {
	root := e.Root()
	if o.Head == "" {
		o.Head = "HEAD"
	}
	if o.Base == "" {
		o.Base = e.Project().BaseBranch
	}
	for _, ref := range []string{o.Base, o.Head} {
		if !gitx.RefExists(root, ref) {
			return nil, model.Errf("cannot resolve %q: fetch enough history (actions/checkout needs fetch-depth: 0)", ref)
		}
	}
	rep := &CheckReport{Base: o.Base, Head: o.Head, Files: gitx.ChangedFiles(root, o.Base, o.Head)}

	// ---- which task is this change for?
	taskID := strings.ToUpper(o.Task)
	if taskID == "" {
		b := o.Branch
		if b == "" && o.Head != "HEAD" {
			b = o.Head // the ref being checked names the task, not whatever is checked out
		}
		if b == "" {
			b = gitx.CurrentBranch(root)
		}
		if m := branchTaskRe.FindStringSubmatch(b); m != nil {
			taskID = m[1]
		}
	}
	if taskID == "" {
		seen := map[string]bool{}
		for _, msg := range gitx.Messages(root, o.Base, o.Head) {
			if m := trailerTaskRe.FindStringSubmatch(msg); m != nil {
				seen[strings.ToUpper(m[1])] = true
			}
		}
		var ids []string
		for id := range seen {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if len(ids) > 1 {
			rep.add("warn", "MIXED_TASKS", "commits belong to several tasks ("+strings.Join(ids, ", ")+"); one task per PR keeps ownership clear", "")
		}
		if len(ids) > 0 {
			taskID = ids[0]
		}
	}

	var task *model.Task
	if taskID != "" {
		if t, err := e.Task(taskID); err == nil {
			task = &t
			rep.Task = t.ID
		} else {
			rep.add("error", "UNKNOWN_TASK", fmt.Sprintf("%s is not in the Engineering Graph", taskID), "")
		}
	}
	if task == nil && taskID == "" {
		lvl := "warn"
		if o.RequireTask {
			lvl = "error"
		}
		rep.add(lvl, "NO_TASK", "this change is not linked to a wbi task (branch wbi/TASK-n, a WBI-Task: trailer, or --task)", "")
	}

	// ---- plan integrity (the graph on the head ref must still be a valid DAG)
	for _, i := range graph.Validate(e.Tasks(), e.Contracts()) {
		if i.Severity == "error" {
			rep.add("error", "PLAN_INVALID", i.Message, ".wbi")
		}
	}

	// ---- constitution: machine-checkable rules on every added line
	for _, v := range rules.CheckLines(rules.Load(root), gitx.AddedLines(root, o.Base, o.Head)) {
		rep.add("error", "CONSTITUTION", fmt.Sprintf("%s: %s (%s)", v.Rule.ID, v.Rule.Text, v.Excerpt), v.File)
	}

	// ---- contracts changed in this diff (compare base vs head)
	handoff := (*model.Handoff)(nil)
	if task != nil {
		if body, ok := gitx.Show(root, o.Head, ".wbi/handoffs/"+task.ID+".json"); ok {
			var h model.Handoff
			if json.Unmarshal([]byte(body), &h) == nil {
				handoff = &h
			}
		}
	}
	for _, f := range rep.Files {
		if !strings.HasPrefix(f, ".wbi/contracts/") || !strings.HasSuffix(f, ".json") {
			continue
		}
		var before, after model.Contract
		_, hadBefore := showJSON(root, o.Base, f, &before)
		_, hasAfter := showJSON(root, o.Head, f, &after)
		name := after.Name
		if !hasAfter {
			name = before.Name
		}
		switch {
		case !hadBefore && hasAfter:
			if task == nil || task.Layer != "architecture" {
				rep.add("warn", "CONTRACT_ADDED", fmt.Sprintf("new contract %s added outside an architecture task", name), f)
			}
		case hadBefore && !hasAfter:
			rep.add("error", "CONTRACT_REMOVED", fmt.Sprintf("contract %s was deleted; consumers would break silently", name), f)
		default:
			shapeChanged := before.Shape != after.Shape || before.Kind != after.Kind || before.Public != after.Public
			switch {
			case after.Version < before.Version:
				rep.add("error", "CONTRACT_VERSION", fmt.Sprintf("%s version went backwards (v%d → v%d)", name, before.Version, after.Version), f)
			case shapeChanged && after.Version == before.Version:
				rep.add("error", "CONTRACT_UNVERSIONED", fmt.Sprintf("%s changed shape without a version bump; consumers will not be notified", name), f)
			case after.Version > before.Version:
				if task == nil || after.ProvidedBy != task.ID {
					owner := after.ProvidedBy
					rep.add("error", "CONTRACT_NOT_OWNED", fmt.Sprintf("%s is owned by %s but changed by %s", name, owner, orNone(taskID)), f)
					break
				}
				recorded := false
				if handoff != nil {
					for _, c := range handoff.ContractChanges {
						recorded = recorded || (c.Name == name && c.Version == after.Version)
					}
				}
				if !recorded {
					rep.add("error", "CONTRACT_NO_HANDOFF", fmt.Sprintf("%s bumped to v%d but %s has no handoff record of it (run: wbi handoff %s --contract \"%s=...\")", name, after.Version, task.ID, task.ID, name), f)
				}
			}
		}
	}

	// ---- scope
	if task != nil {
		allowed := append(append([]string{}, task.AllowedPaths...), ".wbi/handoffs/"+task.ID+".json", ".wbi/contracts/**")
		var planChanged []string
		for _, f := range rep.Files {
			switch {
			case glob.MatchesAny(task.RestrictedPaths, f):
				rep.add("error", "SCOPE_RESTRICTED", fmt.Sprintf("%s touches a path restricted for %s", f, task.ID), f)
			case strings.HasPrefix(f, ".wbi/") && !glob.MatchesAny(allowed, f):
				planChanged = append(planChanged, f)
			case !strings.HasPrefix(f, ".wbi/") && !glob.MatchesAny(allowed, f):
				rep.add("error", "SCOPE", fmt.Sprintf("%s is outside %s's allowed paths (%s)", f, task.ID, strings.Join(task.AllowedPaths, ", ")), f)
			}
		}
		if len(planChanged) > 0 && task.Layer != "architecture" && !glob.MatchesAny(task.AllowedPaths, ".wbi/x") {
			rep.add("warn", "PLAN_CHANGE", fmt.Sprintf("%s edits the plan itself (%s); plan changes belong in an architecture task", task.ID, strings.Join(first(planChanged, 3), ", ")), planChanged[0])
		}

		// ---- dependencies: upstream work should be merged (its handoff record is on the base)
		for _, d := range task.DependsOn {
			if _, ok := gitx.Show(root, o.Base, ".wbi/handoffs/"+d+".json"); !ok {
				rep.add("warn", "UNMERGED_DEPENDENCY", fmt.Sprintf("%s depends on %s, which has no merged handoff on %s yet", task.ID, d, o.Base), "")
			}
		}

		// ---- handoff + human gate
		if handoff == nil {
			lvl := "warn"
			if o.RequireHandoff {
				lvl = "error"
			}
			rep.add(lvl, "NO_HANDOFF", fmt.Sprintf("%s has no handoff record on this branch (run: wbi handoff %s ...)", task.ID, task.ID), "")
		}
		if len(task.Impact) > 0 {
			switch {
			case o.Approved:
				rep.add("notice", "HIGH_IMPACT", fmt.Sprintf("%s is high-impact (%s) and has human approval", task.ID, strings.Join(task.Impact, ", ")), "")
			case o.RequireApproval:
				rep.add("error", "NEEDS_APPROVAL", fmt.Sprintf("%s is high-impact (%s): a human reviewer must approve before merge", task.ID, strings.Join(task.Impact, ", ")), "")
			default:
				rep.add("notice", "HIGH_IMPACT", fmt.Sprintf("%s is high-impact (%s): needs human approval", task.ID, strings.Join(task.Impact, ", ")), "")
			}
		}
	}

	rep.OK = true
	for _, f := range rep.Findings {
		if f.Level == "error" {
			rep.OK = false
		}
	}
	return rep, nil
}

func showJSON(root, rev, path string, into any) (string, bool) {
	body, ok := gitx.Show(root, rev, path)
	if !ok {
		return "", false
	}
	return body, json.Unmarshal([]byte(body), into) == nil
}

func orNone(s string) string {
	if s == "" {
		return "(no task)"
	}
	return s
}
