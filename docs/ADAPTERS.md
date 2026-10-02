# Agent adapters

An adapter tells wbi three things about a coding agent: **which file it reads for project instructions**, **how to register `wbi mcp` with it**, and (optionally) **how to recognize it from its environment**. Everything else (claiming, intents, handoffs, verification, sync) is agent-agnostic and lives in the core.

## Status (read this)

| Agent | Instruction file | MCP | Status |
|---|---|---|---|
| **Claude Code** | `CLAUDE.md` | `claude mcp add` / `.mcp.json` | ✅ **Verified**: run end to end over MCP and the CLI protocol ([REAL-AGENTS.md](REAL-AGENTS.md)); auto-detected via `CLAUDECODE=1` |
| OpenAI Codex CLI | `AGENTS.md` | `~/.codex/config.toml` | 🔶 Integration target. Written from its documented conventions. **Not run.** |
| Gemini CLI | `GEMINI.md` | `settings.json` → `mcpServers` | 🔶 Integration target. **Not run.** |
| Cursor | `.cursor/rules/wbi.mdc` | `.cursor/mcp.json` | 🔶 Integration target. **Not run.** |
| OpenCode | `AGENTS.md` | own config shape | 🔶 Integration target. **Not run.** |
| Aider | `CONVENTIONS.md` | none known (CLI protocol) | 🔶 Integration target. **Not run.** |

`wbi adapters list` prints the same table from the registry, and `wbi adapters mcp <agent>` appends an "untested" note for anything not verified. A test enforces that exactly the verified agents are marked verified, so this page and the code cannot drift apart silently.

"Integration target" means: the architecture supports it and the file formats follow the tool's documentation, but nobody has run that agent through wbi. Any agent that can run shell commands can already follow the CLI protocol with `WBI_AGENT=<name>`; the adapter only makes setup one command.

## Adding an agent

1. Add one entry to `registry` in [`internal/engine/adapters.go`](../internal/engine/adapters.go):

   ```go
   {Name: "myagent", Display: "My Agent", InstructionFile: "MYAGENT.md", MCP: MCPGeneric, Status: Documented,
       Notes: "Reads MYAGENT.md; MCP via its mcp.json. Not run."},
   ```

   Optional fields: `Wrap` (front matter for a brand-new instruction file), `Detect` (recognize the tool from its own env vars so no `WBI_AGENT` is needed).
2. Add a row to the table above.
3. That is all: `wbi adapters install myagent`, `wbi adapters mcp myagent`, `wbi adapters list` and the generic adapter tests pick it up.

## Promoting an adapter to "verified"

Run the agent through the protocol for real and record what happened in [REAL-AGENTS.md](REAL-AGENTS.md): what you ran, what it did, what broke. Then flip `Status` to `Verified` and update the count in `TestAdapterRegistryIsHonestAndConsistent`. Please include the tool's version. Real runs have always found something: Claude Code's first runs found five issues that scripted tests never would.

## What an agent must be able to do

Run shell commands (or speak MCP), read a file, and commit to git. That is the entire contract. See [PROTOCOL.md](PROTOCOL.md).
