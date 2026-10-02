# Contributing

Thanks for helping. This project coordinates other people's agents, so we try to practice what we preach: small, scoped, verifiable changes.

## Setup

```bash
git clone <repo> && cd who-broke-it
make test         # go vet + go test; unit + end-to-end tests on real temp git repos
make demo         # full scenario on a throwaway repo
make demo-team    # two clones + a bare remote: the cross-machine story
```

Go ≥ 1.26 and git are required. The binary is pure Go (no cgo) and has one third-party dependency, a pure-Go SQLite driver. Please keep the dependency list that short; open an issue first if you think another is needed.

## Ground rules

* **Deterministic before LLM.** If a check can be an algorithm (overlap, DAG, scope, rule, diff), it must be. LLMs belong in pluggable planning, never in verification.
* **Core stays vendor-neutral.** Nothing in `internal/engine`, `internal/intent`, `internal/blast` may mention a specific agent or protocol. MCP and agent files are adapters.
* **Don't fake it.** Features must act on real repository state. If something is heuristic, say so in the output and the docs.
* **Honest limits.** If you add a capability that has a known gap, document the gap in the README's "does not work yet" section.

## Where things go

See the module table in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). New pure analysis goes in its own package with unit tests; new verbs go in `internal/engine`, then get a CLI command (`cmd/wbi/main.go`) and an MCP tool (`internal/mcp/mcp.go`), and a row in [docs/PROTOCOL.md](docs/PROTOCOL.md).

## Tests

* Unit tests for pure functions (`internal/glob`, `internal/graph`, `internal/planner`, `internal/rules` `_test.go` files).
* End-to-end tests in `internal/engine/engine_test.go` and `internal/mcp/mcp_test.go` build a real git repo, plan it, run agents in real worktrees, and assert on coordination outcomes. Add a scenario there for any behavior that crosses modules.

## Adding an agent adapter

Add an entry to `Targets` in `internal/engine/adapters.go` (guidance file) and a case in `MCPConfig`. See [docs/INTEGRATING.md](docs/INTEGRATING.md).

## Pull requests

* One concern per PR; describe the user-visible behavior change.
* `make test` must pass (and `gofmt -l .` must print nothing).
* Update docs and, for protocol changes, `docs/PROTOCOL.md`.
* Be kind in review. See the [Code of Conduct](CODE_OF_CONDUCT.md).
