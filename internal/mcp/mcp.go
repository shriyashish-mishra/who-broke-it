// Package mcp is a minimal MCP server (JSON-RPC 2.0, newline-delimited, over stdio) layered over the engine.
// The coordination core is protocol-agnostic; this is just one adapter. No third-party dependencies.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

type Args map[string]any

func (a Args) str(k string) string {
	s, _ := a[k].(string)
	return s
}
func (a Args) boolean(k string) bool { b, _ := a[k].(bool); return b }
func (a Args) strs(k string) []string {
	var out []string
	if l, ok := a[k].([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
func (a Args) ints(k string) []int {
	var out []int
	if l, ok := a[k].([]any); ok {
		for _, x := range l {
			if f, ok := x.(float64); ok {
				out = append(out, int(f))
			}
		}
	}
	return out
}
func (a Args) num(k string) int { f, _ := a[k].(float64); return int(f) }

func (a Args) agent() engine.AgentOpts {
	return engine.AgentOpts{Agent: a.str("agent"), As: a.str("developer")}
}

type Tool struct {
	Name, Description string
	Schema            map[string]any
	Run               func(e *engine.Engine, a Args) (any, error)
}

type M = map[string]any

func obj(props M, required ...string) M {
	if required == nil {
		required = []string{}
	}
	return M{"type": "object", "properties": props, "required": required}
}

var str = M{"type": "string"}
var who = M{
	"agent":     M{"type": "string", "description": "Your tool name (claude, codex, gemini, cursor...). Defaults to $WBI_AGENT."},
	"developer": M{"type": "string", "description": "Human you work for. Defaults to $WBI_DEVELOPER / git user."},
}

func with(base M, extra M) M {
	out := M{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// Tools is the full tool table, in a stable order.
var Tools = []Tool{
	{"wbi_register_agent", "Register this agent (also acts as a heartbeat).", obj(with(who, M{"capabilities": M{"type": "array", "items": str}})),
		func(e *engine.Engine, a Args) (any, error) {
			o := a.agent()
			o.Capabilities = a.strs("capabilities")
			return e.ResolveAgent(o)
		}},
	{"wbi_list_tasks", "List tasks with live status (READY, BLOCKED, IN_PROGRESS, REVIEW, DONE).", obj(M{"status": str}),
		func(e *engine.Engine, a Args) (any, error) {
			st := e.States()
			out := []M{}
			for _, t := range e.Tasks() {
				d := e.Display(t, st)
				if s := a.str("status"); s != "" && !strings.EqualFold(s, d) {
					continue
				}
				var agent any
				if x := st[t.ID].AgentID; x != "" {
					agent = x
				}
				out = append(out, M{"id": t.ID, "title": t.Title, "status": d, "layer": t.Layer, "dependsOn": t.DependsOn, "agent": agent})
			}
			return out, nil
		}},
	{"wbi_claim_task", "Claim a READY task. Fails with an explanation if it is blocked or owned by someone else.", obj(with(who, M{"task_id": str, "force": M{"type": "boolean"}}), "task_id"),
		func(e *engine.Engine, a Args) (any, error) {
			r, err := e.Claim(a.str("task_id"), a.agent(), a.boolean("force"))
			if err != nil {
				return nil, err
			}
			return M{"claimed": r.Task.ID, "agent": r.Agent.ID, "branch": r.State.Branch}, nil
		}},
	{"wbi_get_context", "Get the full work packet for a task: scope, contracts, constitution rules, acceptance criteria, upstream handoffs and pending change notices.", obj(M{"task_id": str}, "task_id"),
		func(e *engine.Engine, a Args) (any, error) { return e.Context(a.str("task_id")) }},
	{"wbi_declare_intent", "Declare what you are about to change BEFORE editing. kind: CREATE|MODIFY|DELETE (target = path/glob), CHANGE_CONTRACT (target = contract name), DEPEND_ON (target = task id). Returns conflicts; if blocked=true, do not proceed.",
		obj(with(who, M{"task_id": str, "kind": M{"type": "string", "enum": []string{"CREATE", "MODIFY", "DELETE", "CHANGE_CONTRACT", "DEPEND_ON"}}, "target": str, "note": str}), "task_id", "kind", "target"),
		func(e *engine.Engine, a Args) (any, error) {
			r, err := e.DeclareIntent(a.agent(), a.str("task_id"), strings.ToUpper(a.str("kind")), a.str("target"), a.str("note"))
			if err != nil {
				return nil, err
			}
			conflicts := r.Conflicts
			if conflicts == nil {
				conflicts = []model.Conflict{}
			}
			return M{"intent": r.Intent, "conflicts": conflicts, "blocked": r.Blocked}, nil
		}},
	{"wbi_release_intents", "Withdraw your active intents for a task (e.g. you declared one by mistake or changed plan).", obj(M{"task_id": str}, "task_id"),
		func(e *engine.Engine, a Args) (any, error) {
			if _, err := e.Task(a.str("task_id")); err != nil {
				return nil, err
			}
			e.ReleaseIntents(strings.ToUpper(a.str("task_id")))
			return M{"released": strings.ToUpper(a.str("task_id"))}, nil
		}},
	{"wbi_release_task", "Give a task back (unclaim it) so someone else can take it.", obj(M{"task_id": str}, "task_id"),
		func(e *engine.Engine, a Args) (any, error) {
			if err := e.Release(a.str("task_id")); err != nil {
				return nil, err
			}
			return M{"released": strings.ToUpper(a.str("task_id"))}, nil
		}},
	{"wbi_start_task", "Claim a task if needed and prepare its isolated git worktree (default true). Returns the branch, workdir and work packet. Do your edits and commits inside workdir.",
		obj(with(who, M{"task_id": str, "worktree": M{"type": "boolean"}, "force": M{"type": "boolean"}}), "task_id"),
		func(e *engine.Engine, a Args) (any, error) {
			wt := true
			if v, ok := a["worktree"].(bool); ok {
				wt = v
			}
			r, err := e.Start(a.str("task_id"), a.agent(), wt, a.boolean("force"))
			if err != nil {
				return nil, err
			}
			return M{"task": r.Task.ID, "agent": r.Agent.ID, "branch": r.Branch, "workdir": r.Workdir, "packet_file": r.ContextFile, "packet": r.Context}, nil
		}},
	{"wbi_list_intents", "List active intents of all agents.", obj(M{}),
		func(e *engine.Engine, a Args) (any, error) {
			if i := e.Intents(false); i != nil {
				return i, nil
			}
			return []model.Intent{}, nil
		}},
	{"wbi_blast_radius", "What is affected if a contract, task or path changes?", obj(M{"target": str}, "target"),
		func(e *engine.Engine, a Args) (any, error) { return e.Blast(a.str("target")), nil }},
	{"wbi_submit_handoff", "Hand off a finished task. Records changed files (from git), runs optional tests, records contract changes (propagated to affected tasks), then runs verification. Done is decided by the verifier.",
		obj(with(who, M{
			"task_id": str, "summary": str, "run_tests": M{"type": "string", "description": "Shell command to run for test evidence"},
			"tests_passed": M{"type": "boolean"}, "tests_summary": str,
			"contract_changes":  M{"type": "array", "items": str, "description": `"Name" or "Name=what changed"`},
			"limitations":       M{"type": "array", "items": str},
			"attested_criteria": M{"type": "array", "items": M{"type": "number"}, "description": "1-based acceptance criteria you satisfied that have no automatic check"},
			"pr":                M{"type": "number"},
		}), "task_id", "summary"),
		func(e *engine.Engine, a Args) (any, error) {
			o := engine.HandoffOpts{AgentOpts: a.agent(), Summary: a.str("summary"), RunTests: a.str("run_tests"), TestsSummary: a.str("tests_summary"),
				Contracts: a.strs("contract_changes"), Limitations: a.strs("limitations"), Attest: a.ints("attested_criteria"), PR: a.num("pr")}
			if v, ok := a["tests_passed"].(bool); ok {
				o.TestsPassed = &v
			}
			return e.SubmitHandoff(a.str("task_id"), o)
		}},
	{"wbi_verify", "Run the verifier on a handed-off task.", obj(M{"task_id": str}, "task_id"),
		func(e *engine.Engine, a Args) (any, error) { return e.VerifyTask(a.str("task_id")) }},
	{"wbi_inbox", "Notifications for you: contract changes affecting your task, handoffs from upstream, verification failures.", obj(with(who, M{"mark_read": M{"type": "boolean"}})),
		func(e *engine.Engine, a Args) (any, error) {
			ag, err := e.ResolveAgent(a.agent())
			if err != nil {
				return nil, err
			}
			out := []M{}
			for _, n := range e.Inbox(ag.ID, a.boolean("mark_read")) {
				out = append(out, M{"id": n.ID, "ts": n.TS, "taskId": n.TaskID, "message": n.Msg})
			}
			return out, nil
		}},
	{"wbi_ack_changes", "Acknowledge contract changes for a task after adapting to them.", obj(M{"task_id": str}, "task_id"),
		func(e *engine.Engine, a Args) (any, error) {
			if err := e.Ack(a.str("task_id")); err != nil {
				return nil, err
			}
			return M{"acked": a.str("task_id")}, nil
		}},
	{"wbi_status", "Project mission control: progress, active/ready/blocked tasks, attention items.", obj(M{}),
		func(e *engine.Engine, a Args) (any, error) { return e.Status(), nil }},
	{"wbi_simulate", "Execution plan: waves, critical path, conflicts, missing dependencies.", obj(M{"agents": M{"type": "number"}}),
		func(e *engine.Engine, a Args) (any, error) { return e.Simulate(a.num("agents")), nil }},
	{"wbi_drift", "Product, architecture and context drift report.", obj(M{}),
		func(e *engine.Engine, a Args) (any, error) { return e.Drift(), nil }},
	{"wbi_blame", "Who changed this path, under which task, and what does it affect?", obj(M{"path": str}, "path"),
		func(e *engine.Engine, a Args) (any, error) { return e.Blame(a.str("path"), 5), nil }},
}

// mutating tools publish their effect to teammates; the rest just refresh from them first.
var mutating = map[string]bool{"wbi_release_intents": true, "wbi_release_task": true, "wbi_start_task": true, "wbi_register_agent": true, "wbi_claim_task": true, "wbi_declare_intent": true, "wbi_submit_handoff": true, "wbi_verify": true, "wbi_ack_changes": true}

// CallTool runs a tool and returns its text output; errors become isError results, never protocol errors.
// When cross-machine sync is enabled the call is wrapped: pull first, publish after writes.
func CallTool(e *engine.Engine, name string, args Args) (text string, isError bool) {
	for _, t := range Tools {
		if t.Name != name {
			continue
		}
		var out any
		run := func() (err error) {
			out, err = t.Run(e, args)
			return err
		}
		var err error
		if mutating[name] {
			err = e.Synced(true, run)
		} else {
			age := 3 * time.Second
			if name == "wbi_inbox" || name == "wbi_list_intents" {
				age = 0 // coordination reads must never be stale
			}
			e.PullIfStale(age)
			err = run()
		}
		if err != nil {
			return err.Error(), true
		}
		if s, ok := out.(string); ok {
			return s, false
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err.Error(), true
		}
		return string(b), false
	}
	return "Unknown tool " + name, true
}

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		ProtocolVersion string `json:"protocolVersion"`
		Name            string `json:"name"`
		Arguments       Args   `json:"arguments"`
	} `json:"params"`
}

// Handle processes one JSON-RPC message; it returns nil for notifications.
func Handle(e *engine.Engine, line []byte) any {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return M{"jsonrpc": "2.0", "id": nil, "error": M{"code": -32700, "message": "Parse error"}}
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return nil
	}
	reply := func(result any) any { return M{"jsonrpc": "2.0", "id": req.ID, "result": result} }
	switch req.Method {
	case "initialize":
		v := req.Params.ProtocolVersion
		if v == "" {
			v = "2025-06-18"
		}
		return reply(M{"protocolVersion": v, "capabilities": M{"tools": M{}}, "serverInfo": M{"name": "who-broke-it", "version": "0.1.0"}})
	case "ping":
		return reply(M{})
	case "tools/list":
		tools := []M{}
		for _, t := range Tools {
			tools = append(tools, M{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
		}
		return reply(M{"tools": tools})
	case "tools/call":
		text, isErr := CallTool(e, req.Params.Name, req.Params.Arguments)
		return reply(M{"content": []M{{"type": "text", "text": text}}, "isError": isErr})
	}
	return M{"jsonrpc": "2.0", "id": req.ID, "error": M{"code": -32601, "message": fmt.Sprintf("Method not found: %s", req.Method)}}
}

// Serve reads newline-delimited JSON-RPC from r and writes responses to w until EOF.
func Serve(e *engine.Engine, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if res := Handle(e, []byte(line)); res != nil {
			if err := enc.Encode(res); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}
