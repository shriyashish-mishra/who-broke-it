# Integrating an agent

Any tool that can run a shell command can follow the [protocol](PROTOCOL.md). Pick the level that fits.

## Level 0: instructions only (works today for every agent)

```bash
wbi adapters install all        # CLAUDE.md, AGENTS.md, GEMINI.md, .cursor/rules/wbi.mdc
```

This inserts a managed block (`<!-- wbi:begin --> … <!-- wbi:end -->`) that teaches the agent the loop: identify → `wbi start` → declare intent → code → `wbi handoff`. Re-running updates the block in place and never touches the rest of the file.

| Agent | File |
|---|---|
| Claude Code | `CLAUDE.md` |
| Codex, OpenCode, generic | `AGENTS.md` |
| Gemini CLI | `GEMINI.md` |
| Cursor | `.cursor/rules/wbi.mdc` |

Set identity per terminal: `export WBI_AGENT=codex WBI_DEVELOPER="Dev B"`.

## Level 1: MCP (tool-native)

`wbi mcp` is a stdio MCP server exposing 18 `wbi_*` tools. Print config for your agent:

```bash
wbi adapters mcp claude    # claude mcp add wbi --env WBI_AGENT=claude -- wbi mcp   (+ .mcp.json)
wbi adapters mcp codex     # ~/.codex/config.toml [mcp_servers.wbi]
wbi adapters mcp gemini    # JSON mcpServers block
wbi adapters mcp cursor
```

Run it from (or point `cwd` at) a directory inside the repo. Tools: `wbi_register_agent`, `wbi_list_tasks`, `wbi_claim_task`, `wbi_start_task`, `wbi_release_task`, `wbi_release_intents`, `wbi_get_context`, `wbi_declare_intent`, `wbi_list_intents`, `wbi_blast_radius`, `wbi_submit_handoff`, `wbi_verify`, `wbi_inbox`, `wbi_ack_changes`, `wbi_status`, `wbi_simulate`, `wbi_drift`, `wbi_blame`. Schemas are served by `tools/list`.

Minimal agent loop over MCP:

1. `wbi_list_tasks {status: "ready"}` → pick one → `wbi_start_task {task_id}` (claims it and creates an isolated worktree under `.wbi/worktrees/`; edit and commit there)
2. `wbi_get_context {task_id}` → follow the packet
3. `wbi_declare_intent {task_id, kind, target}` before touching files; stop if `blocked`
4. work, commit on the task branch
5. `wbi_submit_handoff {task_id, summary, run_tests, contract_changes?, attested_criteria}` → read the verification; fix and resubmit on `FAILED`
6. Periodically `wbi_inbox`; after adapting to a contract change, `wbi_ack_changes`

MCP is only a transport. The server is one small file, `internal/mcp/mcp.go`, over `engine.Engine`.

## Level 2: wrap the CLI in your own agent harness

`status`, `graph`, `blast`, `blame` and `drift` accept `--json`. `wbi start` prints the work packet (also written to `<workdir>/.wbi/state/<TASK>.context.md`) and the `WBI_*` env to export. `wbi intent declare` exits `2` when blocked; `wbi handoff` exits `1` when verification fails, so a harness can loop "work → handoff → fix" mechanically.

```bash
wbi start TASK-007 --agent mybot --as maya --worktree
# … your agent runs in the printed workdir …
wbi handoff TASK-007 --agent mybot --as maya --summary "…" --tests "npm test" --attest 1,2 || retry
```

## Level 3: native adapter in code

Go is the implementation language, so the supported programmatic surface is the CLI with `--json` and the MCP server, both of which wrap the same `engine.Engine`. The engine lives under `internal/` while the API is unstable; it will be promoted to a public package at 1.0. Until then, to extend wbi itself, add a package inside this repo (see [CONTRIBUTING.md](../CONTRIBUTING.md)).

## Teams on separate machines

Nothing changes for agents: the same CLI / MCP calls work. When `wbi sync init` has been committed, every call pulls teammates' state first and publishes writes after, so an agent on Bob's laptop sees Alice's claims, intents, handoffs and contract changes without any extra step. `wbi inbox` and `wbi intent` always refresh. If the network is down, calls still succeed locally and print one warning.

## Commit attribution

`wbi init` installs a `prepare-commit-msg` hook (in the git common dir, so it covers all worktrees) that adds `WBI-Task` from the `wbi/TASK-n` branch name and `WBI-Agent` from `$WBI_AGENT`. It will not overwrite an existing hook of yours; add the same two `git interpret-trailers` lines to it, or add the trailers manually.

## Contributing an adapter

Adapters are small: a guidance file template (`internal/engine/adapters.go: Targets`) and an MCP config snippet. PRs welcome for Aider, OpenCode, Continue, Zed, Windsurf, Copilot agent mode, etc.
