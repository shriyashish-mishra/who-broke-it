# Who Broke It?

### The coordination layer for AI-native engineering teams. One repo. Humans. AI agents. Zero excuses.

> **The agents write the code. Who Broke It? coordinates the engineering.**

Your team now has Claude Code, Codex, Gemini CLI, Cursor and a few humans all committing to one repo. The agents are good at writing code. Nobody is good at answering:

*Who owns this? What depends on it? What did that agent just change that breaks mine? Is it actually done?*

`wbi` keeps a **living Engineering Graph** in your repo (goal → requirements → components → tasks → contracts → agents → intents → files → commits → PRs) and uses it to coordinate any mix of humans and AI agents. It does not write code, it is not a Jira clone, and it is not tied to any vendor.

```text
$ wbi blame src/api/billing/

Last significant changes:

🤖 Claude   TASK-007           1s ago  feat(billing): status endpoint, add paused  [wbi/TASK-007, unmerged]
🧑 Maya                        3s ago  initial commit

⚠ Contract BillingStatus changed by TASK-007
→ 3 downstream task(s) affected: TASK-008, TASK-009, TASK-016
```

## 30-second explanation

1. **`wbi plan "<goal>"`** inspects your repo and produces a task graph: requirements, components, contracts (the interfaces tasks share), dependencies, allowed/restricted paths, acceptance criteria, risks.
2. **Each task is a work packet.** `wbi start TASK-007 --worktree` gives an agent its own branch/worktree and everything it needs: scope, contracts, constitution rules, upstream handoffs. No rediscovering the project.
3. **Agents declare intent before editing.** `wbi intent declare MODIFY src/api/billing/**` is checked against other agents, task ownership and contract ownership *before* code is written. Not after the merge conflict.
4. **Handoffs are structured, and "done" is verified.** `wbi handoff` records what changed, tests and contract changes, notifies affected tasks, then the Judge checks scope, contracts, constitution, tests and acceptance criteria. Agents don't get to say DONE.
5. **Everything is traceable.** `wbi blame`, `wbi why`, `wbi blast`, `wbi drift` answer: what changed, who/which agent, under which task and requirement, what it affects.

## Quickstart

```bash
git clone https://github.com/shriyashish-mishra/who-broke-it.git && cd who-broke-it && make install      # builds a single static binary and copies it to your PATH
# or: go install ./cmd/wbi

cd your-repo                 # any git repo with at least one commit
wbi init
wbi plan "Build a multi-tenant SaaS dashboard with authentication, billing, analytics and an AI assistant"
wbi simulate --approve       # review waves / critical path / conflicts, then approve
git add .wbi && git commit -m "chore: engineering graph"

# a human or agent picks up work (identify with --agent/--as or WBI_AGENT/WBI_DEVELOPER)
wbi status
wbi start TASK-001 --agent claude --as maya --worktree
```

Try the whole story on a throwaway repo, offline, in about 15 seconds:

```bash
make demo           # one machine, three agents
make demo-team      # two machines sharing only a git remote
```

> **Honesty note on the demo:** every `wbi` action in it is real (git repo, branches, worktrees, commits, SQLite state, verification). The *coding* is a scripted commit per agent turn, because the demo must run without anyone's AI subscription. Swap any step for a real agent session and the coordination is identical.

Requires only git at runtime. `wbi` is one static binary (pure Go, no cgo: `make cross` builds Linux/macOS/Windows from any machine). Building needs Go ≥ 1.26. The only third-party dependency is a pure-Go SQLite driver (`modernc.org/sqlite`).

## Mission control

```text
$ wbi status

WHO BROKE IT?

PROJECT: healthai
Build a multi-tenant SaaS dashboard with authentication, billing, analytics and an AI assistant.

Progress: ██████░░░░░░░░░░░░░░ 31%  (4/16 tasks)

ACTIVE
● Gemini   → TASK-003 Web app shell  [1 commit]
● Gemini   → TASK-008 Billing UI
● Codex    → TASK-010 Analytics API

READY
○ TASK-013 AI assistant API

BLOCKED
○ TASK-005 Authentication UI
  waiting for TASK-003
  … and 7 more (wbi tasks --status BLOCKED)

ATTENTION
⚠ BillingStatus changed (v2, by TASK-007) → 2 downstream task(s) affected: TASK-008, TASK-009
```

