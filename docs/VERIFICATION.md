# What has been verified, and how

Claims in this project are only as good as the evidence. This page lists what was actually run, where, and how to re-run it, and it is just as explicit about what was **not** tested.

## Verified

| Claim | Evidence | Re-run |
|---|---|---|
| Unit and end-to-end behaviour on **Linux, macOS and Windows** | CI matrix on every push (`go vet`, `go test ./...`, 24-process SQLite stress test; Windows included) | `make test` |
| Two "machines" (separate clones) coordinate through a plain git remote | Tests with a bare remote and real worktrees; claim races, offline queueing, contract propagation | `go test ./internal/replicate` |
| **Sync on real GitHub**: custom ref accepted, signed batches replicate, a forged batch pushed to GitHub is rejected, the team allow-list quarantines then applies a member, `--force-with-lease` compaction works and purges the forged data, 6/6 concurrent claim races have exactly one winner, 3/3 compactions racing live claims lose nothing | `scripts/verify-github-sync.sh` against the public demo repo (unique throwaway ref, deleted afterwards). 17/17 checks passed. | `scripts/verify-github-sync.sh <repo-url> <wbi>` |
| **Claude Code, Codex, Cursor Agent, OpenCode and Antigravity** each drove wbi end to end over MCP *and* the CLI protocol; Aider works with a human running `wbi` | Real runs on 2026-10-03, judged from wbi's database and git, not the agent's report; recorded with versions and findings in [REAL-AGENTS.md](REAL-AGENTS.md) | `/tmp`-style harness described in that file |
| The merge gate and `wbi pr` on **real pull requests** | Public demo repo [wbi-action-demo](https://github.com/shriyashish-mishra/wbi-action-demo): a clean PR passes; scope, constitution, contract-tampering and high-impact PRs fail with the right annotations | open a PR there |
| Install paths: `curl` installer (checksum verified, refuses on mismatch), Homebrew tap, `go install`, release archives | Run against the published release; Homebrew install/uninstall and the tamper test were executed | `docs/RELEASING.md` step 7 |
| Documentation matches the implementation | Tests fail if the README or docs mention a command, adapter or Action input that does not exist (mutation-checked) | `go test ./cmd/wbi -run Doc` |
| Website transcripts are real | `scripts/site-data.py` regenerates them from actual demo runs | `make site-data` |

## Not verified (so don't rely on it)

- **Gemini CLI**: Google rejected the test account ("migrate to Antigravity"), so it was not run. Antigravity, which reads the same `GEMINI.md`, was.
- **Other versions** of the verified agents, and Windows for any agent.
- **Aider driving the protocol itself**: it cannot (no shell); only the human-driven mode was verified. See [ADAPTERS.md](ADAPTERS.md).
- **GitLab, Bitbucket, self-hosted git** as the sync remote. Only GitHub and local bare repos were tested. If a host blocks `refs/wbi/*`, use `wbi sync init --ref refs/heads/wbi-sync` (tested on a plain git remote, not on those hosts).
- **Slack and Discord notifications** were tested against local HTTP servers that mimic the webhook contract, **not** against real Slack or Discord endpoints.
- **Linear and Jira**: not built.
- **Windows demo scripts** (they are bash). The Go tests run on Windows in CI; the demos run on Linux and macOS.
- **Sync latency/scale**: tested with a handful of clones and a log of tens of events, not hundreds of machines or a long-lived busy log.
- **Security review**: signatures and the allow-list were tested adversarially (forged batch, unauthorized key), but there has been no independent audit. Authorized team members are trusted with each other by design.
- **Code graph accuracy** on real-world repos: tested on fixtures. Aliases, computed imports and generated code are not followed.

## How to add to this page

Run something for real, put what happened here (or in REAL-AGENTS.md), including failures. If a claim cannot be re-run by someone else, it does not belong in "Verified".
