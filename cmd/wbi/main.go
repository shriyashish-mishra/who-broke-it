// Command wbi is the Who Broke It? CLI.
package main

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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

var boolFlags = map[string]bool{"force": true, "json": true, "worktree": true, "mermaid": true, "dot": true, "approve": true, "prompt": true, "no-hook": true, "mark-read": true, "help": true, "version": true, "all": true, "require-task": true, "require-handoff": true, "require-approval": true, "approved": true, "enforce": true, "draft": true, "dry-run": true, "no-push": true}
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
  wbi sync [init|status|watch|compact]  share claims/intents/handoffs with teammates over git (auto when enabled)
  wbi notify [add slack|discord|webhook --env VAR | test | list]   post key events to Slack/Discord/any webhook
  wbi team [join|add|remove|list|enforce]   who may publish to the shared log (.wbi/team.json, signed events)
  wbi graph [--mermaid|--dot|--json]    the engineering graph

%s  %s
  wbi claim <id>                        reserve a READY task
  wbi start <id> [--worktree]           claim + branch/worktree + print the work packet
  wbi intent declare <KIND> <target> --task <id>   KIND: CREATE MODIFY DELETE CHANGE_CONTRACT DEPEND_ON
  wbi intent [list|release <id>]
  wbi handoff <id> --summary "..." [--tests "<cmd>"] [--contract "Name=note"] [--attest 1,2] [--limitation "..."]
  wbi inbox   wbi ack <id>              contract changes that affect you

%s
  wbi pr <id> [--draft] [--dry-run]     open a PR whose body comes from the task's handoff (needs gh)
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
	case "pr":
		return prCmd(e, a, restArg(0))
	case "team":
		return teamCmd(e, a, rest)
	case "notify":
		return notifyCmd(e, a, rest)
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
		case "markdown":
			fmt.Print(render.CheckMarkdown(*rep))
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
	case "compact":
		keep, _ := strconv.Atoi(a.get("keep-events"))
		if keep <= 0 {
			keep = 500
		}
		rep, err := e.Compact(keep)
		if err != nil {
			return err
		}
		fmt.Printf("%s compacted: the log is now one snapshot of %d row(s)\n", render.Green("✓"), rep.Pushed)
		return nil
	case "watch":
		iv, err := time.ParseDuration(firstNonEmptyStr(a.get("interval"), "5s"))
		if err != nil {
			return model.Errf("bad --interval: %v", err)
		}
		fmt.Printf("%s watching the team every %s (Ctrl-C to stop)\n", render.Cyan("●"), iv)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return e.Watch(ctx, iv, func(r replicate.Report) {
			fmt.Printf("%s %s: %d event(s) from teammates\n", render.Dim(time.Now().Format("15:04:05")), render.Bold("sync"), r.Applied)
			for _, x := range r.Rejected {
				fmt.Println(render.Yellow("  ⚠ " + x))
			}
			for _, x := range r.Quarantined {
				fmt.Println(render.Red("  ✖ " + x))
			}
		})
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
		for _, q := range rep.Quarantined {
			fmt.Println(render.Red("  ✖ " + q))
		}
		return nil
	}
	return model.Errf("usage: wbi sync [init [--remote origin] [--ref refs/wbi/sync] | status | now | watch [--interval 5s] | compact [--keep-events 500]]")
}

func prCmd(e *engine.Engine, a args, taskID string) error {
	d, err := e.BuildPR(taskID)
	if err != nil {
		return err
	}
	if a.has("dry-run") {
		fmt.Printf("# %s   (%s → %s, labels: %s)\n\n%s", d.Title, d.Branch, d.Base, strings.Join(d.Labels, ", "), d.Body)
		return nil
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return model.Errf("the GitHub CLI (gh) is required to open PRs; use --dry-run to print the body")
	}
	dir := e.Root()
	if wt := e.State(strings.ToUpper(taskID)).Worktree; wt != "" {
		dir = wt
	}
	if !a.has("no-push") {
		if out, err := exec.Command("git", "-C", dir, "push", "-u", "origin", d.Branch).CombinedOutput(); err != nil {
			return model.Errf("pushing %s failed: %s", d.Branch, strings.TrimSpace(string(out)))
		}
	}
	args := []string{"pr", "create", "--head", d.Branch, "--base", d.Base, "--title", d.Title, "--body-file", "-"}
	if a.has("draft") {
		args = append(args, "--draft")
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(d.Body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return model.Errf("gh pr create failed: %s", strings.TrimSpace(string(out)))
	}
	url := strings.TrimSpace(string(out))
	if i := strings.LastIndex(url, "/pull/"); i >= 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(url[i+len("/pull/"):])); err == nil {
			e.SetPR(taskID, n)
		}
	}
	for _, l := range d.Labels { // best effort: create the label if the repo lacks it, then apply it
		_ = exec.Command("gh", "label", "create", l, "--force", "--color", labelColor(l)).Run()
		_ = exec.Command("gh", "pr", "edit", url, "--add-label", l).Run()
	}
	fmt.Printf("%s opened %s\n", render.Green("✓"), url)
	return nil
}

