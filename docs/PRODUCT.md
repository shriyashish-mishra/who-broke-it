# Product

## One line

**Who Broke It? is the open-source engineering control plane for the agentic software era.** It turns product intent into a living Engineering Graph and coordinates humans and heterogeneous AI coding agents from planning through implementation, integration and verification.

> The agents write the code. Who Broke It? coordinates the engineering.

## The problem

Teams now run several agents and humans against one repository. Writing code is no longer the bottleneck; coordination is. In practice, someone spends a day on: splitting tasks, deciding who owns what, working out dependencies, setting up each person's agent with the right context, and then — continuously — figuring out what one agent changed that breaks another, and whether "done" is actually done.

Existing tools coordinate *which files are touched right now* (locks, claims) or *run more agents* (vendor mission control). Few maintain a durable shared model of **what is being built and how the pieces relate**.

## Who it is for

* **Primary:** a small team (2–10) building a product with a mix of agents (Claude Code, Codex, Cursor, OpenCode, Antigravity…) in one GitHub repo: hackathon teams, startups, internal tools teams.
* **Secondary:** a single developer running several agents in parallel worktrees who wants them not to collide.
* **Later:** orgs wanting governance (audit, permissions, approvals) over agent-authored change.

## The answer it must give

> **Here's exactly what changed, who changed it, why they changed it, what it affects, and what we should do next.**

`wbi blame` = who + which task + which agent + which PR. `wbi why` = requirement → task → agent → commit. `wbi blast` = what it affects. `wbi status`/`wbi inbox` = what to do next.

## Principles

1. **Repo-native memory.** The graph is JSON/Markdown under `.wbi/`, reviewed in PRs like code. No proprietary hosted memory.
2. **Vendor-neutral.** Any agent that can run a shell command can participate; MCP is an adapter, not the foundation.
3. **Deterministic first.** Overlap detection, DAG validation, blast radius, scope checks and rule enforcement use plain algorithms. LLMs are used for planning (pluggable), not for judging.
4. **Soft ownership, hard gates where it matters.** Most conflicts are warnings; restricted paths, foreign-contract edits and risky areas (migrations, security, public API, infra) block or require a human.
5. **Local-first.** Works with no account, no server, no model subscription.
6. **Fun on the surface, serious underneath.** The joke lives in the CLI copy; the guarantees are real.

## Non-goals

* Not a coding agent or model.
* Not a Jira/Linear clone or a kanban UI.
* Not hard-wired to Claude (or any vendor).
* No SaaS dashboard in the open-source core.
* No fake agent execution: the coordination always acts on real repository state.

## Scope by phase

**Phase 1: vertical slice (built):** init → plan → Engineering Graph → agent registration → claim/start (branch/worktree + work packet) → intent declaration + conflict detection → handoff → git/PR state → `status` → MCP interface.

**Phase 2: depth (built in first form):** blast radius · contract change propagation · verification (Agent Judge) · drift detection · simulation · `blame`/`why` traceability.

**Phase 2.5: team sync (built):** git-native cross-machine sync of claims, intents, handoffs and contract changes, with race-safe claims.

**Phase 3 (next):** sync hardening (compaction, signed events) · CI/pre-commit enforcement · semantic analysis (call graph, OpenAPI/types) · multi-repo graph · integrations (Linear/Jira/Slack/Discord, GitLab/Bitbucket) · hosted collaboration and governance.

## Success looks like

* A new team goes from "we have a goal" to "everyone's agent is working on a scoped, contract-first task" in minutes instead of a day.
* Cross-agent breakages (changed contract, overlapping edits, duplicate work) are surfaced **before** merge, to the people who need to know.
* "Done" means verified; risky changes always have a human sign-off recorded.
* Anyone can ask "why does this code exist?" and get a task, requirement and decision.

## Open questions

* How much of the plan should be LLM-authored vs templated? (Pluggable today; default is a conservative template planner.)
* Is git-ref sync enough, or do teams need push notifications / a relay for latency? (Git refs shipped first; polling on each command.)
* How should enforcement degrade for teammates who don't opt in?
