#!/usr/bin/env bash
# Two developers on two separate clones ("machines") that share nothing but a git remote.
# No server, no account: wbi publishes coordination state to refs/wbi/sync on the same remote as the code.
#
# Everything is real (git, SQLite, the sync protocol). Only the coding is scripted so it runs offline.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
WBI_BIN="$HERE/bin/wbi"
[ -x "$WBI_BIN" ] || (cd "$HERE" && go build -o bin/wbi ./cmd/wbi)
export WBI_NO_GH=1

ROOT="${DEMO_DIR:-$(mktemp -d)}"
rm -rf "$ROOT" && mkdir -p "$ROOT"
REMOTE="$ROOT/remote.git"; ALICE="$ROOT/alice"; BOB="$ROOT/bob"
step() { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
say()  { printf '\033[2m  %s\033[0m\n' "$*"; }
alice() { ( cd "$ALICE" && WBI_AGENT=claude WBI_DEVELOPER=Alice "$WBI_BIN" "$@" ); }
bob()   { ( cd "$BOB"   && WBI_AGENT=codex  WBI_DEVELOPER=Bob   "$WBI_BIN" "$@" ); }

step "0. A shared remote and Alice's repo"
git init -q --bare -b main "$REMOTE"
mkdir -p "$ALICE" && cd "$ALICE"
git init -q -b main && git config user.name Alice && git config user.email alice@example.com
echo "# healthai" > README.md && git add -A && git commit -qm "initial commit"
git remote add origin "$REMOTE"

step "1. Alice plans the project and turns on sync (one command, committed with the repo)"
alice init >/dev/null
alice plan "Build a SaaS app with authentication, billing and analytics" | sed -n 3,4p
alice sync init
git add -A && git commit -qm "chore: wbi plan + sync" && git push -q origin main

step "2. Bob clones: nothing to configure, sync comes with the repo"
git clone -q "$REMOTE" "$BOB" && cd "$BOB" && git config user.name Bob && git config user.email bob@example.com
bob sync status

step "3. Alice finishes TASK-001. Bob has not been told anything."
alice start TASK-001 --worktree >/dev/null
mkdir -p "$ALICE/.wbi/worktrees/TASK-001/docs" && echo "contracts reviewed" > "$ALICE/.wbi/worktrees/TASK-001/docs/architecture-notes.md"
( cd "$ALICE/.wbi/worktrees/TASK-001" && git add -A && git -c user.name=Alice -c user.email=a@x commit -qm "docs: architecture notes" )
( cd "$ALICE/.wbi/worktrees/TASK-001" && WBI_AGENT=claude WBI_DEVELOPER=Alice "$WBI_BIN" handoff TASK-001 --summary "Contracts reviewed" --attest 1,2 | tail -3 )
say "architecture criteria are human sign-offs, so Alice (the person) approves:"
alice approve TASK-001 --as Alice

step "4. Bob opens mission control. His clone pulls the team's state first."
bob status

step "5. The race: both grab TASK-003 while offline from each other (WBI_NO_SYNC=1)"
WBI_NO_SYNC=1 alice claim TASK-003
WBI_NO_SYNC=1 bob claim TASK-003
say "each machine believes it owns the task. Alice publishes first:"
alice sync
say "Bob syncs second. The remote's order decides, not anyone's clock:"
bob sync
bob tasks --status in_progress

step "6. Alice declares intent; Bob sees it before touching the same area"
alice intent declare MODIFY 'src/web/shell/**' --task TASK-003
bob intent

step "7. Contract-first: Bob builds Billing UI early; Alice changes BillingStatus"
bob claim TASK-008 --force
alice start TASK-007 --worktree --force >/dev/null
mkdir -p "$ALICE/.wbi/worktrees/TASK-007/src/api/billing" && echo "export type S = 'paused'" > "$ALICE/.wbi/worktrees/TASK-007/src/api/billing/status.ts"
( cd "$ALICE/.wbi/worktrees/TASK-007" && git add -A && git -c user.name=Alice -c user.email=a@x commit -qm "feat(billing): paused" )
( cd "$ALICE/.wbi/worktrees/TASK-007" && WBI_AGENT=claude WBI_DEVELOPER=Alice "$WBI_BIN" handoff TASK-007 --summary "Billing API v2" --tests "echo 24 passed" --contract "BillingStatus=now supports paused" --attest 1,2,3 | grep -E "Contract|BillingStatus|Status:" )

step "8. Bob, on another machine, is told. His next command surfaces it."
bob inbox
bob blast BillingStatus | tail -6
bob sync status

printf '\n\033[1;32m✓ done.\033[0m alice=%s  bob=%s  remote=%s\n' "$ALICE" "$BOB" "$REMOTE"
