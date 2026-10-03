# Changelog

All notable changes. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow [SemVer](https://semver.org/) (pre-1.0: minor versions may change behaviour, and the file formats in `.wbi/` are kept backward compatible).

## [Unreleased]

### Added
- Verified sync on **gitlab.com** (17/17 checks, same as GitHub); `scripts/verify-sync.sh` is now host-neutral.
- Adapter verification for Codex CLI, Cursor Agent, OpenCode and Antigravity (see `docs/REAL-AGENTS.md`), the `wbi executor` command used by the commit hook, and `scripts/agent-harness/`.
- Website redesigned: a brand-only hero, then a 35-second clearly-labelled-simulated incident investigation that teaches `wbi status`, `wbi task` and `wbi blame`, followed by the full product sections. Source for the 9:16 launch video lives in `marketing/video/`.

### Fixed
- Commits made by agents driven over MCP now get a `WBI-Agent` trailer (the hook asks wbi who holds the task).
- Test summaries no longer capture runtime warnings (e.g. Node's NO_COLOR/FORCE_COLOR notice).

### Added
- **Multi-repo links** (foundation): `wbi link add|remove|list|impact`. A task consumes `<repo>:<Contract>`; cross-repo drift reuses the banner / status / `wbi ack` flow, `wbi blast <repo>:<Contract>` shows impact in the consumer, and `wbi link impact <Contract>` shows consumers in linked repos. One hop per repo; local checkouts; committed graphs only.

## [0.2.0] - 2026-10-02

### Added
- `wbi pr <task>`: opens a pull request whose body is built from the task's handoff (requirement, files from git, contract changes, verification, acceptance criteria, affected tasks) and creates the `high-impact` / `contract-change` labels on demand.
- **Code-level blast radius**: a static import scan (JS/TS, Python, Go) adds the code that imports a change to `wbi blast`, and `wbi drift` / `wbi check` report *undeclared coupling* (one task's code importing another's with no dependency edge).
- **Signed sync**: every published batch is ed25519-signed; forged or tampered batches are always rejected. `wbi team join|add|remove|enforce|list|key` manage `.wbi/team.json`, an allow-list that changes via pull request; with `enforce` only listed keys are applied.
- `wbi sync compact`: replaces the log's history with one snapshot under `--force-with-lease`, so it can never clobber a concurrent push.
- `wbi sync watch`: polling live mode.
- `wbi notify`: Slack, Discord and generic JSON webhook notifications; the URL is read from a named environment variable and never stored.
- `wbi dashboard` (`--out`, `--serve`, `--json`): a self-contained single-file HTML dashboard (graph with click-to-trace and contract blast radius, tasks, agents, intents, handoffs, drift, plan).
- Adapter registry (`wbi adapters list`, `docs/ADAPTERS.md`): one entry per agent with an explicit verification status. Statuses are pinned by a test and backed by [REAL-AGENTS.md](docs/REAL-AGENTS.md).
- MCP tools `wbi_start_task`, `wbi_release_task`, `wbi_release_intents` (18 tools total).
- Human-only acceptance criteria (`"human": true`): an agent cannot attest them; the task goes to review and `wbi approve` is the sign-off.
- Claude Code is auto-detected (`CLAUDECODE=1`); claim errors now say who *you* are.
- The GitHub Action writes a Markdown job summary, and pinning it (`@v0.2.0`) now pins the `wbi` version it installs (previously it always installed the latest release).
- GitHub Pages site and the `scripts/site-data.py` generator that builds it from real demo runs.
- Issue and PR templates (including an "I ran another agent" report), Dependabot, CODEOWNERS, release docs.

### Changed
- Worktrees now live in `.wbi/worktrees/<TASK>` (git-ignored) so agents sandboxed to the repo directory can reach them.
- Architecture tasks list every project contract in their work packet.
- The state database only migrates when its schema version is out of date, and the SQLite busy timeout is 20 s. Previously every invocation re-ran the migration, which made many concurrent agents contend for the write lock (caught by the 24-process stress test on Windows CI).
- `wbi check` infers the task from the head ref being checked.

### Fixed
- Task branches were named after the Go module path instead of `wbi/TASK-n` in v0.1.0 (see below).
- Python relative imports of the form `from . import x` were resolved to the wrong path by the import scanner.

### Security
- Sync events from the shared log are sanitized (control characters stripped) before they can reach a terminal, and unknown event types are ignored.

## [0.1.1] - 2026-10-02

### Added
- `wbi check`: the merge gate (scope, contract ownership and versioning, constitution, plan integrity, optional handoff and human-approval gates) and the reusable GitHub Action.
- Release tooling: `scripts/release.sh`, `install.sh` (checksum-verified, bounded network calls), Homebrew formula generator, CI on Linux, macOS and Windows.

### Fixed
- **Task branches were named `github.com/shriyashish-mishra/who-broke-it/TASK-n` instead of `wbi/TASK-n`** (a blanket import rewrite also rewrote the string literal). v0.1.0 shipped with this bug.

## [0.1.0] - 2026-10-02 (pre-release, do not use)

First public cut. Contains the task-branch naming bug above; use 0.1.1 or later.

### Added
- Engineering Graph in `.wbi/`, planning, claims, work packets, intent registry, contract versioning and propagation, blast radius over declared relationships, the Agent Judge, drift, blame / why, simulation, MCP server (15 tools), agent instruction files, git-native cross-machine sync (claims race on a compare-and-swap push).

[Unreleased]: https://github.com/shriyashish-mishra/who-broke-it/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/shriyashish-mishra/who-broke-it/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/shriyashish-mishra/who-broke-it/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/shriyashish-mishra/who-broke-it/releases/tag/v0.1.0
