# Agent harness

The exact setup used to run real coding agents through wbi (results: [docs/REAL-AGENTS.md](../../docs/REAL-AGENTS.md)).

```bash
export WBI=$(command -v wbi)
R=$(scripts/agent-harness/mkrepo.sh codex)            # fresh repo + graph + that agent's instruction file
cd "$R" && export WBI_AGENT=codex WBI_DEVELOPER=Maya
<run your agent headlessly with scripts/agent-harness/prompt-cli.txt>   # or prompt-mcp.txt over MCP
scripts/agent-harness/verify.sh "$R"                  # judge from recorded state, not the agent's claim
```

Success means: task `DONE`; a worktree under `.wbi/worktrees/TASK-001`; `src/hello.js` there and **not** on main; commits carry `WBI-Task` / `WBI-Agent` trailers; intents were declared (events). Per-agent invocations and required settings are in REAL-AGENTS.md.
