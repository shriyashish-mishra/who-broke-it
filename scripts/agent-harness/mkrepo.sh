#!/bin/bash
# usage: mkrepo.sh <name> [adapter]   -> prints the path of a fresh repo with a wbi graph and that agent's instruction file
set -e; export WBI_NO_GH=1 WBI_NO_SYNC=1 NO_COLOR=1
HERE="$(cd "$(dirname "$0")" && pwd)"; W="${WBI:-wbi}"
D="${RUNS:-${TMPDIR:-/tmp}/wbi-agent-runs}/$1-$(date +%H%M%S)"; mkdir -p "$D"; cd "$D"
git init -q -b main; git config user.name Maya; git config user.email maya@example.com
echo "# demo" > README.md; git add -A; git commit -qm init
$W init >/dev/null; $W plan --from "$HERE/plan.json" >/dev/null; $W simulate --approve >/dev/null
$W adapters install "${2:-$1}" >/dev/null
git add -A; git commit -qm "wbi graph"
echo "$D"
