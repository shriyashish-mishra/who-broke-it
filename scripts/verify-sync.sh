#!/usr/bin/env bash
# Verify wbi's cross-machine sync against a REAL git host (GitHub, GitLab, ...), with two (then three) independent clones.
# It uses one unique custom ref (refs/wbi/verify-<timestamp>) in a scratch repo and deletes it afterwards.
#
#   scripts/verify-sync.sh https://github.com/OWNER/SCRATCH_REPO.git [path/to/wbi]
#
# The scratch repo must already contain a .wbi graph on its default branch (wbi init + wbi plan, committed)
# and you must be able to push to it. Exit status is the number of failed checks.
set -uo pipefail
URL="${1:?usage: verify-sync.sh <repo-url> [wbi-binary]}"
WBI="${2:-wbi}"
TS=$(date +%s); REF="refs/wbi/verify-$TS"
W=$(mktemp -d)
FAILS=0
export WBI_NO_GH=1 NO_COLOR=1; unset FORCE_COLOR   # output is parsed below, so colours must be off
pass() { printf '  \033[32mPASS\033[0m %s\n' "$*"; }
fail() { printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAILS=$((FAILS+1)); }
check() { local msg="$1"; shift; if "$@" >/dev/null 2>&1; then pass "$msg"; else fail "$msg"; fi; }
section() { printf '\n\033[1m%s\033[0m\n' "$*"; }
cleanup() { git -C "$W/a" push -q origin ":$REF" 2>/dev/null; echo; echo "cleanup: deleted $REF -> remaining wbi refs on the remote: [$(git ls-remote "$URL" 'refs/wbi/*' | wc -l | tr -d ' ')]"; }
trap cleanup EXIT

clone() { git clone -q "$URL" "$W/$1" && ( cd "$W/$1" && git config user.name "$2" && git config user.email "$1@example.com" && "$WBI" sync init --ref "$REF" >/dev/null ); }
alice() { ( cd "$W/a" && WBI_AGENT=claude WBI_DEVELOPER=Alice "$WBI" "$@" ); }
bob()   { ( cd "$W/b" && WBI_AGENT=codex  WBI_DEVELOPER=Bob   "$WBI" "$@" ); }
carol() { ( cd "$W/c" && WBI_AGENT=opencode WBI_DEVELOPER=Carol "$WBI" "$@" ); }
# prints the owning agent of $TASK as seen by a clone, or nothing if unowned (an owned line ends in "(agent@dev)")
owner() { "$@" tasks 2>/dev/null | awk -v t="$TASK" '$1==t && $NF ~ /^\(.*\)$/ {gsub(/[()]/,"",$NF); print $NF}'; }
fetchref() { git -C "$W/$1" fetch -q origin "+$REF:refs/wbi/chk" 2>/dev/null; }

echo "scratch repo: $URL"; echo "ref:          $REF"; echo "binary:       $("$WBI" version)"
clone a Alice; clone b Bob
check "task graph present in the scratch repo" test -d "$W/a/.wbi/tasks"

section "1. signed batches replicate through the host, on a custom ref"
alice claim TASK-001 >/dev/null 2>&1
fetchref a
FILE=$(git -C "$W/a" ls-tree -r --name-only refs/wbi/chk 2>/dev/null | grep '^log/' | head -1)
check "the host accepted a push to $REF" test -n "$FILE"
check "the published batch carries an ed25519 signature header" bash -c "git -C '$W/a' show 'refs/wbi/chk:$FILE' | head -1 | grep -q '\"sig\"'"
bob sync >/dev/null 2>&1
TASK=TASK-001; check "bob (separate clone) sees alice's claim" test "$(owner bob)" = "claude@alice"

section "2. a forged batch pushed to the host is rejected"
fetchref b
TIP=$(git -C "$W/b" rev-parse refs/wbi/chk); FILE=$(git -C "$W/b" ls-tree -r --name-only "$TIP" | grep '^log/' | head -1)
BODY=$(git -C "$W/b" show "$TIP:$FILE"); FORGED=$(printf '%s\n' "$BODY" | sed 's/claude@alice/mallory@evil/g')
BLOB=$(printf '%s\n' "$FORGED" | git -C "$W/b" hash-object -w --stdin)
export GIT_INDEX_FILE="$W/forge.idx" GIT_AUTHOR_NAME=x GIT_AUTHOR_EMAIL=x@x GIT_COMMITTER_NAME=x GIT_COMMITTER_EMAIL=x@x
git -C "$W/b" read-tree "$TIP"; git -C "$W/b" update-index --add --cacheinfo "100644,$BLOB,log/mallory/forged.jsonl"
COMMIT=$(git -C "$W/b" commit-tree "$(git -C "$W/b" write-tree)" -p "$TIP" -m "totally legit")
unset GIT_INDEX_FILE GIT_AUTHOR_NAME GIT_AUTHOR_EMAIL GIT_COMMITTER_NAME GIT_COMMITTER_EMAIL
git -C "$W/b" push -q origin "$COMMIT:$REF" 2>/dev/null
OUT=$(bob sync 2>&1); echo "$OUT" | sed 's/^/        /'
check "bob's sync reports the batch as not verifying" grep -q "does not verify" <<<"$OUT"
check "the forged owner never appears" test "$(owner bob)" = "claude@alice"

