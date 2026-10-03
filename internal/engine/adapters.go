package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/shriyashish-mishra/who-broke-it/internal/gitx"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

const (
	beginMark = "<!-- wbi:begin -->"
	endMark   = "<!-- wbi:end -->"
)

// Guidance is the managed block written into each agent's instruction file.
var Guidance = strings.ReplaceAll(beginMark+`
## Team coordination (Who Broke It?)

This repo is coordinated by **wbi**. Humans and several AI agents work here at the same time. Follow this protocol:

1. Identify yourself: ¤export WBI_AGENT=<your-tool> WBI_DEVELOPER=<human>¤ (or pass ¤--agent¤/¤--as¤).
2. Work only on a claimed task: ¤wbi tasks¤, then ¤wbi start <TASK-ID> --worktree¤. The printed work packet is your source of truth (scope, contracts, acceptance criteria, upstream handoffs).
3. Before editing, declare intent: ¤wbi intent declare MODIFY <path-or-glob> --task <TASK-ID>¤. Heed BLOCK/WARN output. Changing a shared contract needs ¤CHANGE_CONTRACT <name>¤.
4. Stay inside the task's allowed paths. Obey ¤.wbi/constitution.md¤.
5. Commit on the task branch (¤wbi/<TASK-ID>¤); the installed hook adds ¤WBI-Task¤/¤WBI-Agent¤ trailers.
6. When finished: ¤wbi handoff <TASK-ID> --summary "..." --tests "<test cmd>" --contract "Name=what changed" --attest 1,2¤. "Done" is decided by ¤wbi verify¤, not by you.
7. Check ¤wbi inbox¤ for contract changes that affect you, then adapt and ¤wbi ack <TASK-ID>¤.

If your tool supports MCP, run ¤wbi mcp¤ as an MCP server and use the ¤wbi_*¤ tools instead of the CLI.
`+endMark+"\n", "¤", "`")

// Verification states for an adapter. Be honest: "verified" means a real agent was run through it end to end
// and the result was checked from wbi's and git's recorded state, not from the agent's own report.
const (
	Verified   = "verified"   // run end to end with the real tool (see docs/REAL-AGENTS.md)
	Partial    = "partial"    // works, but only in a reduced mode described in Notes
	Documented = "documented" // written from the tool's documented conventions; NOT run
)

// Adapter is everything wbi needs to know about one coding agent. Adding an agent is adding one entry to
// the registry below (plus a row in docs/ADAPTERS.md); nothing else in the core changes.
type Adapter struct {
	Name            string // value of --agent / WBI_AGENT
	Display         string
	InstructionFile string                    // file the agent reads for project instructions (repo-relative)
	Wrap            func(block string) string // optional: wrap a brand-new instruction file (e.g. Cursor front matter)
	MCP             MCPStyle                  // how this tool is told about `wbi mcp`
	Detect          func() bool               // optional: recognize the tool from its own environment variables
	Status          string                    // Verified | Partial | Documented
	Tested          string                    // tool + version that was actually run ("" if none)
	Notes           string                    // setup requirements and caveats learned from real runs
}

// MCPStyle selects the config snippet printed by `wbi adapters mcp <agent>`.
type MCPStyle int

const (
	MCPGeneric   MCPStyle = iota // {"mcpServers": {"wbi": {...}}}: the de-facto standard shape
	MCPClaude                    // `claude mcp add ...` plus .mcp.json
	MCPCodexTOML                 // ~/.codex/config.toml [mcp_servers.wbi]
	MCPOpenCode                  // opencode.json {"mcp": {"wbi": {"type": "local", ...}}}
	MCPAgy                       // `agy mcp add ...` (global config)
	MCPNone                      // the tool has no MCP support: use the CLI protocol
)

