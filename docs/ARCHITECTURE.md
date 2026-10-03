# Architecture

Simplest thing that supports the thesis: **Go (one static binary, no cgo), git, SQLite via the pure-Go `modernc.org/sqlite` driver.** No server, no account.

## State: committed, runtime, and replicated

| | Committed (`.wbi/`) | Runtime (`<git-common-dir>/wbi/state.db`) | Replicated log (`refs/wbi/sync`) |
|---|---|---|---|
| What | The Engineering Graph: project, constitution, requirements, components, tasks, contracts, decisions, handoffs | Who is doing what *right now*: agents, claims/task status, intents, events, notifications, acks, plus the sync outbox | The runtime facts above, shared between machines |
| Format | JSON (+ Markdown) | SQLite, WAL mode, `busy_timeout` | One JSONL file per push, in a linear commit history |
| Reviewed in PRs | yes | no, never committed | no (separate ref, not a branch) |
| Shared by | everyone via git | every worktree on one machine (the git *common* dir) | every clone, via the remote |

The plan and contracts should be reviewable and durable; presence and claims are ephemeral and need atomic multi-process writes (several agents calling `wbi` at once), which SQLite gives for free. Cross-machine sharing is layered underneath without touching the engine.

## Cross-machine sync (`internal/replicate`, `internal/state/replication.go`)

```text
 engine writes ─▶ SQLite tables ─▶ AFTER triggers ─▶ outbox (pending events)
                                                         │  wbi sync
   fetch refs/wbi/sync ─▶ replay others' events (log order) ─▶ re-apply pending on top ─▶ push (compare-and-swap)
                                   │                                    │ rejected? someone pushed first:
                                   └────────────── refetch, replay, retry (claims that lost are dropped)
```

* **Capture without touching the engine.** Triggers on `task_state`, `agents`, `intents`, `events`, `notifications`, `handoffs`, `contract_state`, `task_ack` and `kv.plan_approved` copy each change into `outbox` as a row-image event. A flag in `sync_ctl` silences the triggers while remote events are applied, so nothing echoes. Machine-local columns (`task_state.worktree`, `notifications.read`) never replicate; agent heartbeats are throttled to one event a minute.
* **A single agreed order.** Each sync adds one commit (one file `log/<actor>/<ms>-<n>.jsonl`) on top of the current tip of `refs/wbi/sync`, built with git plumbing (`hash-object`, a throwaway index, `write-tree`, `commit-tree`), so the user's branches, index and working tree are never touched. The push is a compare-and-swap on that ref: if it was updated since we fetched, git rejects it and we refetch, replay and retry. Commit order is therefore a total order every machine agrees on, with no clocks involved.
* **Merging.** Remote events are applied in log order with idempotent per-table rules (upserts by key or uid; contract versions and acks only move forward; notifications/events insert-or-ignore). Then this machine's *pending* events are replayed on top. A pending claim is dropped if the log already gave that task to someone else (first writer wins), the outbox row is marked `rejected`, the claimant is told, and the agent record is reset so the correction is published too.
* **Offline.** Git failures surface as `ErrOffline`; the command still succeeds locally with a one-line warning and the outbox keeps accumulating.
* **Config.** `project.json: {"sync": {"remote": "...", "ref": "..."}}` is committed, so teammates get sync by pulling. `WBI_SYNC_REMOTE` overrides the remote per machine; `WBI_NO_SYNC=1` disables.
* **Signed batches and the team policy** (`internal/replicate/trust.go`). Each published file is `header\nbody`: the header carries the author's ed25519 public key and a signature over the body. A batch with a bad signature is *always* dropped and reported. With `enforce: true` in `.wbi/team.json`, a batch is applied only if its key is listed (and unsigned batches are dropped). The policy file's hash is remembered; when it changes the next sync re-verifies the **whole** log, so adding a member applies their earlier events and removing one stops applying theirs.
* **Compaction** (`wbi sync compact`). Sync first, read the tip, build one orphan snapshot commit of the materialized state (active intents, the latest events/notifications, everything else), and push it with `--force-with-lease=<ref>:<tip>`. If anyone published meanwhile the lease fails and compaction restarts on top of their changes. Other clones see a non-ancestor tip, replay the snapshot, and re-apply their own pending events on top.
* **Watch** (`wbi sync watch`) is a polling loop over the same sync.
* **Trust and safety.** Event text is stripped of control characters before it can reach a terminal; unknown tables are ignored; SQL uses parameters only. Authors are not authenticated beyond git's own push permissions.

