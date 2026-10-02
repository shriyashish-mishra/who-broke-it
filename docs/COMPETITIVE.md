# Competitive landscape

_Research pass: October 2026, public web sources only. Product capabilities change quickly; verify before relying on any row. Items I could not verify are marked **unverified**._

## Principle

Do not assume competitors lack parallel agents, worktrees, task assignment, MCP, cloud agents, PR automation or orchestration. They have them. A product that competes on those loses. The question is what is **still missing** once all of that exists.

## What already exists

### Vendor platforms ("mission control")

| Product | What it already does | Source |
|---|---|---|
| **GitHub Agent HQ** (Copilot Pro+/Enterprise) | Runs Copilot, Claude and Codex (public preview) inside GitHub / VS Code; "mission control" to assign, steer and track multiple agents; compare results and merge; context, history and review stay attached to the PR/issue | [GitHub blog](https://github.blog/news-insights/company-news/pick-your-agent-use-claude-and-codex-on-agent-hq/), [i-programmer](https://www.i-programmer.info/news/105-artificial-intelligence/18651-github-adds-claude-and-codex-to-agent-hq.html) |
| **OpenAI Codex** | Cloud tasks: sandbox VM, clones repo, returns a PR; worktrees for parallel tasks | [techsy comparison](https://techsy.io/en/blog/background-coding-agents-compared), [Wikipedia](https://en.wikipedia.org/wiki/OpenAI_Codex_(AI_agent)) |
| **Cursor** (2.x/3) | Up to ~8 parallel agents/subagents in isolated worktrees or remote machines; background agents triggerable from Slack, Linear, GitHub | [Cursor 3 guide](https://baeseokjae.github.io/posts/cursor-3-guide-2026/), [amux comparison](https://amux.io/guides/background-agents-compared/) |
| **Claude Code** | Agent teams with a shared task list; each subagent in its own git worktree | [MindStudio](https://www.mindstudio.ai/blog/claude-code-agent-teams-shared-task-list), [Augment comparison](https://www.augmentcode.com/tools/claude-code-agent-teams-vs-intent) |
| **Devin** | A coordinator Devin splits work into packages, starts managed child Devins in parallel, messages them, compiles results | [morphllm comparison](https://www.morphllm.com/comparisons/devin-vs-cursor) |
| **Augment "Intent"** | Per-prompt "Spaces", each with its own branch and worktree | [Augment](https://www.augmentcode.com/tools/claude-code-agent-teams-vs-intent) |

Common shape: **one vendor's agents (or a few partner agents), orchestrated inside that vendor's surface**, with isolation via worktrees/VMs and review via PRs.

### Open-source coordination projects

| Project | Approach | Source |
|---|---|---|
| **ai-crew-sync** | Coordination bus for Claude Code/Codex/Cursor/any MCP client: messages, tasks, presence, notes, locks; self-hosted, Postgres-backed | [GitHub](https://github.com/joaquinbejar/ai-crew-sync) |
| **COORD-Harness** | Autonomous orchestration over a single SQLite-WAL authority: claim work, bounded-context handoffs, audits, multi-day resume, retrieval over large repos | [GitHub](https://github.com/0marm0/COORD-Harness) |
| **ai-team-sync** | Declared sessions, advisory/exclusive file-scope locks, shared decisions; VS Code, dashboard, CLI, MCP | [GitHub](https://github.com/pvestal/ai-team-sync) |
| **agent-claim-mcp** | Local-first MCP server: agents claim normalized paths via a JSON ledger; three tools, no infra | [GitHub](https://github.com/vk0dev/agent-claim-mcp) |
| **agent-coord** | Machine-wide presence, self-healing file locks, a global git commit guard, messaging, duplicate-work detection | [GitHub](https://github.com/The-skomoroh/agent-coord) |
| **agent-comms** | Agents announce planned edits in shared JSON files; upfront conflict detection and negotiation | [GitHub](https://github.com/LakshmiSravyaVedantham/agent-comms) |
| **Research** | AgentRoom (CRDT-backed shared workspace), MPAC (multi-principal coordination protocol); file-level claims reported to eliminate semantic conflicts from concurrent same-file edits | [AgentRoom](https://arxiv.org/pdf/2608.23740), [MPAC](https://arxiv.org/pdf/2604.09744) |
| Curated list | `awesome-agent-orchestrators` indexes many more | [GitHub](https://github.com/andyrewlee/awesome-agent-orchestrators) |

Not verified in this pass (searches did not return relevant primary sources): **Axis**, **ATC**, **Multea**. They may overlap; check before launch.

## What is crowded

* Claims / locks / presence over MCP (ai-team-sync, agent-claim-mcp, agent-coord, ai-crew-sync).
* Task queues and agent messaging buses.
* Worktree isolation and parallel agent launch (every vendor).
* PR-centric review of agent output.

Building another lock server or message bus here would be re-doing solved work. Who Broke It? includes an intent registry because it is a prerequisite, but it is not the differentiator.

## The actual gap

Almost everything above coordinates **who is touching which files right now**. Very little of it holds a persistent, vendor-neutral model of **what the team is building and how the pieces relate**, and uses that to answer engineering questions:

| Capability | Vendor platforms | OSS coordination tools | **Who Broke It?** |
|---|---|---|---|
| Parallel agents, worktrees | ✓ | partial | uses them, doesn't compete |
| File/path claims & presence | partial | ✓ | ✓ (soft ownership, intent kinds) |
| **Shared requirement → task → contract → file graph in the repo** | ✗ (lives in the vendor UI/issues) | ✗ (flat tasks/locks) | ✓ core primitive |
| **Contract ownership + change propagation to consumers** | ✗ | ✗ | ✓ |
| **Blast radius** (what breaks if X changes, who is affected now) | ✗ | ✗ | ✓ (graph-based, deterministic) |
| **Independent verification** vs scope, contracts, constitution, criteria | partial (tests/CI, reviewer agents) | partial (audits) | ✓ + human gates for risky areas |
| **Product-to-code traceability** (why does this exist?) | ✗ | ✗ | ✓ (`wbi why`, commit trailers) |
| **Drift** (product / architecture / context) | ✗ | ✗ | ✓ foundations |
| **Vendor-neutral, repo-native, no account** | ✗ | partial (often server/Postgres) | ✓ |
| Cross-machine sync | ✓ (hosted) | ✓ (server-based) | ✓ **git-native, no server** (eventually consistent; atomic claims while online; unsigned events) |

So the target differentiation is exactly: **shared engineering understanding + intent + dependency graph + blast radius + verification + product-to-code traceability + vendor-neutral coordination**.

## Risks to the thesis

1. **Vendors move up the stack.** GitHub is best placed to add graph/traceability features on top of Agent HQ. Counter: neutrality across vendors, and the graph being plain files in the repo.
2. **Agent-written plans may make hand-written graphs unnecessary.** Counter: the value is in *enforcement and propagation* over the graph, not in authoring it; the planner is pluggable (`--from`, `--agent-cmd`).
3. **Adoption is cooperative.** If one teammate ignores the protocol, guarantees weaken. Mitigation: hooks (commit trailers), CI enforcement (roadmap), and making the CLI the shortest path for agents.
4. **Sync is table stakes** for real teams. It is built on plain git refs (no infrastructure), which is a strength for adoption but weaker than a server on latency (polling, not push) and on authorization (git permissions only).
