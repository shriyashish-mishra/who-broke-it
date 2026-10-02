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

type target struct {
	File string
	Wrap func(body string) string
}

// Targets maps an agent name to the instruction file it reads.
var Targets = map[string]target{
	"claude":   {File: "CLAUDE.md"},
	"codex":    {File: "AGENTS.md"},
	"opencode": {File: "AGENTS.md"},
	"gemini":   {File: "GEMINI.md"},
	"cursor": {File: ".cursor/rules/wbi.mdc", Wrap: func(b string) string {
		return "---\ndescription: Team coordination via Who Broke It?\nalwaysApply: true\n---\n" + b
	}},
}

func TargetNames() []string {
	var n []string
	for k := range Targets {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

var blockRe = regexp.MustCompile(`(?s)` + regexp.QuoteMeta(beginMark) + `.*?` + regexp.QuoteMeta(endMark) + `\n?`)

// InstallGuidance inserts or replaces the managed block, preserving everything else in the file.
func InstallGuidance(root, name string) (string, error) {
	t, ok := Targets[name]
	if !ok {
		return "", model.Errf("Unknown adapter %q. Known: %s", name, strings.Join(TargetNames(), ", "))
	}
	path := filepath.Join(root, t.File)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	b, _ := os.ReadFile(path)
	existing := string(b)
	var next string
	switch {
	case strings.Contains(existing, beginMark):
		next = blockRe.ReplaceAllLiteralString(existing, Guidance)
	case t.Wrap != nil && existing == "":
		next = t.Wrap(Guidance)
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
	return t.File, os.WriteFile(path, []byte(next), 0o644)
}

// MCPConfig prints the config snippet that registers `wbi mcp` with an agent.
func MCPConfig(agent, wbiPath string) string {
	if wbiPath == "" {
		wbiPath = "wbi"
		if _, err := exec.LookPath("wbi"); err != nil { // not installed on PATH: point at this binary
			if exe, err := os.Executable(); err == nil {
				wbiPath = exe
			}
		}
	}
	switch agent {
	case "codex":
		return fmt.Sprintf("# ~/.codex/config.toml\n[mcp_servers.wbi]\ncommand = %q\nargs = [\"mcp\"]\nenv = { WBI_AGENT = \"codex\" }\n", wbiPath)
	}
	server := map[string]any{"command": wbiPath, "args": []string{"mcp"}, "env": map[string]string{"WBI_AGENT": agent}}
	js, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"wbi": server}}, "", "  ")
	if agent == "claude" {
		return fmt.Sprintf("# run once:\nclaude mcp add wbi --env WBI_AGENT=claude -- %s mcp\n\n# or .mcp.json:\n%s\n", wbiPath, js)
	}
	return string(js) + "\n"
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
