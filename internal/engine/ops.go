package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wbi/internal/gitx"
	"wbi/internal/model"
	"wbi/internal/planner"
	"wbi/internal/rules"
	"wbi/internal/store"
)

type InitOpts struct {
	Name, Base    string
	Force, NoHook bool
}

type InitResult struct {
	Root      string
	Project   model.Project
	Hook      string
	HasCommit bool
}

// Init creates `.wbi/` in the enclosing git repo.
func Init(cwd string, o InitOpts) (*InitResult, error) {
	root := gitx.Toplevel(cwd)
	if root == "" {
		return nil, model.Errf("Who Broke It? needs a git repository. Run `git init` (and make one commit) first.")
	}
	if _, err := os.Stat(filepath.Join(root, ".wbi", "project.json")); err == nil && !o.Force {
		return nil, model.Errf("Already initialized (.wbi/ exists). Use --force to re-initialize.")
	}
	s := &store.Store{Root: root}
	name := o.Name
	if name == "" {
		name = filepath.Base(root)
	}
	base := o.Base
	if base == "" {
		base = gitx.DefaultBranch(root)
	}
	p := model.Project{Version: 1, Name: name, CreatedAt: time.Now().UTC().Format(time.RFC3339), BaseBranch: base}
	if err := s.SaveProject(p); err != nil {
		return nil, err
	}
	if s.ReadText("constitution.md") == "" {
		if err := s.WriteText("constitution.md", rules.DefaultConstitution); err != nil {
			return nil, err
		}
	}
	for _, d := range []string{"requirements", "tasks", "contracts", "decisions", "handoffs"} {
		_ = os.MkdirAll(filepath.Join(s.Dir(), d), 0o755)
	}
	EnsureIgnored(root, ".wbi/state/")
	EnsureIgnored(root, ".wbi/worktrees/")
	hook := "skipped"
	if !o.NoHook {
		hook = InstallHook(root)
	}
	_, hasCommit := gitx.Try(root, "rev-parse", "--verify", "HEAD")
	return &InitResult{root, p, hook, hasCommit}, nil
}

type PlanOpts struct {
	From, AgentCmd    string
	Force, PromptOnly bool
}

type PlanResult struct {
	Plan   *model.Plan
	Prompt string
	Source string
}

// PlanProject generates (or imports) the Engineering Graph.
func PlanProject(cwd, goal string, o PlanOpts) (*PlanResult, error) {
	s, err := store.Find(cwd)
	if err != nil {
		return nil, err
	}
	facts := planner.Inspect(s.Root)
	project, err := s.Project()
	if err != nil {
		return nil, err
	}
	full := goal
	if full == "" {
		full = project.Goal
	}
	if o.PromptOnly {
		return &PlanResult{Prompt: planner.AgentPrompt(full, facts), Source: "prompt"}, nil
	}
	if full == "" && o.From == "" {
		return nil, model.Errf(`Give a product goal: wbi plan "Build a multi-tenant SaaS dashboard with auth, billing and analytics"`)
	}
	if s.HasPlan() && !o.Force {
		return nil, model.Errf("A plan already exists. Use --force to replace it (this resets task state).")
	}
	var plan model.Plan
	var source string
	testCmd := facts.TestCmd
	switch {
	case o.From != "":
		b, err := os.ReadFile(o.From)
		if err != nil {
			return nil, err
		}
		if plan, err = planner.ParsePlan(b); err != nil {
			return nil, err
		}
		source = "file " + o.From
	case o.AgentCmd != "":
		b, err := planner.RunAgentCmd(o.AgentCmd, planner.AgentPrompt(full, facts))
		if err != nil {
			return nil, err
		}
		if plan, err = planner.ParsePlan(b); err != nil {
			return nil, err
		}
		source = "agent command"
	default:
		plan = planner.HeuristicPlan(full, facts)
		source = "built-in planner"
	}
	if err := planner.AssertValid(plan); err != nil {
		return nil, err
	}
	if full != "" {
		project.Goal = full
	}
	project.TestCmd = testCmd
	if err := s.SaveProject(project); err != nil {
		return nil, err
	}
	if err := s.WritePlan(plan); err != nil {
		return nil, err
	}
	_ = s.WriteText("project.md", planner.RenderProjectDoc(project, plan))
	_ = s.WriteText("architecture.md", planner.RenderArchitectureDoc(plan))
	for _, d := range plan.Decisions {
		tasks := "—"
		if len(d.Tasks) > 0 {
			tasks = strings.Join(d.Tasks, ", ")
		}
		_ = s.WriteText("decisions/"+d.ID+".md", fmt.Sprintf("# %s: %s\n\nStatus: %s\n\n%s\n\nTasks: %s\n", d.ID, d.Title, d.Status, d.Why, tasks))
	}
	cons := s.ReadText("constitution.md")
	var fresh []string
	for _, r := range plan.ConstitutionRules {
		key := r
		if len(key) > 40 {
			key = key[:40]
		}
		if !strings.Contains(cons, key) {
			fresh = append(fresh, r)
		}
	}
	if len(fresh) > 0 {
		_ = s.WriteText("constitution.md", strings.TrimRight(cons, "\n")+"\n"+strings.Join(fresh, "\n")+"\n")
	}
	if o.Force {
		e, err := NewWithStore(cwd, s)
		if err != nil {
			return nil, err
		}
		for _, q := range []string{`DELETE FROM task_state`, `UPDATE intents SET status='released'`, `DELETE FROM notifications`, `DELETE FROM task_ack`, `DELETE FROM contract_state`, `DELETE FROM handoffs`} {
			_, _ = e.DB.Exec(q)
		}
		e.Close()
	}
	return &PlanResult{Plan: &plan, Source: source}, nil
}

// EnsureIgnored appends pattern to the repo's .gitignore if it is not already there.
func EnsureIgnored(root, pattern string) {
	gi := filepath.Join(root, ".gitignore")
	cur, _ := os.ReadFile(gi)
	for _, l := range strings.Split(string(cur), "\n") {
		if strings.TrimSpace(l) == pattern {
			return
		}
	}
	add := pattern + "\n"
	if len(cur) > 0 && !strings.HasSuffix(string(cur), "\n") {
		add = "\n" + add
	}
	if f, err := os.OpenFile(gi, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.WriteString(add)
		f.Close()
	}
}