## Teams on separate machines

Your teammates are on their own laptops with their own Claude / Codex / Gemini accounts. `wbi` shares coordination state through the git remote you already use. No server, no account, nothing to host:

```bash
wbi sync init                       # once; writes .wbi/project.json
git add -A && git commit -m "chore: enable wbi sync" && git push
# teammates just `git pull`. Every wbi command now syncs automatically.
```

* **What is shared:** claims and task status, agents, intents, handoffs, contract changes, inbox notices and acknowledgements.
* **How:** each clone keeps a local outbox (SQLite triggers capture changes, so the engine does not know about sync). `wbi sync` appends it as a commit on `refs/wbi/sync` and replays everyone else's. A git push to a ref is an atomic compare-and-swap, so that ref's history is **one linear log with one agreed order**.
* **Races:** if two people claim the same task at the same moment, exactly one push wins. The other machine learns it lost (`⚠ TASK-003: codex@bob lost the claim to claude@alice`), drops the claim and shows the owner. The order is the remote's, not anyone's clock.
* **Offline:** everything keeps working locally and queues; changes publish when you are back online. A claim made offline can still lose a race later, and you are told.
* **Automatic:** mutating commands pull first and publish after; `status`/`tasks`/etc. refresh at most every 3 s; `inbox` and `intent` always refresh. `WBI_NO_SYNC=1` turns it off.
* **Hosts that block custom refs:** `wbi sync init --ref refs/heads/wbi-sync` uses an ordinary branch instead (verified; it just shows up as a branch).

```bash
examples/demo-two-machines.sh       # two clones + a bare remote: handoff, race, intents, contract change
```

## What it does

| Capability | Command | How it works (deterministic, no LLM required) |
|---|---|---|
| Engineering Graph | `wbi plan`, `wbi graph` | JSON entities under `.wbi/`, validated as a DAG (cycles, unknown deps, consumers without providers) |
| Work packets | `wbi start`, `wbi task` | Scope + contracts + constitution rules + upstream handoffs + pending change notices |
| Intent registry | `wbi intent declare` | Glob-overlap analysis vs active intents, task ownership (soft), restricted paths, contract ownership |
| Contract propagation | `wbi handoff --contract "X=note"` | Bumps version, finds consumers via the graph, notifies owners, marks their context stale |
| Blast radius | `wbi blast <contract\|task\|path>` | Explicit relationships: contracts, task/component deps, path ownership, active agents |
| Agent Judge | `wbi verify` (auto on handoff) | Scope, contracts, forbid-rules on the diff, test evidence, acceptance criteria; human approval for migrations/security/public API |
| Blame / why | `wbi blame`, `wbi why` | Commit trailers (`WBI-Task`, `WBI-Agent`) → task → requirement → ADR; includes **unmerged** branches |
| Drift | `wbi drift` | Product (requirement × layer matrix), architecture (constitution forbid-rules, incl. unmerged branches), context (stale contract versions) |
| Simulation | `wbi simulate` | Waves, critical path, parallelizable tasks, overlapping scopes, missing deps, approval gate |
| Cross-machine sync | `wbi sync` (automatic once enabled) | Git-native: an append-only event log on `refs/wbi/sync`; atomic claims via push compare-and-swap |
| MCP | `wbi mcp` | 15 `wbi_*` tools over stdio; same engine as the CLI |

## Architecture

