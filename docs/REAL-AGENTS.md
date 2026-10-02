# Validated against real agents

Scripted demos prove the coordination logic; they cannot prove that a real agent can *use* it. This page records what was run with a real agent, what it found, and what is still untested.

## What was run

| Agent | Version | Transport | Result |
|---|---|---|---|
| **Claude Code** | 2.1.287 | MCP (`.mcp.json` from `wbi adapters mcp claude`) | Listed ready tasks, claimed one, read its packet, declared an out-of-scope intent and correctly interpreted the `OUT_OF_SCOPE` / `RISKY_PATH` warnings (7 turns, about $0.11) |
| **Claude Code** | 2.1.287 | CLI, following the `CLAUDE.md` block from `wbi adapters install claude`, **no `WBI_*` env set** | Auto-detected as `claude`, started a worktree, wrote and committed a file, handed off, read the verifier (9 turns, about $0.13) |
| Codex, Gemini CLI, Cursor, OpenCode, Aider | n/a | n/a | **Not tested.** Not installed on the machine this was built on. The adapters for them are written from their documented conventions and should be treated as unverified. |

## What the real agent found (and what changed)

1. **No way to withdraw an intent over MCP.** Claude declared a deliberately out-of-scope intent, then reported *"I have no tool here to withdraw it."* → added `wbi_release_intents`, `wbi_release_task`, and `wbi_start_task` (which also creates the worktree).
2. **Sandboxed agents could not reach the worktree.** `wbi start --worktree` created `../repo-TASK-001`, outside the directory Claude Code was allowed to touch. → worktrees now live in `<repo>/.wbi/worktrees/TASK-n` (git-ignored).
3. **Identity was a papercut.** With no `WBI_AGENT`, the agent had to guess. → Claude Code is auto-detected (`CLAUDECODE=1`); claim errors now say who *you* are and how to act as someone else.
4. **The architecture task's packet said `Contracts: none`.** The task's whole job is to review every contract. → architecture tasks now list all project contracts.
5. **The agent refused to lie to the verifier.** It would not attest "every contract was reviewed by its providers" because nobody had reviewed anything, and the handoff failed. That is the right behavior, but "FAILED" was the wrong outcome for criteria only a person can sign. → criteria can be `human: true`; the agent cannot attest them, the task goes to `REVIEW`, and `wbi approve` is the sign-off.

## Reproduce

```bash
git init demo && cd demo && git commit --allow-empty -m init
wbi init && wbi plan "Build a SaaS app with authentication, billing and analytics"
wbi adapters install claude && git add -A && git commit -m "wbi plan"
wbi adapters mcp claude > .mcp.json      # keep the JSON part
claude -p "Using only the wbi MCP tools, list ready tasks and claim the first" \
  --mcp-config .mcp.json --strict-mcp-config --allowedTools "mcp__wbi__*" --output-format json
```

If you run another agent through wbi, please open an issue with what happened. That is the most valuable contribution at this stage.