func labelColor(l string) string {
	switch l {
	case "high-impact":
		return "d93f0b"
	case "contract-change":
		return "fbca04"
	}
	return "6f42c1"
}

func firstNonEmptyStr(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func teamCmd(e *engine.Engine, a args, rest []string) error {
	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}
	switch sub {
	case "key":
		id, err := e.Identity()
		if err != nil {
			return err
		}
		fmt.Printf("%s\nfingerprint: %s\n", id.PubB64(), replicate.Fingerprint(id.PubB64()))
	case "join":
		t, err := e.TeamJoin(a.get("name"), a.has("enforce"))
		if err != nil {
			return err
		}
		fmt.Printf("%s your key is in .wbi/team.json (%d member(s), signatures %s).\n", render.Green("✓"), len(t.Members), map[bool]string{true: "ENFORCED", false: "advisory"}[t.Enforce])
		fmt.Println("  Commit it through a pull request: merging it is what authorizes you, and every teammate's clone applies your events once it lands.")
	case "add":
		if len(rest) < 2 || a.get("name") == "" {
			return model.Errf("usage: wbi team add <base64-key> --name <person>   (they get it from: wbi team key)")
		}
		if err := e.TeamAdd(a.get("name"), rest[1]); err != nil {
			return err
		}
		fmt.Printf("%s added %s (%s)\n", render.Green("✓"), a.get("name"), replicate.Fingerprint(rest[1]))
	case "remove":
		if err := e.TeamRemove(strings.Join(rest[1:], " ")); err != nil {
			return err
		}
		fmt.Println(render.Green("✓") + " removed; their published events stop being applied once this change is merged")
	case "enforce":
		on := len(rest) < 2 || rest[1] != "off"
		if err := e.TeamSetEnforce(on); err != nil {
			return err
		}
		fmt.Printf("%s signatures %s\n", render.Green("✓"), map[bool]string{true: "ENFORCED: only listed keys are applied", false: "advisory: unlisted keys are applied, forged signatures are still rejected"}[on])
	case "", "list":
		t, _ := replicate.LoadTeam(e.Root())
		id, err := e.Identity()
		if err != nil {
			return err
		}
		mode := "advisory (forged signatures are rejected; unlisted keys are applied)"
		if t.Enforce {
			mode = "ENFORCED (only listed keys are applied)"
		}
		fmt.Printf("signatures: %s\n", mode)
		for _, m := range t.Members {
			me := ""
			if m.Key == id.PubB64() {
				me = render.Cyan("  ← this clone")
			}
			fmt.Printf("  %-16s %s%s\n", m.Name, replicate.Fingerprint(m.Key), me)
		}
		if len(t.Members) == 0 {
			fmt.Println(render.Dim("  no members yet: wbi team join --name <you> --enforce"))
		}
		fmt.Printf("this clone's key: %s\n", replicate.Fingerprint(id.PubB64()))
	default:
		return model.Errf("usage: wbi team [key | join --name N [--enforce] | add <key> --name N | remove N | enforce [on|off] | list]")
	}
	return nil
}

func notifyCmd(e *engine.Engine, a args, rest []string) error {
	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}
	switch sub {
	case "add":
		if len(rest) < 2 {
			return model.Errf("usage: wbi notify add <slack|discord|webhook> --env <VAR_HOLDING_THE_URL> [--events contract_changed,handoff,...]")
		}
		var events []string
		for _, ev := range strings.Split(a.get("events"), ",") {
			if ev = strings.TrimSpace(ev); ev != "" {
				events = append(events, ev)
			}
		}
		if err := e.AddNotify(rest[1], a.get("env"), events); err != nil {
			return err
		}
		fmt.Printf("%s %s notifications enabled; the URL is read from $%s (never stored in the repo)\n", render.Green("✓"), rest[1], a.get("env"))
		fmt.Println("  Commit .wbi/project.json so the team shares the setting; each person exports the variable. Try: wbi notify test")
	case "test":
		res := e.TestNotify()
		if len(res) == 0 {
			fmt.Println("no notification sinks configured: wbi notify add slack --env WBI_SLACK_WEBHOOK")
		}
		for _, r := range res {
			fmt.Println("  " + r)
		}
	default:
		sinks := e.Project().Notify
		if len(sinks) == 0 {
			fmt.Println("no notification sinks configured")
		}
		for _, s := range sinks {
			ev := strings.Join(s.Events, ",")
			if ev == "" {
				ev = strings.Join(engine.DefaultNotifyEvents, ",") + render.Dim(" (default)")
			}
			set := render.Green("set")
			if os.Getenv(s.URLEnv) == "" {
				set = render.Yellow("NOT SET")
			}
			fmt.Printf("  %-8s $%s [%s]  events: %s\n", s.Type, s.URLEnv, set, ev)
		}
	}
	return nil
}