```text
 Claude Code     Codex       Gemini CLI     Cursor     custom agent        humans
      │            │             │            │             │                │
      └────────────┴──────┬──────┴────────────┴─────────────┘                │
            MCP (wbi mcp) │  or plain CLI/ JSON (wbi …)                       │
                          ▼                                                   ▼
              ┌──────────────────────────────  Engine  ───────────────────────────────┐
              │  claim · start · intents · handoff · verify · blast · drift · simulate │
              └───────────────┬───────────────────────────────────┬────────────────────┘
        committed, reviewable │                                   │ runtime, local
                              ▼                                   ▼
        .wbi/  (the Engineering Graph)                 <git-common-dir>/wbi/state.db  (SQLite, WAL)
        project.json  constitution.md                  agents · task_state · intents · events
        tasks/ contracts/ components/                  notifications · handoffs · acks
        requirements/ decisions/ handoffs/             shared by every worktree, never committed
                              │
                              ▼
                   git: branches · worktrees · commits (trailers) · PRs
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), [docs/PROTOCOL.md](docs/PROTOCOL.md) and [docs/PRODUCT.md](docs/PRODUCT.md).

## Using it with your agents

* **CLI works with every agent.** Any tool that can run shell commands can follow the protocol. `wbi adapters install all` writes a managed block into `CLAUDE.md`, `AGENTS.md` (Codex/OpenCode), `GEMINI.md` and `.cursor/rules/wbi.mdc`.
* **MCP for tool-native use.** `wbi adapters mcp claude|codex|gemini|cursor` prints the config for your agent.
* **Commit attribution.** `wbi init` installs a `prepare-commit-msg` hook that stamps `WBI-Task` / `WBI-Agent` trailers from the branch name and `$WBI_AGENT`.
* **Write your own adapter** in a few lines: see [docs/INTEGRATING.md](docs/INTEGRATING.md).

## Why not just use X?

Parallel agents, worktrees, task assignment, MCP, cloud agents, PR automation and file-claim servers all exist already (GitHub Agent HQ, Cursor, Codex, Devin, agent-claim-mcp, ai-team-sync, ai-crew-sync, COORD-Harness…). Who Broke It? does **not** compete on those. The gap it targets is the *shared engineering understanding* above them: intent + dependency graph + contract propagation + blast radius + verification + product-to-code traceability, vendor-neutral and living in your repo. Details and sources: [docs/COMPETITIVE.md](docs/COMPETITIVE.md).

## What works today, and what does not (read this)

Works and is tested (`go test ./...`, 38 tests: unit, end-to-end on real git repos, and two-clone sync scenarios including a deterministic fetch→push race; plus both demos): everything in the table above.

Known limits of this prototype:

* **Sync is eventually consistent and trusts your teammates.** Anyone with push access to the remote can write events (the same trust as pushing code); incoming text is sanitized, unknown event types are ignored. There is no per-user authorization inside the log yet.
* **Claims are only atomic while you are online.** Offline claims are optimistic and can lose when you reconnect (you are told, and the claim is dropped).
* **The log grows without bound.** There is no compaction yet; it is small (one short JSON line per change) but not free.
* **Hosts:** verified against GitHub (two clones racing for the same task six times in a row: always exactly one winner, both clones converged; a claim round-trip takes about 4 s there) and against plain bare git remotes. GitLab, Bitbucket and self-hosted setups are untested; if one blocks `refs/wbi/*`, use the branch fallback above.
* **Intent is cooperative.** Agents must call it (the instruction files and MCP make that easy); it is not enforced at the filesystem level. CI/pre-commit enforcement is planned.
* **The built-in planner is template-based** (auth, billing, analytics, assistant, notifications, mobile, multi-tenant, plus a generic fallback). It inspects your repo for stack, layout, test command and relevant files, but it is not an architect. For LLM-authored plans use `wbi plan --prompt` (prints a prompt + JSON schema for any agent), or `--agent-cmd "<cmd>"` (pipes the prompt to your agent CLI and ingests its JSON), or `--from plan.json`.
* **Blast radius is graph-based**, not semantic code analysis: it only knows relationships that are declared in the graph (contracts, dependencies, component and path ownership).
* **Acceptance criteria** are verified automatically only when they carry a `check` (command / file-exists / contains); otherwise the agent must attest and the human reviews.
* Single repository per graph. The schema is multi-repo-ready; propagation across repos is not implemented.

## Roadmap

1. **Sync hardening:** log compaction, signed events / per-user authorization, push-notification wake-ups instead of polling.
2. **Enforcement:** a GitHub Action / pre-commit check that fails PRs which leave scope, change contracts without a handoff, or violate the constitution.
3. **Smarter analysis:** import/call-graph based blast radius (tree-sitter), drift checks against OpenAPI specs and typed schemas.
4. **Adapters:** Linear/Jira/Slack/Discord notifications, GitLab/Bitbucket, first-class PR state.
5. **Multi-repo graph** with cross-repo contract propagation.
6. **Hosted collaboration** (team dashboard, org permissions, audit logs, SSO), built on the same protocol.

## Development

```bash
make test         # go vet + go test (unit + end-to-end on real temp git repos)
make demo
```

See [CONTRIBUTING.md](CONTRIBUTING.md). MIT licensed.
