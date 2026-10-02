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

## Install

```bash
# macOS / Linux: verified download of the latest release
curl -fsSL https://raw.githubusercontent.com/shriyashish-mishra/who-broke-it/main/install.sh | sh

brew install shriyashish-mishra/tap/wbi                                        # Homebrew
go install github.com/shriyashish-mishra/who-broke-it/cmd/wbi@latest           # Go 1.26+
# Windows: download wbi_<version>_windows_amd64.zip from the Releases page
# From source: git clone https://github.com/shriyashish-mishra/who-broke-it && cd who-broke-it && make install
```

Every release ships six archives (Linux, macOS, Windows × amd64/arm64) with a `checksums.txt`; the installer refuses to install on a checksum mismatch.

## Quickstart

```bash
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
* **Live mode:** `wbi sync watch` polls every few seconds in a spare terminal and prints what teammates did.
* **Trust:** batches are signed per clone; `wbi team join --name you --enforce` + a reviewed PR to `.wbi/team.json` limits who is applied. `wbi sync compact` keeps the log small.
* **Hosts that block custom refs:** `wbi sync init --ref refs/heads/wbi-sync` uses an ordinary branch instead (verified; it just shows up as a branch).

```bash
examples/demo-two-machines.sh       # two clones + a bare remote: handoff, race, intents, contract change
```

## Enforce it in CI

Agents are *asked* to follow the rules; CI makes them *required*. `wbi check` needs only the repo (no runtime state), so it runs on a fresh checkout and fails a PR that:

* touches files **outside its task's allowed paths** (or inside restricted ones),
* **changes a contract it does not own**, changes a contract's shape **without a version bump**, or bumps a version **without a handoff record**,
* adds code that violates a **constitution `forbid` rule**,
* breaks the plan itself (cycles, unknown dependencies),
* is **high-impact** (migration, security, public API, infra) and has no human approval (opt-in).

```yaml
# .github/workflows/wbi.yml
name: wbi
on: [pull_request]
jobs:
  check:
    runs-on: ubuntu-latest        # Linux or macOS runners
    steps:
      - uses: actions/checkout@v7
        with: { fetch-depth: 0 }  # wbi diffs against the base branch
      - uses: shriyashish-mishra/who-broke-it@v0.1.1
        with:
          require-handoff: true
          require-approval: true  # high-impact tasks need an approving review
```

**See it live:** [wbi-action-demo](https://github.com/shriyashish-mishra/wbi-action-demo) has real open PRs: one clean (passes), one that edits restricted billing code and bypasses `BillingService` (fails), one that bumps a contract it doesn't own (fails), and one high-impact migration waiting for human approval (fails until approved). Each shows inline annotations and a job summary.

The PR's task comes from the branch name (`wbi/TASK-7`), a `WBI-Task:` commit trailer, or `--task`. Findings appear as inline annotations. Locally: `wbi check --base main`.

## What it does

| Capability | Command | How it works (deterministic, no LLM required) |
|---|---|---|
| Engineering Graph | `wbi plan`, `wbi graph` | JSON entities under `.wbi/`, validated as a DAG (cycles, unknown deps, consumers without providers) |
| Work packets | `wbi start`, `wbi task` | Scope + contracts + constitution rules + upstream handoffs + pending change notices |
| Intent registry | `wbi intent declare` | Glob-overlap analysis vs active intents, task ownership (soft), restricted paths, contract ownership |
| Contract propagation | `wbi handoff --contract "X=note"` | Bumps version, finds consumers via the graph, notifies owners, marks their context stale |
| Blast radius | `wbi blast <contract\|task\|path>` | Declared relationships (contracts, task/component deps, path ownership, active agents) **plus a static import scan** (JS/TS, Python, Go) that finds code depending on the change |
| Agent Judge | `wbi verify` (auto on handoff) | Scope, contracts, forbid-rules on the diff, test evidence, acceptance criteria; human approval for migrations/security/public API |
| Blame / why | `wbi blame`, `wbi why` | Commit trailers (`WBI-Task`, `WBI-Agent`) → task → requirement → ADR; includes **unmerged** branches |
| Drift | `wbi drift` | Product (requirement × layer matrix), architecture (constitution forbid-rules, incl. unmerged branches), context (stale contract versions) |
| Simulation | `wbi simulate` | Waves, critical path, parallelizable tasks, overlapping scopes, missing deps, approval gate |
| Pull requests | `wbi pr <task>` | Opens a PR (via `gh`) whose body is built from the handoff: requirement, files changed (from git), contract changes, verification, acceptance, affected tasks, labels (`high-impact`, `contract-change`) |
| Merge gate | `wbi check`, GitHub Action | Diff vs. the task's scope, contract ownership/versioning, constitution, plan integrity; optional handoff + human-approval gates |
| Cross-machine sync | `wbi sync` (automatic once enabled), `watch`, `compact`, `wbi team` | Git-native: signed, append-only event log on `refs/wbi/sync`; atomic claims via push compare-and-swap; team allow-list; snapshot compaction |
| MCP | `wbi mcp` | 18 `wbi_*` tools over stdio; same engine as the CLI |

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

**Real agents:** validated end to end with Claude Code over both MCP and the CLI protocol ([docs/REAL-AGENTS.md](docs/REAL-AGENTS.md): what it found and what changed). **Codex, Gemini CLI, Cursor, OpenCode and Aider are untested.** Their adapters follow each tool's documented conventions but have not been run.

Works and is tested (`go test ./...`, 38 tests: unit, end-to-end on real git repos, and two-clone sync scenarios including a deterministic fetch→push race; plus both demos): everything in the table above.

Known limits of this prototype:

* **Sync is eventually consistent and trusts *team members* with each other.** Every published batch is ed25519-signed; a forged or tampered batch is always rejected, and with `wbi team join --enforce` only keys listed in `.wbi/team.json` (changed via PR) are applied. That protects against outsiders and forgery. It does **not** stop an authorized member from publishing events about someone else's task: there is no per-object permission.
* **Claims are only atomic while you are online.** Offline claims are optimistic and can lose when you reconnect (you are told, and the claim is dropped).
* **Log growth:** `wbi sync compact` replaces the history with one snapshot (guarded by a lease, so it cannot clobber a concurrent push). Run it occasionally; nothing runs it for you.
* **Hosts:** verified against GitHub (two clones racing for the same task six times in a row: always exactly one winner, both clones converged; a claim round-trip takes about 4 s there) and against plain bare git remotes. GitLab, Bitbucket and self-hosted setups are untested; if one blocks `refs/wbi/*`, use the branch fallback above.
* **Intent is cooperative at edit time.** Agents must call it (the instruction files and MCP make that easy). The merge gate (`wbi check`) is what enforces the rules, after the fact, on the PR.
* **The built-in planner is template-based** (auth, billing, analytics, assistant, notifications, mobile, multi-tenant, plus a generic fallback). It inspects your repo for stack, layout, test command and relevant files, but it is not an architect. For LLM-authored plans use `wbi plan --prompt` (prints a prompt + JSON schema for any agent), or `--agent-cmd "<cmd>"` (pipes the prompt to your agent CLI and ingests its JSON), or `--from plan.json`.
* **Blast radius is declared relationships plus a best-effort static import scan** (JS/TS, Python, Go). It does not follow tsconfig/webpack aliases, computed dynamic imports, reflection, generated code, or other languages. It surfaces *undeclared coupling* (code in one task's area importing another's with no dependency edge) in `wbi blast`, `wbi drift` and `wbi check`.
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
