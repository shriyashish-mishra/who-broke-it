# Security Policy

## Reporting a vulnerability

Please **do not** open a public issue. Use GitHub's private vulnerability reporting ("Security" tab → "Report a vulnerability") on this repository, or contact the maintainers privately. We aim to acknowledge within 3 business days.

## Threat model notes (v0.x)

* **Plans are code.** `.wbi/tasks/*.json` can contain `check: {type: "command"}` acceptance criteria, and `wbi handoff --tests "<cmd>"` runs a shell command. These execute with your user's privileges in the task workdir. Review changes to `.wbi/` in PRs like any executable code, and do not run `wbi verify`/`wbi handoff` on checkouts you do not trust.
* **Cooperative enforcement.** Intent declarations and scope checks are advisory for agents that don't call them; verification catches violations after the fact. Do not rely on `wbi` as a sandbox or access-control boundary. Use OS/container isolation for untrusted agents.
* **Sync trust.** `refs/wbi/sync` is writable by anyone who can push to the remote, so a teammate (or a compromised token) can forge events: release your claim, post inbox messages, bump contract versions. Treat it like push access to the code. Incoming event text is sanitized (control characters stripped), SQL is parameterized, and unknown event types are ignored; events are not signed yet.
* **Local state.** Runtime state is a SQLite file in the git common dir. It contains task, agent and message metadata, not credentials.
* **MCP server.** Stdio only, no network listener; it trusts the launching process.
* **Secret scanning** in the default constitution is a best-effort regex, not a substitute for a real scanner.

## Supported versions

Pre-1.0: only the latest release receives fixes.
