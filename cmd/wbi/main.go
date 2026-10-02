// Command wbi is the Who Broke It? CLI.
package main

import (
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
	"github.com/shriyashish-mishra/who-broke-it/internal/graph"
	"github.com/shriyashish-mishra/who-broke-it/internal/mcp"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
	"github.com/shriyashish-mishra/who-broke-it/internal/render"
	"github.com/shriyashish-mishra/who-broke-it/internal/replicate"
	"github.com/shriyashish-mishra/who-broke-it/internal/store"
)

// version is set at release time: -ldflags "-X main.version=v0.1.0". Binaries built with `go install` carry
// the module version instead, which Go embeds in the build info.
var version = "dev"

func init() {
	if version != "dev" {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = bi.Main.Version
	}
}

var boolFlags = map[string]bool{"force": true, "json": true, "worktree": true, "mermaid": true, "dot": true, "approve": true, "prompt": true, "no-hook": true, "mark-read": true, "help": true, "version": true, "all": true, "require-task": true, "require-handoff": true, "require-approval": true, "approved": true}
var multiFlags = map[string]bool{"contract": true, "limitation": true}

type args struct {
	pos   []string
	flags map[string]string
	multi map[string][]string
}

func (a args) has(k string) bool   { _, ok := a.flags[k]; return ok }
func (a args) get(k string) string { return a.flags[k] }
func (a args) arg(i int) string {
	if i < len(a.pos) {
		return a.pos[i]
	}
	return ""
}

func parseArgs(argv []string) args {
	a := args{flags: map[string]string{}, multi: map[string][]string{}}
	for i := 0; i < len(argv); i++ {
		s := argv[i]
		switch {
		case s == "-h":
			a.flags["help"] = "true"
		case strings.HasPrefix(s, "--"):
			k, v, hasV := strings.Cut(s[2:], "=")
			if !hasV && !boolFlags[k] && i+1 < len(argv) {
				i++
				v, hasV = argv[i], true
			}
			if !hasV {
				v = "true"
			}
			if multiFlags[k] {
				a.multi[k] = append(a.multi[k], v)
			} else {
				a.flags[k] = v
			}
		default:
			a.pos = append(a.pos, s)
		}
	}
	return a
}

