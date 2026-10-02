# Protocol (v0.1, draft)

A vendor-neutral way for humans and AI agents to coordinate on one repository. It is defined by **files in `.wbi/`**, **a small set of verbs**, and **commit conventions**. MCP and the CLI are two transports for the same verbs.

## Concepts

| Concept | Meaning |
|---|---|
| **Agent** | A coding agent session: `provider@developer[#session]` (e.g. `claude@maya`, `codex@devb`). `human` is a valid provider. |
| **Developer** | The human an agent works for; owner of the task. |
| **Repository** | The git repo that holds `.wbi/`. |
| **Task** | A work packet (see `internal/model/model.go`). |
| **Intent** | A declaration of what an agent is about to change, made before editing. |
| **Ownership** | Soft: tasks own `allowedPaths`; contracts are owned by their provider task. |
| **Handoff** | Structured record of completed work. |
| **Decision** | An ADR. |
| **Contract** | A named interface (type/http/event/function) with a version and a single owning task. |
| **Change** | A contract version bump, with propagation to consumers. |

## Lifecycle

```text
register ─▶ claim ─▶ (start: branch/worktree + packet) ─▶ declare intents ─▶ code + commit
                                                                 │
                         inbox ◀── change notices ◀──────────────┤
                           │ ack                                  ▼
                           └──────────────────────────── handoff ─▶ verify ─▶ DONE | REVIEW | back to IN_PROGRESS
```

## Verbs

| Verb | CLI | MCP tool | Notes |
|---|---|---|---|
| register / heartbeat | `wbi agents register` | `wbi_register_agent` | Implicit on every call |
| list tasks | `wbi tasks` | `wbi_list_tasks` | Status derived from the DAG |
| claim | `wbi claim <id>` | `wbi_claim_task` | Fails with an explanation if BLOCKED or owned |
| work packet | `wbi task <id>` / `wbi start <id>` | `wbi_get_context` | Scope, contracts, rules, criteria, upstream handoffs, stale-contract banner |
| declare intent | `wbi intent declare KIND target --task id` | `wbi_declare_intent` | Returns `{intent, conflicts[], blocked}` |
| list intents | `wbi intent` | `wbi_list_intents` | |
| handoff | `wbi handoff <id> …` | `wbi_submit_handoff` | Also runs verification |
| verify | `wbi verify <id>` | `wbi_verify` | |
| inbox / ack | `wbi inbox` / `wbi ack <id>` | `wbi_inbox` / `wbi_ack_changes` | |
| blast radius | `wbi blast <target>` | `wbi_blast_radius` | |
| status / simulate / drift / blame | `wbi status` … | `wbi_status` … | Read-only |

## Intent

`kind` ∈ `CREATE | MODIFY | DELETE` (target = path or glob), `CHANGE_CONTRACT` (target = contract name), `DEPEND_ON` (target = task id).

Response conflicts: `{code, severity: info|warn|block, message, with?: {agentId, taskId}}`.
Codes: `OUT_OF_SCOPE`, `RESTRICTED_PATH`, `OWNERSHIP_CONFLICT`, `RISKY_PATH`, `OVERLAP`, `DUPLICATE_WORK`, `DELETE_CONFLICT`, `CONTRACT_NOT_OWNED`, `CONTRACT_COLLISION`, `ACTIVE_CONSUMER`, `UNMET_DEPENDENCY`, `UNKNOWN_TASK`, `UNKNOWN_CONTRACT`, `NOT_CLAIMED`.
CLI exit code `2` when blocked. An agent should **stop and adapt** on `block`, and **tell its human** on `warn`.

## Handoff

```json
{
  "taskId": "TASK-007", "agentId": "claude@maya", "at": "2026-10-02T10:00:00Z",
  "implemented": "Billing API v2",
  "changedFiles": ["src/api/billing/status.ts"],
  "contractChanges": [{ "name": "BillingStatus", "version": 2, "note": "now supports paused" }],
  "tests": { "ran": true, "passed": true, "summary": "24 passed" },
  "limitations": ["Refund API not implemented"],
  "attested": [1, 2, 3],
  "affected": ["TASK-008", "TASK-009", "TASK-016"],
  "branch": "wbi/TASK-007", "pr": 482, "commits": ["ed8ccd3 feat(billing): …"]
}
```

`changedFiles` and `commits` come from git, not from the agent. `attested` lists 1-based acceptance criteria without automatic checks that the agent claims to satisfy.

## Commit conventions

Branch: `wbi/TASK-<n>`. Trailers (added by the installed `prepare-commit-msg` hook, or by hand):

```text
WBI-Task: TASK-007
WBI-Agent: claude@maya
```

These make `wbi blame` / `wbi why` exact. Without them, attribution falls back to `Co-Authored-By`, `TASK-nnn` in the message, and `(#PR)`.

## Human-only criteria

A task criterion may carry `"human": true`. An agent cannot attest it (attesting is ignored). The verifier reports `awaiting human sign-off`, the task moves to `REVIEW` (`READY_FOR_REVIEW`), and `wbi approve` is the sign-off. Agents should still hand off normally when their part is done.

## Environment

`WBI_AGENT` (provider; Claude Code is auto-detected from `CLAUDECODE=1` when unset), `WBI_DEVELOPER`, `WBI_SESSION` (to run two sessions of one provider as one developer), `NO_COLOR`, `WBI_NO_GH=1` (skip PR lookups via `gh`).

## Replication (cross-machine)

Coordination state replicates through git; the wire format is part of the protocol so other implementations can interoperate.

* **Ref:** `refs/wbi/sync` (configurable) on the project's remote. Its history is linear (every update is a fast-forward).
* **Commit:** adds one file `log/<actor>/<unix-ms>-<n>.jsonl`. Events are read oldest commit first, lines in file order.
* **Batch signature:** the file is `<header>\n<body>`; header `{"v":1,"actor":"…","key":"<base64 ed25519 public key>","sig":"<base64 signature of the exact body bytes>"}`; body is the event lines. A bad signature MUST be rejected. A file whose first line is not such a header is a legacy unsigned batch, accepted only when the team does not enforce signatures.
* **Team policy:** `.wbi/team.json` `{"version":1,"enforce":bool,"members":[{"name","key"}]}` in the verifier's checkout. With `enforce`, only batches whose key is a member are applied.
* **Compaction:** the ref may be replaced by a single root commit holding a snapshot (same batch format). Readers that find their last-read commit is no longer an ancestor of the tip MUST replay from the root.
* **Event line:** `{"uid":"…","actor":"<8 hex>","ts":<ms>,"tbl":"<table>","row":{…}}`. `tbl` ∈ `task_state | agents | intents | events | notifications | handoffs | contract_state | task_ack | kv`; unknown tables MUST be ignored. `row` is the full row image (snake_case columns) without machine-local columns.
* **Apply rules:** upsert by primary key (`intents`/`events`/`notifications` by `uid`); `contract_state.version` and `task_ack.version` only increase; `notifications`/`events` are insert-or-ignore; `kv` replicates only `plan_approved`.
* **Claim rule:** a `task_state` event that would give a task to agent *B* while the log already shows it `IN_PROGRESS`/`REVIEW` under agent *A* is invalid when it is *pending* (not yet published): the publisher drops it. First writer in log order wins.
* **Publishing:** build a commit on the fetched tip and push it without force. A rejected push means the tip moved: fetch, replay, retry.
* **Text from events is untrusted** and must have control characters stripped before display.

## Versioning

The schema carries `project.version: 1`. Additive changes only within 0.x; breaking changes bump the version and ship a migration command.