section "3. compaction with --force-with-lease on a custom ref (and it purges the forged data)"
OUT=$(alice sync compact 2>&1); echo "$OUT" | sed 's/^/        /'
check "compaction succeeded against the host" grep -q "compacted" <<<"$OUT"
fetchref a
check "the remote log is now exactly one commit" test "$(git -C "$W/a" rev-list --count refs/wbi/chk)" = "1"
check "the forged file is gone from the remote" bash -c "! git -C '$W/a' ls-tree -r --name-only refs/wbi/chk | grep -q mallory"
bob sync >/dev/null 2>&1; TASK=TASK-001
check "bob converges after the history rewrite" test "$(owner bob)" = "claude@alice"
clone c Carol; carol sync >/dev/null 2>&1
check "a brand-new clone bootstraps from the snapshot alone" test "$(owner carol)" = "claude@alice"

section "4. the team allow-list (enforce) against real pushes"
alice team join --name Alice --enforce >/dev/null 2>&1
bob claim TASK-002 --force >/dev/null 2>&1; bob sync >/dev/null 2>&1
OUT=$(alice sync 2>&1); echo "$OUT" | sed 's/^/        /'
check "alice quarantines bob while he is not on the team" grep -q "not in .wbi/team.json" <<<"$OUT"
TASK=TASK-002; check "bob's unauthorized claim is not applied on alice's side" test -z "$(owner alice)"
BOBKEY=$(bob team key | head -1)
alice team add "$BOBKEY" --name Bob >/dev/null 2>&1; alice sync >/dev/null 2>&1
check "after being authorized, bob's earlier claim applies" test "$(owner alice)" = "codex@bob"

section "5. concurrent claims racing through the host (6 rounds, both signed)"
alice team enforce off >/dev/null 2>&1
WINS=0
for i in 1 2 3 4 5 6; do
  alice release TASK-001 >/dev/null 2>&1; alice sync >/dev/null 2>&1; bob sync >/dev/null 2>&1
  ( alice claim TASK-001 >"$W/ra.out" 2>&1; echo $? >"$W/ra.rc" ) & ( bob claim TASK-001 >"$W/rb.out" 2>&1; echo $? >"$W/rb.rc" ) & wait
  alice sync >/dev/null 2>&1; bob sync >/dev/null 2>&1
  TASK=TASK-001; OA=$(owner alice); OB=$(owner bob)
  N=$(( $(cat "$W/ra.rc") == 0 )); N=$(( N + ($(cat "$W/rb.rc") == 0) ))
  if [ "$N" = 1 ] && [ -n "$OA" ] && [ "$OA" = "$OB" ]; then WINS=$((WINS+1)); else echo "        round $i: winners=$N alice sees [$OA] bob sees [$OB]"; fi
done
[ "$WINS" = 6 ] && pass "6/6 rounds: exactly one winner and both clones agree" || fail "only $WINS/6 rounds had exactly one winner with agreement"

section "6. compaction racing live claims (3 rounds)"
SAFE=0
for i in 1 2 3; do
  alice release TASK-001 >/dev/null 2>&1; alice sync >/dev/null 2>&1; bob sync >/dev/null 2>&1
  ( alice sync compact >"$W/ca.out" 2>&1 ) & ( bob claim TASK-001 >"$W/cb.out" 2>&1; echo $? >"$W/cb.rc" ) & wait
  alice sync >/dev/null 2>&1; bob sync >/dev/null 2>&1; alice sync >/dev/null 2>&1
  TASK=TASK-001; OA=$(owner alice); OB=$(owner bob)
  if [ "$(cat "$W/cb.rc")" = 0 ]; then WANT="codex@bob"; else WANT="$OA"; fi
  if [ -n "$OA" ] && [ "$OA" = "$OB" ] && [ "$OB" = "$WANT" ]; then SAFE=$((SAFE+1)); else echo "        round $i: bob rc=$(cat "$W/cb.rc") alice sees [$OA] bob sees [$OB]"; fi
done
[ "$SAFE" = 3 ] && pass "3/3 rounds: no claim lost, both clones converged" || fail "only $SAFE/3 rounds were safe"

echo; if [ "$FAILS" = 0 ]; then printf '\033[1;32mall checks passed\033[0m\n'; else printf '\033[1;31m%s check(s) failed\033[0m\n' "$FAILS"; fi
exit "$FAILS"