## The Engineering Graph

```text
Project ─┬─ Requirement (REQ-n, expected layers)
         ├─ Component   (paths, dependsOn components)
         ├─ Contract    (name, kind, version, providedBy task, shape, public, history[])
         ├─ Decision    (ADR-n, linked tasks)
         └─ Task (TASK-n)
              ├─ requirements[]  → Requirement
              ├─ component       → Component
              ├─ dependsOn[]     → Task               (DAG)
              ├─ provides[]/consumes[] → Contract      (ownership + dependency edges)
              ├─ allowedPaths / restrictedPaths / relevantFiles
              ├─ acceptance[]    {text, check?}        (command | file-exists | contains)
              ├─ impact[]        migration|security|public-api|infra  (human gate)
              └─ contractVersions{}                    (baseline for context-drift)
Runtime:  Agent ── claims ──▶ Task ── declares ──▶ Intent(kind,target)
Git:      Task ─ branch wbi/TASK-n ─ commits (WBI-Task/WBI-Agent trailers) ─ PR
Handoff:  Task → {implemented, changedFiles, contractChanges, tests, limitations, affected}
```

Go definitions: [`internal/model/model.go`](../internal/model/model.go) (the JSON field names are the schema). Every entity is one JSON file, e.g. `.wbi/tasks/TASK-007.json`, `.wbi/contracts/BillingStatus.json` (names are sanitized for the filename; `GET /api/billing` → `GET_api_billing.json`).

### Task status

Stored: `TODO · IN_PROGRESS · REVIEW · DONE`. Derived for display: `TODO` becomes `READY` if all dependencies are `DONE`, else `BLOCKED`. Verification can move a task `REVIEW → DONE` (no human gate), `REVIEW` (needs approval), or back to `IN_PROGRESS` (failed).

## Modules

| Package | Responsibility |
|---|---|
| `internal/model` | Graph schema (JSON-tagged structs) |
| `internal/store` | Read/write `.wbi/`; finds the graph from any worktree |
| `internal/state` | SQLite runtime state (WAL, busy timeout); `replication.go`: outbox triggers, event apply/merge rules |
| `internal/replicate` | Git-backed event log: fetch/plumbing commit/compare-and-swap push, retry on race |
| `internal/graph` | DAG validation, cycle detection, waves, critical path, dependents/ancestors, ready/blocked |
| `internal/glob` | Glob matching and conservative glob-vs-glob overlap |
| `internal/rules` | Constitution parsing, scoping to tasks, forbid-rule checks on diffs and the working tree |
| `internal/planner` | Repo inspection, template planner, plan parsing/validation, agent prompt, doc rendering |
| `internal/intent` | Conflict detection (pure function) |
| `internal/blast` | Blast radius (pure function; optionally given the code graph) |
| `internal/codegraph` | Static import graph for JS/TS, Python, Go |
| `internal/engine` | `engine.go`: agents, claims, work packets, worktrees, contract versions/staleness, intents, inbox, approvals. `judge.go`: handoff + verification. `insight.go`: status, simulation, drift, blame, why. `sync.go`: sync config and auto-sync wrappers. `ops.go`: init/plan. `adapters.go`: agent instruction files, MCP config, commit-trailer hook |
| `internal/mcp` | MCP adapter (JSON-RPC over stdio), thin wrapper over `engine.Engine` |
| `internal/render` | Terminal rendering |
| `cmd/wbi` | CLI |
| `internal/gitx`, `internal/testutil` | git wrapper; real-temp-repo test helpers |

The core (`Engine`, pure analyzers) knows nothing about MCP or any vendor. MCP and the CLI are two adapters over the same functions.

## Key algorithms

