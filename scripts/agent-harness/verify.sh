#!/bin/bash
# usage: verify.sh <repo>   -> evidence from wbi's database and git, independent of what the agent said
cd "$1"; export WBI_NO_GH=1 WBI_NO_SYNC=1 NO_COLOR=1; W="${WBI:-wbi}"
echo "task status : $($W tasks | awk '{print $1, $2}')"
echo "worktree    : $( [ -d .wbi/worktrees/TASK-001 ] && echo yes || echo NO )"
echo "hello.js    : $( [ -f .wbi/worktrees/TASK-001/src/hello.js ] && tr '\n' ' ' < .wbi/worktrees/TASK-001/src/hello.js || echo MISSING )"
echo "src/ on main: $( [ -e src ] && echo PRESENT-protocol-violated || echo absent )"
echo "commits     :"; git log --branches='wbi/*' --format='   %h %s | %(trailers:key=WBI-Task,valueonly,separator=,) %(trailers:key=WBI-Agent,valueonly,separator=,)' -4 | grep -v "wbi graph\|init"
python3 - "$1" <<'PY'
import sqlite3,sys,json
db=sqlite3.connect(sys.argv[1]+"/.git/wbi/state.db")
ev=[(t,json.loads(p or '{}')) for t,p in db.execute("select type,payload from events order by id")]
print("events      :", ", ".join(t+("("+p.get("kind","")+" "+p.get("target","")+")" if t.startswith("intent") else "") for t,p in ev))
PY
