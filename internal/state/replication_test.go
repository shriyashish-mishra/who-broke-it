package state

import (
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestTriggersCaptureLocalWritesButNotAppliedOnes(t *testing.T) {
	d := open(t)
	_, _ = d.Exec(`INSERT INTO task_state (task_id,status,owner,agent_id,branch,claimed_at,updated_at) VALUES ('T1','IN_PROGRESS','a','x@a','wbi/T1',1,1)`)
	_, _ = d.Exec(`INSERT INTO notifications (ts,task_id,agent_id,message,ref) VALUES (1,'T1','x@a','hi','r')`)
	evs, _ := d.Pending()
	if len(evs) != 2 {
		t.Fatalf("want 2 captured events, got %d", len(evs))
	}
	if evs[1].Row["uid"] == nil || evs[1].Row["uid"] == "" {
		t.Fatal("rows with identity must get a uid")
	}
	// A no-op update must not generate an event; a real change must.
	_, _ = d.Exec(`UPDATE task_state SET updated_at=2 WHERE task_id='T1'`)
	_, _ = d.Exec(`UPDATE task_state SET status='REVIEW' WHERE task_id='T1'`)
	if evs, _ = d.Pending(); len(evs) != 3 {
		t.Fatalf("want 3 events after one real update, got %d", len(evs))
	}
	// Applying remote events must not echo into the outbox.
	before := d.PendingCount()
	if _, _, err := d.Merge([]Event{{UID: "u1", Tbl: "events", Row: map[string]any{"uid": "u1", "ts": 1.0, "type": "x", "payload": "{}"}}}, ""); err != nil {
		t.Fatal(err)
	}
	if d.PendingCount() != before {
		t.Fatalf("applied events leaked into the outbox: %d -> %d", before, d.PendingCount())
	}
}

func TestMergeSanitizesIgnoresUnknownAndKeepsLocalColumns(t *testing.T) {
	d := open(t)
	_, _ = d.Exec(`INSERT INTO task_state (task_id,status,worktree,updated_at) VALUES ('T1','TODO','/my/worktree',1)`)
	_, _ = d.Exec(`UPDATE outbox SET state='sent'`)
	evil := Event{UID: "n1", Tbl: "notifications", Row: map[string]any{"uid": "n1", "ts": 5.0, "task_id": "T1", "message": "\x1b[31mgotcha\x1b[0m\x07 ok", "ref": ""}}
	unknown := Event{UID: "z", Tbl: "drop_table", Row: map[string]any{"x": 1.0}}
	claim := Event{UID: "c", Tbl: "task_state", Row: map[string]any{"task_id": "T1", "status": "IN_PROGRESS", "agent_id": "x@y", "owner": "y", "branch": "b", "claimed_at": 9.0, "updated_at": 9.0}}
	if _, _, err := d.Merge([]Event{evil, unknown, claim}, "abc"); err != nil {
		t.Fatal(err)
	}
	var msg string
	_ = d.QueryRow(`SELECT message FROM notifications WHERE uid='n1'`).Scan(&msg)
	if strings.ContainsAny(msg, "\x1b\x07") || !strings.Contains(msg, "gotcha") {
		t.Fatalf("control characters must be stripped: %q", msg)
	}
	var wt, ag string
	_ = d.QueryRow(`SELECT COALESCE(worktree,''), COALESCE(agent_id,'') FROM task_state WHERE task_id='T1'`).Scan(&wt, &ag)
	if wt != "/my/worktree" || ag != "x@y" {
		t.Fatalf("worktree is machine-local and must survive a merge: wt=%q agent=%q", wt, ag)
	}
	if d.KV("sync.last_seen") != "abc" {
		t.Fatal("merge must record how far the log has been read")
	}
}

func TestContractVersionsAndAcksOnlyMoveForward(t *testing.T) {
	d := open(t)
	ev := func(v float64) Event {
		return Event{UID: "c", Tbl: "contract_state", Row: map[string]any{"name": "C", "version": v, "task_id": "T", "ts": 1.0, "note": ""}}
	}
	_, _, _ = d.Merge([]Event{ev(3), ev(2)}, "")
	var v int
	_ = d.QueryRow(`SELECT version FROM contract_state WHERE name='C'`).Scan(&v)
	if v != 3 {
		t.Fatalf("an older event must not roll a contract back, got v%d", v)
	}
}
