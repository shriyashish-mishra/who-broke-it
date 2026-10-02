#!/usr/bin/env bash
# End-to-end demo: three "developers", three different agent tools, one repo.
#
# Everything wbi does here is real: real git repo, real branches and worktrees, real commits,
# real SQLite coordination state. The only simulated part is the *coding*: each agent's turn is
# a scripted commit, because this demo must run offline without anyone's AI subscription.
# Swap any step for a real `claude` / `codex` / `gemini` session and the coordination is identical.
set -euo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
WBI_BIN="$HERE/bin/wbi"
[ -x "$WBI_BIN" ] || (cd "$HERE" && go build -o bin/wbi ./cmd/wbi)
wbi() { "$WBI_BIN" "$@"; }
export WBI_NO_GH=1
SLOW=${SLOW:-0}

DEMO="${DEMO_DIR:-$(mktemp -d)/healthai}"
rm -rf "$DEMO" "$DEMO"-TASK-*
mkdir -p "$DEMO"
step() { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; sleep "$SLOW"; }
as() { # as <agent> <developer> -- command...
  local a="$1" d="$2"; shift 2
  ( export WBI_AGENT="$a" WBI_DEVELOPER="$d"; "$@" )
}
commit_in() { # commit_in <dir> <agent> <dev> <msg> ; files already written
  ( cd "$1" && git add -A && WBI_AGENT="$2" WBI_DEVELOPER="$3" git -c user.name="$3" -c user.email="$3@example.com" commit -qm "$4" )
}
write() { mkdir -p "$(dirname "$1")"; cat > "$1"; }

step "0. An existing repo (a small Next.js + Prisma app)"
cd "$DEMO"
git init -q -b main && git config user.name "Maya" && git config user.email maya@example.com
write package.json <<'EOF'
{ "name": "healthai", "scripts": { "test": "echo tests ok" }, "dependencies": { "next": "15.0.0", "react": "19.0.0", "prisma": "6.0.0" } }
EOF
write src/api/billing/legacy.ts <<'EOF'
export const legacyPlans = ['free', 'pro'];
EOF
git add -A && git commit -qm "initial commit"

step "1. wbi init + wbi plan  (repo → Engineering Graph)"
wbi init
wbi plan "Build a multi-tenant SaaS dashboard with authentication, billing, analytics and an AI assistant."
git add -A && git commit -qm "chore: add wbi engineering graph"

step "2. wbi simulate  (the human approves the plan before any agent starts)"
wbi simulate --agents 3 --approve

step "3. TASK-001 Architecture: Maya + Claude Code"
as claude Maya wbi start TASK-001 --worktree | tail -7
write "$DEMO-TASK-001/docs/architecture-notes.md" <<'EOF'
# Architecture notes
Contracts reviewed. BillingStatus = active | past_due | canceled. Tenant isolation via tenant_id.
EOF
commit_in "$DEMO-TASK-001" claude Maya "docs: architecture notes"
as claude Maya bash -c "cd '$DEMO-TASK-001' && '$WBI_BIN' handoff TASK-001 --summary 'Contracts and constitution reviewed' --attest 1,2"

step "4. TASK-002 Database: Dev B + Codex  (a migration, so a human must approve)"
as codex DevB wbi start TASK-002 --worktree | tail -5
as codex DevB wbi intent declare CREATE 'migrations/001_init.sql' --task TASK-002
write "$DEMO-TASK-002/migrations/001_init.sql" <<'EOF'
create table tenants (id uuid primary key);
create table users (id uuid primary key, tenant_id uuid references tenants(id));
create table subscriptions (id uuid primary key, tenant_id uuid references tenants(id), status text);
EOF
write "$DEMO-TASK-002/src/db/schema.ts" <<'EOF'
export type Tenant = { id: string };
EOF
commit_in "$DEMO-TASK-002" codex DevB "feat(db): tenant-scoped schema"
as codex DevB bash -c "cd '$DEMO-TASK-002' && '$WBI_BIN' handoff TASK-002 --summary 'Tenant-scoped schema + initial migration' --tests 'echo 18 passed' --attest 1,2"
wbi approve TASK-002 --as Maya