**Intent conflicts** (`internal/intent`). For path intents: (1) inside the task's allowed paths? else `OUT_OF_SCOPE` (warn) or `RESTRICTED_PATH` (block); (2) soft owner = another task whose allowed paths overlap; if its agent is active → `OWNERSHIP_CONFLICT` (warn); (3) migration/infra paths → `RISKY_PATH` (warn; verification will need a human); (4) overlap with other agents' active intents → `OVERLAP` (warn), `DUPLICATE_WORK` (CREATE/CREATE, block), `DELETE_CONFLICT` (block). For `CHANGE_CONTRACT`: not the provider → block; another active change → block; active consumers → warn. For `DEPEND_ON`: unknown → block; not DONE → warn. Blocked intents are not registered.

**Glob overlap** (`internal/glob`). Literal vs glob: precise match, or directory prefix. Glob vs glob: static-prefix containment (conservative: may report overlap that cannot occur, never misses one). A planner test asserts that no two concurrent tasks in generated plans have overlapping allowed paths.

**Blast radius** (`internal/blast`). Origin = provider of a contract / the task / path owners. Direct = consumers of the provided contracts (+ direct dependents for a task, + relevant-file listers for a path). Indirect = transitive task dependents and components that depend on affected components. Active agents = affected tasks currently `IN_PROGRESS`/`REVIEW`. Risk: `HIGH` if already-DONE tasks are affected, or (active agent and (public contract or ≥3 affected)), or (public and ≥3 affected); `MEDIUM` if anything is affected; else `LOW`. Reasons are returned with the score.

**Code graph** (`internal/codegraph`). Regex scan of import statements (JS/TS incl. `export … from`, `require`, dynamic `import()`; Python absolute/relative `import`/`from`; Go `import` of module-local packages, which depend on every non-test file of the package), resolved to repo files; unresolved imports are ignored. `blast` uses it to add the owners of importing files (direct importers → direct, transitive → indirect); `UndeclaredCoupling` reports importer-task → imported-task pairs with no dependency path, surfaced by `wbi drift` and as the `UNDECLARED_COUPLING` warning of `wbi check`.

**Contract propagation** (`engine/judge.go`). On handoff with `--contract "Name=note"`: bump version (file + `contract_state`), compute blast, notify the owner of each affected task with the reason ("consumes X" / "depends on TASK-n"), emit a `contract_changed` event. Tasks whose acknowledged version is behind show as context drift and get a banner in their work packet until `wbi ack`.

**Verification** (`engine/judge.go`). Checks: implementation (non-empty diff), scope (allowed/restricted), contracts (only the provider changes a contract; not built on stale versions), dependencies, constitution (forbid rules on the added lines), tests (evidence from the handoff), acceptance criteria (auto-checks run in the task workdir; others require attestation). Any ✗ → `FAILED` and the task returns to `IN_PROGRESS` with a notification. All ✓ → `DONE`, or `REVIEW` ("READY FOR REVIEW") when the task has any `impact` tag.

**Attribution** (`engine/insight.go`). `git log --branches --source` over the path (so *unmerged agent branches are included*), then parse `WBI-Task` / `WBI-Agent` trailers, `Co-Authored-By` (recognizes common agent names), `TASK-nnn` in messages, and `(#123)` PR numbers.

## Worktrees and identity

`wbi start --worktree` creates `.wbi/worktrees/TASK-n` inside the repo (git-ignored, so sandboxed agents that may only touch the repo directory can reach it) on branch `wbi/TASK-n` from the base branch. The graph is found from any worktree (walk up for `.wbi/`, falling back to the main worktree). Agent identity is `provider@developer[#session]`, from flags or `WBI_AGENT` / `WBI_DEVELOPER` / `WBI_SESSION`; every call heartbeats the agent. Handoff records and contract bumps are written into the task's own checkout and committed on the task branch, so they ride with the PR.

## Security notes

* `--tests` and `check: {type: command}` execute shell commands from the plan/handoff in the task workdir. Treat `.wbi/` like code: review plan PRs, and do not run `wbi verify` on untrusted forks.
* The MCP server is stdio only; it trusts its parent process and exposes no network listener.
* The state DB is local, in `.git/`, and holds no secrets.
* The default constitution forbids obvious hard-coded secrets in task diffs (best-effort regex).

## Hosted-ready

The protocol objects (agent, task, intent, handoff, contract change) are plain JSON, and the replicated log is already an ordered event stream. A future cloud service can mirror `.wbi/` and the event stream, add org/permissions/SSO/audit and a dashboard, and the local CLI remains fully functional offline.
