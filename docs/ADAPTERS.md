# Agent adapters

An adapter tells wbi three things about a coding agent: **which file it reads for project instructions**, **how to register `wbi mcp` with it**, and (optionally) **how to recognize it from its environment**. Everything else (claiming, intents, handoffs, verification, sync) is agent-agnostic and lives in the core.

## Status (read this)

"Verified" means a real agent was run through wbi end to end and judged from wbi's and git's recorded state, not from the agent's own report. Evidence, versions and findings: [REAL-AGENTS.md](REAL-AGENTS.md). Run `wbi adapters list` for the same table from the registry.

| Agent | Instruction file | Transports | Status | Required setup |
|---|---|---|---|---|
| **Claude Code** | `CLAUDE.md` | CLI, MCP | ✅ verified (2.1.287) | none; auto-detected via `CLAUDECODE=1` |
| **OpenAI Codex CLI** | `AGENTS.md` | CLI, MCP | ✅ verified (0.160.0) | **`.git` in `sandbox_workspace_write.writable_roots`**; for MCP also `default_tools_approval_mode = "approve"` |
| **Cursor Agent** | `.cursor/rules/wbi.mdc` | CLI, MCP | ✅ verified (2026.10.01) | `cursor-agent -p --force`; MCP: `.cursor/mcp.json` + `--approve-mcps` |
| **OpenCode** | `AGENTS.md` | CLI, MCP | ✅ verified (v2.0.22) | MCP: `opencode.json` in the repo |
| **Google Antigravity** (`agy`) | `GEMINI.md` | CLI, MCP | ✅ verified (1.2.16) | `--dangerously-skip-permissions` headless; `agy mcp add` edits global config |
| **Aider** | `CONVENTIONS.md` | human-driven only | 🔶 partial (0.86.2) | a person runs `wbi`; Aider edits inside the worktree. It cannot run the protocol itself (no shell). |
| **Gemini CLI** | `GEMINI.md` | CLI, MCP | ◌ not run | Google rejected the test account; Antigravity reads the same file |

A test pins this exact list, so this page and the code cannot drift apart silently. `wbi adapters mcp <agent>` prints the config *with* the setup notes above.

Tool vendors change flags and sandboxes often; these versions are the only ones tested. If something differs for you, please [file a report](https://github.com/shriyashish-mishra/who-broke-it/issues/new?template=agent_report.yml).

## Adding an agent

1. Add one entry to `registry` in [`internal/engine/adapters.go`](../internal/engine/adapters.go):

   ```go
   {Name: "myagent", Display: "My Agent", InstructionFile: "MYAGENT.md", MCP: MCPGeneric, Status: Documented,
       Notes: "Reads MYAGENT.md; MCP via its mcp.json. Not run."},
   ```

   Required for anything beyond `documented`: `Tested` (the exact version you ran) and `Notes` (settings it needed). Optional fields: `Wrap` (front matter for a brand-new instruction file), `Detect` (recognize the tool from its own env vars so no `WBI_AGENT` is needed).
2. Add a row to the table above.
3. That is all: `wbi adapters install myagent`, `wbi adapters mcp myagent`, `wbi adapters list` and the generic adapter tests pick it up.

## Promoting an adapter to "verified"

Run the agent through the protocol for real with [`scripts/agent-harness/`](../scripts/agent-harness/) (both transports if it has MCP) and record what happened in [REAL-AGENTS.md](REAL-AGENTS.md): what you ran, what it did, what broke. Then set `Status`, `Tested` and `Notes`, and update the pinned list in `TestAdapterRegistryIsHonestAndConsistent`. Please include the tool's version. Real runs have always found something: Claude Code's first runs found five issues that scripted tests never would.

## What an agent must be able to do

Run shell commands (or speak MCP), read a file, and commit to git. That is the entire contract. See [PROTOCOL.md](PROTOCOL.md).