step "5. Dev C's Gemini tries TASK-007 too early: wbi explains the block"
as gemini DevC wbi claim TASK-007 || true

step "6. Auth API (Claude) → approved → the graph opens up"
as claude Maya wbi start TASK-004 --worktree | tail -4
write "$DEMO-TASK-004/src/api/auth/login.ts" <<'EOF'
export type Session = { userId: string; tenantId: string; expiresAt: string };
export function login(email: string): Session { return { userId: email, tenantId: 't1', expiresAt: '2030-01-01' }; }
EOF
commit_in "$DEMO-TASK-004" claude Maya "feat(auth): login + Session"
as claude Maya bash -c "cd '$DEMO-TASK-004' && '$WBI_BIN' handoff TASK-004 --summary 'Login endpoint and Session contract' --tests 'echo 9 passed' --attest 1,2"
wbi approve TASK-004 --as Maya
wbi status

step "7. Three agents in parallel: Billing API (Claude), Web shell (Gemini), then Intent conflicts"
as claude Maya wbi start TASK-007 --worktree | tail -4
as gemini DevC wbi start TASK-003 --worktree | tail -4
as claude Maya wbi intent declare MODIFY 'src/api/billing/**' --task TASK-007
echo; echo "Gemini (TASK-003) wanders into billing code, and Codex tries to change BillingStatus. wbi catches both BEFORE any code is written:"
as gemini DevC wbi intent declare MODIFY 'src/api/billing/plans.ts' --task TASK-003 || true
as codex DevB wbi claim TASK-010 --force >/dev/null
as codex DevB wbi intent declare CHANGE_CONTRACT BillingStatus --task TASK-010 || true

step "8. Contract-first: Gemini starts Billing UI early against the published contract (--force)"
as gemini DevC wbi claim TASK-008 --force
write "$DEMO-TASK-003/src/web/shell/layout.tsx" <<'EOF'
export const Layout = () => null;
EOF
commit_in "$DEMO-TASK-003" gemini DevC "feat(web): app shell"

step "9. Claude finishes Billing API and CHANGES BillingStatus ('paused'): watch it propagate"
write "$DEMO-TASK-007/src/api/billing/status.ts" <<'EOF'
export type BillingStatus = 'active' | 'past_due' | 'canceled' | 'paused';
EOF
commit_in "$DEMO-TASK-007" claude Maya "feat(billing): status endpoint, add paused"
as claude Maya bash -c "cd '$DEMO-TASK-007' && '$WBI_BIN' handoff TASK-007 --summary 'Billing API v2' --tests 'echo 24 passed' --contract 'BillingStatus=now supports paused' --limitation 'Refund API not implemented' --attest 1,2"
wbi approve TASK-007 --as Maya

step "10. Blast radius, Gemini's inbox, and mission control"
wbi blast BillingStatus
echo; echo "Gemini's inbox:"; as gemini DevC wbi inbox
wbi status

step "11. The Judge: 'done' is not DONE. Codex ships a constitution violation in Analytics"
as codex DevB wbi release TASK-010 >/dev/null
as codex DevB wbi start TASK-010 --worktree | tail -3
write "$DEMO-TASK-010/src/api/analytics/summary.ts" <<'EOF'
export async function summary(db: any) { return db.billing.update({ status: 'active' }); }
EOF
commit_in "$DEMO-TASK-010" codex DevB "feat(analytics): summary"
as codex DevB bash -c "cd '$DEMO-TASK-010' && '$WBI_BIN' handoff TASK-010 --summary 'Analytics summary' --tests 'echo 5 passed' --attest 1,2" || true

step "12. wbi blame / why / drift: the point of the whole thing"
wbi blame src/api/billing/
echo
wbi why src/api/billing/status.ts
echo
wbi drift
echo
wbi graph | sed -n 1,22p

echo; printf '\033[1;32m✓ demo complete.\033[0m Repo left at %s (worktrees alongside). Try: cd %s && %s/bin/wbi status\n' "$DEMO" "$DEMO" "$HERE"
