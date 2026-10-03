# Validated against real agents

Scripted demos prove the coordination logic; they cannot prove that a real agent can *use* it. This page records what was run with a real agent, what it found, and what is still untested.

## What was run

| Agent | Version | Transport | Result |
|---|---|---|---|
| **Claude Code** | 2.1.287 | MCP (`.mcp.json` from `wbi adapters mcp claude`) | Listed ready tasks, claimed one, read its packet, declared an out-of-scope intent and correctly interpreted the `OUT_OF_SCOPE` / `RISKY_PATH` warnings (7 turns, about $0.11) |
| **Claude Code** | 2.1.287 | CLI, following the `CLAUDE.md` block from `wbi adapters install claude`, **no `WBI_*` env set** | Auto-detected as `claude`, started a worktree, wrote and committed a file, handed off, read the verifier (9 turns, about $0.13) |

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

---

# Round 2: five agents (2026-10-03)

Same task for every agent, defined in [`scripts/agent-harness/`](../scripts/agent-harness/): a fresh git repo with a one-task plan (*create `src/hello.js` exporting `hello()` returning `"hello"`, with a test*), the tool's own instruction file installed by `wbi adapters install`, and a prompt asking it to follow the protocol. **A run only counts if wbi's database and git agree**: task `DONE`, an isolated worktree, the code in the worktree and *not* on main, intents declared before editing, commits carrying `WBI-Task`/`WBI-Agent` trailers, and a handoff record. What the agent said about itself was not evidence.

| Agent (version) | CLI protocol | MCP | Notes |
|---|---|---|---|
| **OpenAI Codex CLI** (codex-cli 0.160.0) | ✅ after one setting | ✅ after two settings | Needs `.git` in `sandbox_workspace_write.writable_roots` and, for MCP, `default_tools_approval_mode = "approve"` |
| **Cursor Agent** (2026.10.01) | ✅ | ✅ | `cursor-agent -p --force`; MCP also `--approve-mcps` |
| **OpenCode** (v2.0.22) | ✅ | ✅ | `opencode run "..."`; MCP via `opencode.json` |
| **Google Antigravity CLI** (`agy` 1.2.16) | ✅ | ✅ | `--dangerously-skip-permissions`; reads `GEMINI.md`; `agy mcp add` is global-only |

## What the runs found

1. **Codex's sandbox breaks the protocol until you allow `.git`.** By default (`workspace-write`) `.git` is read-only, so `git worktree add`, `git commit` and wbi's state database (kept in `.git/wbi/`) all fail. Codex did the right thing: it changed nothing and reported exactly why ("`unable to open database file`… the sandbox makes `.git` read-only"). With `-c 'sandbox_workspace_write.writable_roots=["<repo>/.git"]'` the whole flow passed. This is documented in the adapter's notes and printed by `wbi adapters mcp codex`.
2. **Codex over MCP also needs tool approval.** Non-interactive Codex blocks MCP calls that need approval ("approval policy is never"). `default_tools_approval_mode = "approve"` on the wbi server fixes it. (My first MCP attempt also hung because `codex exec` waited on stdin; closing stdin fixed it. That was my harness, not wbi.)
3. **Attribution gap over MCP (fixed).** An MCP-driven agent's identity lives in the MCP server's environment, not in the shell where it runs `git commit`, so those commits had `WBI-Task` but no `WBI-Agent`. The commit hook now asks `wbi executor <task>` when `WBI_AGENT` is unset. Run `wbi hook` to refresh an existing repo's hook.
4. **Test evidence was polluted by runtime warnings (fixed).** Several handoffs recorded Node's "NO_COLOR is ignored due to FORCE_COLOR" warning as the *test summary*. wbi now filters that class of line out of command output before summarising it.
5. **Identity is whatever the agent says.** Over MCP, Antigravity and Cursor each chose their own agent/developer names in tool calls (one took its name from the wording of the instruction file). wbi trusts the agent's declaration (as it must); the field is a label, not authentication.
6. **A failing test command is caught.** In one run a handoff used a wrong path for `--tests` and the Judge sent the task back to `IN_PROGRESS` ("`✗ Tests`"), as designed. Note `--tests` runs *inside the worktree*.

## Caveats on this evidence

- **One task, one run per transport per agent** (plus reruns noted above). This shows the protocol is workable with each tool, not that it is reliable across tasks, models or versions.
- Models differ run to run, so a second run could behave differently.
- Everything ran on macOS only, in a temp repo, with permissions relaxed as each tool's headless mode requires. Real teams will use stricter policies; item 1 shows what that can change.
- These tools update often. The versions above are the only ones tested.