var registry = []Adapter{
	{Name: "claude", Display: "Claude Code", InstructionFile: "CLAUDE.md", MCP: MCPClaude, Status: Verified, Tested: "Claude Code 2.1.287",
		Detect: func() bool { return os.Getenv("CLAUDECODE") == "1" },
		Notes:  "CLI protocol and MCP both run end to end. Auto-detected via CLAUDECODE=1."},
	{Name: "codex", Display: "OpenAI Codex CLI", InstructionFile: "AGENTS.md", MCP: MCPCodexTOML, Status: Verified, Tested: "codex-cli 0.160.0",
		Notes: "CLI protocol and MCP both run end to end. Two settings are REQUIRED: (1) Codex's workspace-write sandbox makes .git read-only, which breaks git worktrees, commits and wbi's state database, so add the repo's .git to sandbox_workspace_write.writable_roots; (2) non-interactive Codex blocks MCP tool calls that need approval, so set default_tools_approval_mode = \"approve\" on the wbi server. Without (1) Codex stops and reports the failure; it does not work around it."},
	{Name: "antigravity", Display: "Google Antigravity CLI (agy)", InstructionFile: "GEMINI.md", MCP: MCPAgy, Status: Verified, Tested: "agy 1.2.16",
		Notes: "CLI protocol and MCP both run end to end (run with --dangerously-skip-permissions for headless use). It reads GEMINI.md. `agy mcp add` edits your GLOBAL Antigravity config (no project scope); remove it with `agy mcp remove wbi`. It reported its own agent name as 'gemini' when the instructions said gemini, so identity is whatever the agent chooses."},
	{Name: "cursor", Display: "Cursor Agent", InstructionFile: ".cursor/rules/wbi.mdc", MCP: MCPGeneric, Status: Verified, Tested: "cursor-agent 2026.10.01",
		Wrap: func(b string) string {
			return "---\ndescription: Team coordination via Who Broke It?\nalwaysApply: true\n---\n" + b
		},
		Notes: "CLI protocol and MCP both run end to end (headless: cursor-agent -p --force; MCP also needs --approve-mcps and a .cursor/mcp.json)."},
	{Name: "opencode", Display: "OpenCode", InstructionFile: "AGENTS.md", MCP: MCPOpenCode, Status: Verified, Tested: "opencode v2.0.22",
		Notes: "CLI protocol and MCP both run end to end (opencode run \"...\"; MCP via opencode.json in the repo)."},
	{Name: "aider", Display: "Aider", InstructionFile: "CONVENTIONS.md", MCP: MCPNone, Status: Partial, Tested: "aider 0.86.2",
		Notes: "Aider edits files but does not run shell commands on its own, so it cannot drive the protocol: asked to, it wrote straight into the main checkout. Works in HUMAN-DRIVEN mode: a person runs `wbi start --worktree` and `wbi intent`, runs aider inside the worktree (its commits get the wbi trailers), then runs `wbi handoff`. Verified in that mode only."},
	{Name: "gemini", Display: "Gemini CLI", InstructionFile: "GEMINI.md", MCP: MCPGeneric, Status: Documented,
		Notes: "Not run: Google rejected the test account for Gemini CLI (\"migrate to Antigravity\"). The Antigravity adapter above reads the same GEMINI.md and is verified."},
}

// Adapters returns the registry (stable order).
func Adapters() []Adapter { return append([]Adapter{}, registry...) }

// LookupAdapter finds an adapter by name.
func LookupAdapter(name string) (Adapter, bool) {
	for _, a := range registry {
		if a.Name == strings.ToLower(name) {
			return a, true
		}
	}
	return Adapter{}, false
}

// DetectProvider recognizes agents that announce themselves in the environment (empty if none).
func DetectProvider() string {
	for _, a := range registry {
		if a.Detect != nil && a.Detect() {
			return a.Name
		}
	}
	return ""
}

func TargetNames() []string {
	var n []string
	for _, a := range registry {
		n = append(n, a.Name)
	}
	sort.Strings(n)
	return n
}

var blockRe = regexp.MustCompile(`(?s)` + regexp.QuoteMeta(beginMark) + `.*?` + regexp.QuoteMeta(endMark) + `\n?`)

