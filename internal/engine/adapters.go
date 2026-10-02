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

// Verification states for an adapter. Be honest: "verified" means a real agent was run through it end to end.
const (
	Verified   = "verified"   // exercised end to end with the real tool (see docs/REAL-AGENTS.md)
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
	Status          string                    // Verified | Documented
	Notes           string
}

// MCPStyle selects the config snippet printed by `wbi adapters mcp <agent>`.
type MCPStyle int

const (
	MCPGeneric   MCPStyle = iota // {"mcpServers": {"wbi": {...}}}: the de-facto standard shape
	MCPClaude                    // `claude mcp add ...` plus .mcp.json
	MCPCodexTOML                 // ~/.codex/config.toml [mcp_servers.wbi]
	MCPNone                      // the tool has no MCP support: use the CLI protocol
)

var registry = []Adapter{
	{Name: "claude", Display: "Claude Code", InstructionFile: "CLAUDE.md", MCP: MCPClaude, Status: Verified,
		Detect: func() bool { return os.Getenv("CLAUDECODE") == "1" },
		Notes:  "Run end to end over MCP and over the CLI protocol (Claude Code 2.1.287)."},
	{Name: "codex", Display: "OpenAI Codex CLI", InstructionFile: "AGENTS.md", MCP: MCPCodexTOML, Status: Documented,
		Notes: "Reads AGENTS.md and supports MCP servers in config.toml. Not run."},
	{Name: "gemini", Display: "Gemini CLI", InstructionFile: "GEMINI.md", MCP: MCPGeneric, Status: Documented,
		Notes: "Reads GEMINI.md; MCP servers go in its settings.json under mcpServers. Not run."},
	{Name: "cursor", Display: "Cursor", InstructionFile: ".cursor/rules/wbi.mdc", MCP: MCPGeneric, Status: Documented,
		Wrap: func(b string) string {
			return "---\ndescription: Team coordination via Who Broke It?\nalwaysApply: true\n---\n" + b
		},
		Notes: "Uses a project rule (.mdc) and .cursor/mcp.json. Not run."},
	{Name: "opencode", Display: "OpenCode", InstructionFile: "AGENTS.md", MCP: MCPGeneric, Status: Documented,
		Notes: "Reads AGENTS.md. Its MCP config has its own shape; check its docs for where the generic snippet goes. Not run."},
	{Name: "aider", Display: "Aider", InstructionFile: "CONVENTIONS.md", MCP: MCPNone, Status: Documented,
		Notes: "No built-in MCP support that we know of (check its docs): use the CLI protocol; load the file with `aider --read CONVENTIONS.md`. Not run."},
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
		out = fmt.Sprintf("# ~/.codex/config.toml\n[mcp_servers.wbi]\ncommand = %q\nargs = [\"mcp\"]\nenv = { WBI_AGENT = %q }\n", wbiPath, agent)
	case MCPNone:
		return fmt.Sprintf("%s has no built-in MCP support that we know of (check its docs). Use the CLI protocol: `wbi adapters install %s` writes the instructions it reads. Status: %s, not tested.\n", a.Display, agent, a.Status)
	default:
		server := map[string]any{"command": wbiPath, "args": []string{"mcp"}, "env": map[string]string{"WBI_AGENT": agent}}
		js, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"wbi": server}}, "", "  ")
		out = string(js) + "\n"
		if style == MCPClaude {
			out = fmt.Sprintf("# run once:\nclaude mcp add wbi --env WBI_AGENT=claude -- %s mcp\n\n# or .mcp.json:\n%s", wbiPath, out)
		}
	}
	if known && a.Status != Verified {
		out += fmt.Sprintf("\n# NOTE: %s support is %q, not tested end to end. %s\n", a.Display, a.Status, a.Notes)
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
[ -n "$WBI_AGENT" ] && git interpret-trailers --in-place --if-exists doNothing --trailer "WBI-Agent: $WBI_AGENT@${WBI_DEVELOPER:-unknown}" "$1"
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return "error: " + err.Error()
	}
	return "installed"
}