func help() string {
	b, d := render.Bold, render.Dim
	return fmt.Sprintf(`%s  %s
%s

%s
  wbi init                              create .wbi/ (constitution, state, commit hook)
  wbi plan "<goal>" [--prompt] [--from plan.json] [--agent-cmd "<cmd>"] [--force]
  wbi simulate [--agents N] [--approve] dry-run the plan: waves, critical path, conflicts

%s
  wbi status [--json]                   progress, who is doing what, what needs attention
  wbi tasks [--status READY]            all tasks      wbi task <id>   work packet
  wbi agents                            registered agents
  wbi sync [init|status]                share claims/intents/handoffs with teammates over git (auto when enabled)
  wbi graph [--mermaid|--dot|--json]    the engineering graph

%s  %s
  wbi claim <id>                        reserve a READY task
  wbi start <id> [--worktree]           claim + branch/worktree + print the work packet
  wbi intent declare <KIND> <target> --task <id>   KIND: CREATE MODIFY DELETE CHANGE_CONTRACT DEPEND_ON
  wbi intent [list|release <id>]
  wbi handoff <id> --summary "..." [--tests "<cmd>"] [--contract "Name=note"] [--attest 1,2] [--limitation "..."]
  wbi inbox   wbi ack <id>              contract changes that affect you

%s
  wbi blame <path>                      who changed it, under which task, what it affects
  wbi why <path>                        requirement → task → agent → commit → PR
  wbi blast <contract|TASK-id|path>     blast radius
  wbi verify <id>   wbi approve <id>    judge a handoff / human sign-off
  wbi drift                             product, architecture and context drift
  wbi check [--base main] [--format github]   merge gate: scope, contracts, constitution, handoff (for CI)
  wbi decide "<title>" --why "..."      record an architecture decision

%s
  wbi adapters install <claude|codex|gemini|cursor|opencode|all>   write agent instruction files
  wbi adapters mcp <agent>              print MCP config      wbi mcp   run the MCP server
`, render.Header(), d(version), d("The agents write the code. Who Broke It? coordinates the engineering."),
		b("Setup"), b("Mission control"), b("Doing work"), d("(identify with --agent <tool> --as <human>, or $WBI_AGENT / $WBI_DEVELOPER)"), b(`Answering "who broke it?"`), b("Integrations"))
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

var exitCode int

func main() {
	if err := run(os.Args[1:]); err != nil {
		var we *model.WbiError
		if errors.As(err, &we) {
			fmt.Fprintln(os.Stderr, render.Red("✖ ")+we.Msg)
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(1)
	}
	os.Exit(exitCode)
}

func run(argv []string) error {
	a := parseArgs(argv)
	cmd := a.arg(0)
	if cmd == "" || a.has("help") || cmd == "help" {
		fmt.Print(help())
		return nil
	}
	if a.has("version") || cmd == "version" {
		fmt.Println(version)
		return nil
	}
	cwd, _ := os.Getwd()
	ident := engine.AgentOpts{Agent: a.get("agent"), As: a.get("as"), Session: a.get("session")}
	rest := a.pos[1:]
	restArg := func(i int) string {
		if i < len(rest) {
			return rest[i]
		}
		return ""
	}
	p := fmt.Println

	// commands that do not need an existing graph
	switch cmd {
	case "init":
		r, err := engine.Init(cwd, engine.InitOpts{Name: a.get("name"), Base: a.get("base"), Force: a.has("force"), NoHook: a.has("no-hook")})
		if err != nil {
			return err
		}
		p(fmt.Sprintf("%s  initialized %s (base branch: %s)\n", render.Header(), render.Bold(r.Project.Name), r.Project.BaseBranch))
		p("  " + render.Green("✓") + " .wbi/ created (constitution.md, project.json)")
		p("  " + render.Green("✓") + " runtime state is local-only: <git common dir>/wbi/state.db (shared by all worktrees, never committed)")
		switch r.Hook {
		case "installed":
			p("  " + render.Green("✓") + " commit hook: prepare-commit-msg adds WBI-Task / WBI-Agent trailers")
		case "exists":
			p("  " + render.Yellow("!") + " commit hook: a different prepare-commit-msg hook exists; not touched")
		default:
			p("  " + render.Yellow("!") + " commit hook: " + r.Hook)
		}
		if !r.HasCommit {
			p(render.Yellow("  ! repo has no commits yet: make one before `wbi start --worktree`"))
		}
		p("\nNext: " + render.Cyan(`wbi plan "Build ..."`))
		return nil
	case "plan":
		r, err := engine.PlanProject(cwd, strings.Join(rest, " "), engine.PlanOpts{From: a.get("from"), AgentCmd: a.get("agent-cmd"), Force: a.has("force"), PromptOnly: a.has("prompt")})
		if err != nil {
			return err
		}
		if r.Prompt != "" {
			fmt.Print(r.Prompt)
			return nil
		}
		pl := r.Plan
		p(fmt.Sprintf("%s  plan generated by %s\n", render.Header(), r.Source))
		p(fmt.Sprintf("  %d requirements · %d components · %d tasks · %d contracts · %d decisions", len(pl.Requirements), len(pl.Components), len(pl.Tasks), len(pl.Contracts), len(pl.Decisions)))
		e, err := engine.New(cwd)
		if err != nil {
			return err
		}
		defer e.Close()
		s := e.Simulate(3)
		p(fmt.Sprintf("  %d waves · critical path %d tasks · %d tasks can run in parallel · %d potential conflicts\n", len(s.Waves), len(s.CriticalPath), s.Parallelizable, len(s.Conflicts)))
		p("  Written to .wbi/ (project.md, architecture.md, tasks/, contracts/, decisions/). Review and edit, then commit it.")
		p(fmt.Sprintf("  Next: %s to review, %s for mission control.", render.Cyan("wbi simulate"), render.Cyan("wbi status")))
		return nil
	case "adapters":
		if restArg(0) == "mcp" {
			a2 := restArg(1)
			if a2 == "" {
				a2 = "claude"
			}
			fmt.Print(engine.MCPConfig(a2, ""))
			return nil
		}
		s, err := store.Find(cwd)
		if err != nil {
			return err
		}
		if restArg(0) == "install" {
			targets := []string{"claude", "codex", "gemini", "cursor"}
			if t := restArg(1); t != "" && t != "all" {
				targets = []string{t}
			}
			for _, t := range targets {
				f, err := engine.InstallGuidance(s.Root, t)
				if err != nil {
					return err
				}
				p(render.Green("✓") + " " + f)
			}
			return nil
		}
		p(fmt.Sprintf("usage: wbi adapters install <%s|all>  |  wbi adapters mcp <agent>", strings.Join(engine.TargetNames(), "|")))
		return nil
	case "hook":
		s, err := store.Find(cwd)
		if err != nil {
			return err
		}
		p("commit hook: " + engine.InstallHook(s.Root))
		return nil
	}

	e, err := engine.New(cwd)
	if err != nil {
		return err
	}
	defer e.Close()
	e.Warn = func(m string) { fmt.Fprintln(os.Stderr, render.Yellow(m)) }

	if cmd == "sync" {
		return syncCmd(e, a, rest)
	}
	switch {
	case isWrite(cmd, a, rest):
		return e.Synced(true, func() error { return execute(e, cmd, a, ident, rest) })
	case readCmds[cmd]:
		age := 3 * time.Second
		if cmd == "inbox" || cmd == "intent" {
			age = 0 // coordination reads must never be stale
		}
		e.PullIfStale(age)
	}
	return execute(e, cmd, a, ident, rest)
}

// Commands that change shared coordination state: pulled before, published after.
var writeCmds = map[string]bool{"claim": true, "release": true, "start": true, "handoff": true, "verify": true, "approve": true, "ack": true}

// Commands that only read: refreshed from the team first (throttled).
var readCmds = map[string]bool{"status": true, "tasks": true, "task": true, "agents": true, "blast": true, "blame": true, "why": true, "drift": true, "inbox": true, "intent": true, "graph": true, "simulate": true}

func isWrite(cmd string, a args, rest []string) bool {
	switch cmd {
	case "intent":
		return len(rest) > 0 && rest[0] != "list"
	case "agents":
		return len(rest) > 0 && rest[0] == "register"
	case "simulate":
		return a.has("approve")
	}
	return writeCmds[cmd]
}

func execute(e *engine.Engine, cmd string, a args, ident engine.AgentOpts, rest []string) error {
	restArg := func(i int) string {
		if i < len(rest) {
			return rest[i]
		}
		return ""
	}
	p := fmt.Println

	switch cmd {
	case "status":
		v := e.Status()
		if a.has("json") {
			printJSON(v)
		} else {
			p("\n" + render.Status(v))
		}
	case "tasks":
		st := e.States()
		want := strings.ToUpper(a.get("status"))
		for _, t := range e.Tasks() {
			d := e.Display(t, st)
			if want == "" || d == want {
				p(render.TaskRow(t, d, st[t.ID].AgentID))
			}
		}
	case "task":
		t, err := e.Task(restArg(0))
		if err != nil {
			return err
		}
		d := e.Display(t, e.States())
		p(fmt.Sprintf("%s  %s\n", render.Bold(d), render.Dim("("+e.State(t.ID).Status+")")))
		if d == model.Blocked {
			p(e.ExplainBlocked(t.ID) + "\n")
		}
		ctx, _ := e.Context(t.ID)
		fmt.Print(ctx)
	case "agents":
		if restArg(0) == "register" {
			o := ident
			for _, c := range strings.Split(a.get("capabilities"), ",") {
				if c != "" {
					o.Capabilities = append(o.Capabilities, c)
				}
			}
			ag, err := e.ResolveAgent(o)
			if err != nil {
				return err
			}
			p("registered " + render.Bold(ag.ID))
			return nil
		}
		ags := e.Agents()
		if len(ags) == 0 {
			p("No agents registered yet. They register on first claim, or: wbi agents register --agent claude")
		}
		for _, ag := range ags {
			dot, task := render.Gray("○"), ag.CurrentTask
			if ag.Status == "working" {
				dot = render.Green("●")
			}
			if task == "" {
				task = "—"
			}
			p(fmt.Sprintf("%s %-9s %s %-14s %s  %s", dot, ag.Provider, render.Dim("dev:"), ag.Developer, task, render.Dim("branch "+ag.Branch+", seen "+render.Ago(ag.Heartbeat))))
		}
	case "graph":
		return graphCmd(e, a)
	case "claim":
		r, err := e.Claim(restArg(0), ident, a.has("force"))
		if err != nil {
			return err
		}
		p(fmt.Sprintf("%s %s claimed %s %s  %s", render.Green("✓"), r.Agent.ID, render.Bold(r.Task.ID), r.Task.Title, render.Dim("branch "+r.State.Branch)))
	case "release":
		if err := e.Release(restArg(0)); err != nil {
			return err
		}
		p("released")
	case "start":
		r, err := e.Start(restArg(0), ident, a.has("worktree"), a.has("force"))
		if err != nil {
			return err
		}
		var approved string
		if e.DB.QueryRow(`SELECT value FROM kv WHERE key='plan_approved'`).Scan(&approved) != nil {
			p(render.Yellow("note: the plan has not been approved yet (wbi simulate --approve)\n"))
		}
		fmt.Print(r.Context)
		p(render.Dim(strings.Repeat("─", 60)))
		p(fmt.Sprintf("%s %s started by %s", render.Green("✓"), render.Bold(r.Task.ID), r.Agent.ID))
		hint := ""
		if !a.has("worktree") {
			hint = render.Dim("   (git switch -c " + r.Branch + ")")
		}
		p("  branch:   " + r.Branch + hint)
		p("  workdir:  " + r.Workdir)
		p("  packet:   " + r.ContextFile)
		p(fmt.Sprintf("  env:      export WBI_AGENT=%s WBI_DEVELOPER=%q", r.Env["WBI_AGENT"], r.Env["WBI_DEVELOPER"]))
	case "intent":
		return intentCmd(e, a, ident, rest)
	case "handoff":
		o := engine.HandoffOpts{AgentOpts: ident, Summary: a.get("summary"), RunTests: a.get("tests"), TestsSummary: a.get("tests-summary"), Contracts: a.multi["contract"], Limitations: a.multi["limitation"]}
		if a.has("tests-passed") {
			v := a.get("tests-passed") == "true"
			o.TestsPassed = &v
		}
		for _, s := range strings.Split(a.get("attest"), ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
				o.Attest = append(o.Attest, n)
			}
		}
		o.PR, _ = strconv.Atoi(a.get("pr"))
		r, err := e.SubmitHandoff(restArg(0), o)
		if err != nil {
			return err
		}
		p(render.Handoff(r.Handoff) + "\n")
		p(render.Verification(r.Verification))
		if r.Verification.Verdict == "FAILED" {
			exitCode = 1
		}
	case "verify":
		v, err := e.VerifyTask(restArg(0))
		if err != nil {
			return err
		}
		p(render.Verification(*v))
		if v.Verdict == "FAILED" {
			exitCode = 1
		}
	case "approve":
		by := a.get("as")
		if by == "" {
			by = os.Getenv("WBI_DEVELOPER")
		}
		if by == "" {
			by = "human"
		}
		if err := e.Approve(restArg(0), by); err != nil {
			return err
		}
		p(fmt.Sprintf("%s %s approved → DONE", render.Green("✓"), restArg(0)))
	case "ack":
		if err := e.Ack(restArg(0)); err != nil {
			return err
		}
		p("acknowledged")
	case "inbox":
		agentID := ""
		if ident.Agent != "" || os.Getenv("WBI_AGENT") != "" {
			ag, err := e.ResolveAgent(ident)
			if err != nil {
				return err
			}
			agentID = ag.ID
		}
		items := e.Inbox(agentID, a.has("mark-read"))
		if len(items) == 0 {
			p("Inbox empty.")
		}
		for _, n := range items {
			p(fmt.Sprintf("%-10s %s  %s", render.Dim(render.Ago(n.TS)), n.TaskID, n.Msg))
		}
	case "simulate":
		n, _ := strconv.Atoi(a.get("agents"))
		s := e.Simulate(n)
		p(render.Simulation(s))
		if a.has("approve") {
			if len(s.MissingDeps) > 0 {
				return model.Errf("Cannot approve a plan with missing dependencies.")
			}
			var parts []any
			for _, t := range e.Tasks() {
				parts = append(parts, []any{t.ID, t.DependsOn, t.AllowedPaths})
			}
			b, _ := json.Marshal(parts)
			hash := fmt.Sprintf("%x", sha1.Sum(b))[:8]
			_, _ = e.DB.Exec(`INSERT OR REPLACE INTO kv (key,value) VALUES ('plan_approved', ?)`, hash)
			p(fmt.Sprintf("\n%s plan approved (%s). Agents may start.", render.Green("✓"), hash))
		} else {
			p("\n" + render.Dim("Approve with: wbi simulate --approve"))
		}
	case "blast":
		b := e.Blast(strings.Join(rest, " "))
		if a.has("json") {
			printJSON(b)
		} else {
			p(render.Blast(b))
		}
	case "blame":
		path := restArg(0)
		if path == "" {
			path = "."
		}
		r := e.Blame(path, 5)
		if a.has("json") {
			printJSON(r)
		} else {
			p(render.Blame(path, r))
		}
	case "why":
		return whyCmd(e, restArg(0))
	case "drift":
		d := e.Drift()
		if a.has("json") {
			printJSON(d)
		} else {
			p(render.Drift(d))
		}
	case "decide":
		var tasks []string
		for _, t := range strings.Split(a.get("task"), ",") {
			if t != "" {
				tasks = append(tasks, t)
			}
		}
		d, err := e.Decide(strings.Join(rest, " "), a.get("why"), tasks)
		if err != nil {
			return err
		}
		p(fmt.Sprintf("%s %s recorded: %s", render.Green("✓"), d.ID, d.Title))
	case "check":
		rep, err := e.Check(engine.CheckOpts{Base: a.get("base"), Head: a.get("head"), Branch: a.get("branch"), Task: a.get("task"),
			RequireTask: a.has("require-task"), RequireHandoff: a.has("require-handoff"), RequireApproval: a.has("require-approval"), Approved: a.has("approved")})
		if err != nil {
			return err
		}
		switch a.get("format") {
		case "json":
			printJSON(rep)
		case "github":
			fmt.Print(render.CheckGitHub(*rep))
		default:
			fmt.Print(render.Check(*rep))
		}
		if !rep.OK {
			exitCode = 1
		}
	case "mcp":
		return mcp.Serve(e, os.Stdin, os.Stdout)
	default:
		return model.Errf(`Unknown command "%s". Try: wbi help`, cmd)
	}
	return nil
}

func intentCmd(e *engine.Engine, a args, ident engine.AgentOpts, rest []string) error {
	sub := "list"
	if len(rest) > 0 {
		sub = rest[0]
	}
	switch sub {
	case "declare", "check":
		kind := ""
		if len(rest) > 1 {
			kind = strings.ToUpper(rest[1])
		}
		target := ""
		if len(rest) > 2 {
			target = strings.Join(rest[2:], " ")
		}
		if kind == "" || target == "" || !a.has("task") {
			return model.Errf("usage: wbi intent declare <CREATE|MODIFY|DELETE|CHANGE_CONTRACT|DEPEND_ON> <target> --task <id> [--note \"...\"]")
		}
		r, err := e.DeclareIntent(ident, a.get("task"), kind, target, a.get("note"))
		if err != nil {
			return err
		}
		if len(r.Conflicts) > 0 {
			fmt.Println(render.Conflicts(r.Conflicts))
		}
		if r.Blocked {
			fmt.Printf("\n%s Resolve the blocking conflict(s) first.\n", render.Red("✖ Intent NOT registered."))
			exitCode = 2
			return nil
		}
		warn := ""
		if len(r.Conflicts) > 0 {
			warn = render.Yellow("  (with warnings)")
		}
		fmt.Printf("%s intent #%d registered: %s %s%s\n", render.Green("✓"), r.Intent.ID, kind, target, warn)
	case "release":
		e.ReleaseIntents(strings.ToUpper(restOr(rest, 1)))
		fmt.Println("released")
	default:
		all := e.Intents(false)
		if len(all) == 0 {
			fmt.Println("No active intents.")
		}
		for _, i := range all {
			fmt.Printf("#%-3d %-18s %s  %s %s  %s\n", i.ID, i.AgentID, i.TaskID, render.Bold(fmt.Sprintf("%-15s", i.Kind)), i.Target, render.Dim(render.Ago(i.CreatedAt)))
		}
		for _, c := range e.IntentCollisions() {
			fmt.Println(render.Yellow(fmt.Sprintf("⚠ collision: #%d ↔ #%d (%s / %s)", c[0].ID, c[1].ID, c[0].Target, c[1].Target)))
		}
	}
	return nil
}

func restOr(rest []string, i int) string {
	if i < len(rest) {
		return rest[i]
	}
	return ""
}

func whyCmd(e *engine.Engine, path string) error {
	r := e.Why(path)
	if r.Introducing == nil && r.Task == nil {
		fmt.Println("No history and no owning task found for that path.")
		return nil
	}
	fmt.Println(render.Bold(fmt.Sprintf("Why does %s exist?\n", path)))
	if r.Task != nil {
		fmt.Printf("Introduced by:   %s %s\n", r.Task.ID, r.Task.Title)
	} else {
		fmt.Printf("Introduced by:   %s\n", render.Dim("(no task trailer on the introducing commit)"))
	}
	for _, q := range r.Reqs {
		fmt.Printf("Requirement:     %s %s\n", q.ID, q.Text)
	}
	for _, d := range r.Decisions {
		fmt.Printf("Decision:        %s %s\n", d.ID, d.Title)
	}
	by := r.Executor
	if by == "" && r.Introducing != nil {
		by = r.Introducing.Who
	}
	if by == "" {
		by = "—"
	}
	if r.Owner != "" {
		by += " / " + r.Owner
	}
	fmt.Printf("Implemented by:  %s\n", by)
	if r.Introducing != nil {
		pr := ""
		if r.Introducing.PR > 0 {
			pr = fmt.Sprintf("  (PR #%d)", r.Introducing.PR)
		}
		fmt.Printf("Commit:          %s %s%s\n", r.Introducing.SHA, r.Introducing.Subject, pr)
	}
	if r.Latest != nil && r.Introducing != nil && r.Latest.SHA != r.Introducing.SHA {
		t := ""
		if r.Latest.TaskID != "" {
			t = " (" + r.Latest.TaskID + ")"
		}
		fmt.Printf("Last changed:    %s by %s %s%s\n", r.Latest.SHA, r.Latest.Who, render.Ago(r.Latest.TS), t)
	}
	return nil
}

func graphCmd(e *engine.Engine, a args) error {
	tasks, st := e.Tasks(), e.States()
	if a.has("json") {
		printJSON(map[string]any{"tasks": tasks, "contracts": e.Contracts(), "components": e.Store.Components()})
		return nil
	}
	id := func(s string) string { return strings.NewReplacer("-", "_", " ", "_").Replace(s) }
	switch {
	case a.has("mermaid"):
		fmt.Println("graph TD")
		for _, t := range tasks {
			fmt.Printf("  %s[\"%s %s<br/>%s\"]\n", id(t.ID), t.ID, strings.ReplaceAll(t.Title, `"`, "'"), e.Display(t, st))
		}
		for _, t := range tasks {
			for _, d := range t.DependsOn {
				fmt.Printf("  %s --> %s\n", id(d), id(t.ID))
			}
		}
	case a.has("dot"):
		fmt.Println("digraph wbi {\n  rankdir=LR;")
		for _, t := range tasks {
			fmt.Printf("  %s [label=\"%s\\n%s\\n%s\"];\n", id(t.ID), t.ID, t.Title, e.Display(t, st))
		}
		for _, t := range tasks {
			for _, d := range t.DependsOn {
				fmt.Printf("  %s -> %s;\n", id(d), id(t.ID))
			}
		}
		fmt.Println("}")
	default:
		tm := map[string]model.Task{}
		for _, t := range tasks {
			tm[t.ID] = t
		}
		for i, w := range graph.Waves(tasks) {
			fmt.Println(render.Bold(fmt.Sprintf("WAVE %d", i+1)))
			for _, tid := range w {
				t := tm[tid]
				fmt.Println("  " + render.TaskRow(t, e.Display(t, st), ""))
				if len(t.Provides) > 0 {
					fmt.Println(render.Dim("      provides " + strings.Join(t.Provides, ", ")))
				}
				needs := append([]string{}, t.DependsOn...)
				for _, c := range t.Consumes {
					needs = append(needs, "⟨"+c+"⟩")
				}
				if len(needs) > 0 {
					fmt.Println(render.Dim("      needs    " + strings.Join(needs, ", ")))
				}
			}
		}
	}
	return nil
}

func syncCmd(e *engine.Engine, a args, rest []string) error {
	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}
	switch sub {
	case "init":
		remote, ref := a.get("remote"), a.get("ref")
		if remote == "" {
			remote = "origin"
		}
		if ref == "" {
			ref = replicate.DefaultRef
		}
		if err := e.EnableSync(remote, ref); err != nil {
			return err
		}
		fmt.Printf("%s sync enabled: events are published to %s on remote %q\n", render.Green("✓"), ref, remote)
		fmt.Println("  Commit .wbi/project.json so teammates sync automatically:  git add .wbi/project.json && git commit -m 'chore: enable wbi sync' && git push")
		if _, _, why := e.SyncConfig(); why != "" {
			fmt.Println(render.Yellow("  ! " + why))
		}
		return nil
	case "status":
		i := e.SyncStatus()
		if !i.Configured {
			fmt.Printf("sync: %s\n", render.Yellow("off — "+i.Why))
			fmt.Printf("  local events waiting: %d\n", i.Pending)
			return nil
		}
		last := "never"
		if i.LastSyncMs > 0 {
			last = render.Ago(i.LastSyncMs)
		}
		fmt.Printf("sync: %s  remote=%s ref=%s\n", render.Green("on"), i.Remote, i.Ref)
		fmt.Printf("  this clone: actor %s\n  unpublished local events: %d\n  claims that lost a race: %d\n  last sync: %s\n", i.Actor, i.Pending, i.Rejected, last)
		return nil
	case "", "now":
		rep, err := e.Sync()
		if err != nil {
			return err
		}
		fmt.Printf("%s synced: %d event(s) from teammates, %d published", render.Green("✓"), rep.Applied, rep.Pushed)
		if rep.Attempts > 1 {
			fmt.Printf(render.Dim("  (raced %d time(s), retried)"), rep.Attempts-1)
		}
		fmt.Println()
		for _, r := range rep.Rejected {
			fmt.Println(render.Yellow("  ⚠ " + r))
		}
		return nil
	}
	return model.Errf("usage: wbi sync [init [--remote origin] [--ref refs/wbi/sync] | status | now]")
}
