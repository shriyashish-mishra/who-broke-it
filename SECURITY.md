# Security Policy

## Reporting a vulnerability

Please **do not** open a public issue. Use GitHub's private vulnerability reporting: [report a vulnerability](https://github.com/shriyashish-mishra/who-broke-it/security/advisories/new) (it is enabled on this repository). We aim to acknowledge within 3 business days.

## Threat model notes (v0.x)

* **Plans are code.** `.wbi/tasks/*.json` can contain `check: {type: "command"}` acceptance criteria, and `wbi handoff --tests "<cmd>"` runs a shell command. These execute with your user's privileges in the task workdir. Review changes to `.wbi/` in PRs like any executable code, and do not run `wbi verify`/`wbi handoff` on checkouts you do not trust.
* **Cooperative enforcement.** Intent declarations and scope checks are advisory for agents that don't call them; verification catches violations after the fact. Do not rely on `wbi` as a sandbox or access-control boundary. Use OS/container isolation for untrusted agents.
* **Sync trust.** `refs/wbi/sync` is writable by anyone who can push to the remote. Every published batch is ed25519-signed with a per-clone key: tampered or forged batches are rejected, and with `enforce` on (`wbi team join --enforce`) only keys in the committed `.wbi/team.json` are applied, so adding a member is a reviewed PR. Remaining limits: an *authorized* member can still publish events about any task (no per-object permission); revoking a key re-verifies the log on the next sync but cannot un-send what a member already did; the private key lives unencrypted (mode 0600) in `.git/wbi/`. Incoming event text is sanitized (control characters stripped), SQL is parameterized, and unknown event types are ignored.
* **Local state.** Runtime state is a SQLite file in the git common dir. It contains task, agent and message metadata, not credentials.
* **MCP server.** Stdio only, no network listener; it trusts the launching process.
* **Secret scanning** in the default constitution is a best-effort regex, not a substitute for a real scanner.

## Supported versions

Pre-1.0: only the latest release receives fixes. Release artifacts are published with `checksums.txt`, and `install.sh` verifies it and refuses to install on a mismatch. Sync signing and the allow-list were tested adversarially against real GitHub (see [docs/VERIFICATION.md](docs/VERIFICATION.md)); there has been no independent security audit.