// InstallGuidance inserts or replaces the managed block, preserving everything else in the file.
func InstallGuidance(root, name string) (string, error) {
	a, ok := LookupAdapter(name)
	if !ok {
		return "", model.Errf("Unknown adapter %q. Known: %s", name, strings.Join(TargetNames(), ", "))
	}
	path := filepath.Join(root, a.InstructionFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	b, _ := os.ReadFile(path)
	existing := string(b)
	var next string
	switch {
	case strings.Contains(existing, beginMark):
		next = blockRe.ReplaceAllLiteralString(existing, Guidance)
	case a.Wrap != nil && existing == "":
		next = a.Wrap(Guidance)
	default:
		next = existing
		if existing != "" {
			if !strings.HasSuffix(existing, "\n") {
				next += "\n"
			}
			next += "\n"
		}
		next += Guidance
	}
	return a.InstructionFile, os.WriteFile(path, []byte(next), 0o644)
}

// MCPConfig prints the config snippet that registers `wbi mcp` with an agent. For tools that are not
// Verified it says so: the snippet is the generic shape, not something proven to work for that tool.
func MCPConfig(agent, wbiPath string) string {
	if wbiPath == "" {
		wbiPath = "wbi"
		if _, err := exec.LookPath("wbi"); err != nil { // not installed on PATH: point at this binary
			if exe, err := os.Executable(); err == nil {
				wbiPath = exe
			}
		}
	}
	a, known := LookupAdapter(agent)
	style := MCPGeneric
	if known {
		style = a.MCP
	}
	var out string
	switch style {
	case MCPCodexTOML:
		out = fmt.Sprintf("# ~/.codex/config.toml\n[mcp_servers.wbi]\ncommand = %q\nargs = [\"mcp\"]\ndefault_tools_approval_mode = \"approve\"   # required for non-interactive runs\nenv = { WBI_AGENT = %q }\n\n# and for sandboxed runs (workspace-write), let Codex write the repo's .git:\n# [sandbox_workspace_write]\n# writable_roots = [\"/path/to/your/repo/.git\"]\n", wbiPath, agent)
	case MCPOpenCode:
		js, _ := json.MarshalIndent(map[string]any{"$schema": "https://opencode.ai/config.json", "mcp": map[string]any{"wbi": map[string]any{"type": "local", "command": []string{wbiPath, "mcp"}, "enabled": true, "environment": map[string]string{"WBI_AGENT": agent}}}}, "", "  ")
		out = "# opencode.json in your repo\n" + string(js) + "\n"
	case MCPAgy:
		out = fmt.Sprintf("# edits your GLOBAL Antigravity config; undo with: agy mcp remove wbi\nagy mcp add -e WBI_AGENT=%s wbi -- %s mcp\n", agent, wbiPath)
	case MCPNone:
		return fmt.Sprintf("%s has no built-in MCP support that we know of (check its docs). Use the CLI protocol: `wbi adapters install %s` writes the instructions it reads. Status: %s.\n# %s\n", a.Display, agent, a.Status, a.Notes)
	default:
		server := map[string]any{"command": wbiPath, "args": []string{"mcp"}, "env": map[string]string{"WBI_AGENT": agent}}
		js, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"wbi": server}}, "", "  ")
		out = string(js) + "\n"
		if style == MCPClaude {
			out = fmt.Sprintf("# run once:\nclaude mcp add wbi --env WBI_AGENT=claude -- %s mcp\n\n# or .mcp.json:\n%s", wbiPath, out)
		}
	}
	if known {
		if a.Status == Verified {
			out += fmt.Sprintf("\n# Verified with %s. Setup notes: %s\n", a.Tested, a.Notes)
		} else {
			out += fmt.Sprintf("\n# NOTE: %s support is %q, not fully tested. %s\n", a.Display, a.Status, a.Notes)
		}
	}
	return out
}

// InstallHook installs a prepare-commit-msg hook (in the git common dir, so all worktrees share it)
// that stamps WBI-Task / WBI-Agent trailers. It never overwrites a foreign hook.
func InstallHook(root string) string {
	common := gitx.CommonDir(root)
	if common == "" {
		return "no-git"
	}
	path := filepath.Join(common, "hooks", "prepare-commit-msg")
	if b, err := os.ReadFile(path); err == nil && !strings.Contains(string(b), "wbi-hook") {
		return "exists"
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	script := `#!/bin/sh
# wbi-hook: adds WBI-Task / WBI-Agent trailers so commits are attributable
BRANCH=$(git symbolic-ref --short HEAD 2>/dev/null)
TASK=$(echo "$BRANCH" | sed -n 's#^wbi/\(TASK-[0-9]*\)$#\1#p')
[ -n "$TASK" ] && git interpret-trailers --in-place --if-exists doNothing --trailer "WBI-Task: $TASK" "$1"
# Agents driven over MCP have their identity in the MCP server's environment, not in the shell that runs
# git commit; ask wbi who holds the task instead.
AGENT="$WBI_AGENT@${WBI_DEVELOPER:-unknown}"
[ -z "$WBI_AGENT" ] && [ -n "$TASK" ] && AGENT=$(wbi executor "$TASK" 2>/dev/null)
[ -n "$AGENT" ] && [ "$AGENT" != "@unknown" ] && git interpret-trailers --in-place --if-exists doNothing --trailer "WBI-Agent: $AGENT" "$1"
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return "error: " + err.Error()
	}
	return "installed"
}
