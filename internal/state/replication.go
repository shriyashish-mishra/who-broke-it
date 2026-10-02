package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Replication layer.
//
// Every write to a coordination table is captured by an AFTER trigger into `outbox`, so the engine never
// has to know about sync. `wbi sync` ships the outbox to a linear, git-backed event log and replays other
// machines' events here. While events are being applied the `applying` flag is set so the triggers stay
// quiet (no echo). Local-only columns (task_state.worktree, notifications.read) are never replicated.

const applyingGuard = `(SELECT v FROM sync_ctl WHERE k='applying')='0'`

func outboxInsert(table, jsonObj string) string {
	return `INSERT INTO outbox(uid,tbl,row,ts) VALUES (lower(hex(randomblob(8))),'` + table + `',` + jsonObj + `,CAST(strftime('%s','now') AS INTEGER)*1000);`
}

// fromTable builds an outbox insert for tables whose rows carry a uid that triggers must fill in first.
func fromTable(table, keyCol, cols string) string {
	return `UPDATE ` + table + ` SET uid = lower(hex(randomblob(8))) WHERE ` + keyCol + `=NEW.` + keyCol + ` AND uid IS NULL;
INSERT INTO outbox(uid,tbl,row,ts) SELECT lower(hex(randomblob(8))),'` + table + `',json_object(` + cols + `),CAST(strftime('%s','now') AS INTEGER)*1000 FROM ` + table + ` WHERE ` + keyCol + `=NEW.` + keyCol + `;`
}

func trig(name, event, table, when, body string) string {
	cond := applyingGuard
	if when != "" {
		cond += " AND (" + when + ")"
	}
	return fmt.Sprintf("CREATE TRIGGER IF NOT EXISTS %s AFTER %s ON %s WHEN %s BEGIN\n%s\nEND;\n", name, event, table, cond, body)
}

func replicationSchema() string {
	taskObj := `json_object('task_id',NEW.task_id,'status',NEW.status,'owner',NEW.owner,'agent_id',NEW.agent_id,'branch',NEW.branch,'claimed_at',NEW.claimed_at,'updated_at',NEW.updated_at)`
	agentObj := `json_object('id',NEW.id,'provider',NEW.provider,'type',NEW.type,'developer',NEW.developer,'repository',NEW.repository,'branch',NEW.branch,'status',NEW.status,'current_task',NEW.current_task,'capabilities',NEW.capabilities,'heartbeat',NEW.heartbeat)`
	handoffObj := `json_object('task_id',NEW.task_id,'json',NEW.json,'ts',NEW.ts)`
	contractObj := `json_object('name',NEW.name,'version',NEW.version,'task_id',NEW.task_id,'ts',NEW.ts,'note',NEW.note)`
	ackObj := `json_object('task_id',NEW.task_id,'contract',NEW.contract,'version',NEW.version)`
	kvObj := `json_object('key',NEW.key,'value',NEW.value)`
	intentCols := `'uid',uid,'agent_id',agent_id,'task_id',task_id,'kind',kind,'target',target,'note',note,'status',status,'created_at',created_at`
	eventCols := `'uid',uid,'ts',ts,'type',type,'task_id',task_id,'agent_id',agent_id,'payload',payload`
	noticeCols := `'uid',uid,'ts',ts,'task_id',task_id,'agent_id',agent_id,'message',message,'ref',ref`

	var b strings.Builder
	b.WriteString(`
CREATE TABLE IF NOT EXISTS sync_ctl (k TEXT PRIMARY KEY, v TEXT);
INSERT OR IGNORE INTO sync_ctl(k,v) VALUES ('applying','0');
CREATE TABLE IF NOT EXISTS outbox (seq INTEGER PRIMARY KEY AUTOINCREMENT, uid TEXT, tbl TEXT, row TEXT, ts INTEGER, state TEXT DEFAULT 'pending');
CREATE UNIQUE INDEX IF NOT EXISTS intents_uid ON intents(uid);
CREATE UNIQUE INDEX IF NOT EXISTS events_uid ON events(uid);
CREATE UNIQUE INDEX IF NOT EXISTS notifications_uid ON notifications(uid);
`)
	taskChanged := `NEW.status IS NOT OLD.status OR NEW.agent_id IS NOT OLD.agent_id OR NEW.owner IS NOT OLD.owner OR NEW.branch IS NOT OLD.branch`
	b.WriteString(trig("r_task_state_ai", "INSERT", "task_state", "", outboxInsert("task_state", taskObj)))
	b.WriteString(trig("r_task_state_au", "UPDATE", "task_state", taskChanged, outboxInsert("task_state", taskObj)))
	b.WriteString(trig("r_agents_ai", "INSERT", "agents", "", outboxInsert("agents", agentObj)))
	// Heartbeats are throttled: only replicate a heartbeat change if it moved by more than a minute.
	b.WriteString(trig("r_agents_au", "UPDATE", "agents",
		`NEW.status IS NOT OLD.status OR NEW.current_task IS NOT OLD.current_task OR NEW.capabilities IS NOT OLD.capabilities OR NEW.heartbeat - OLD.heartbeat > 60000`,
		outboxInsert("agents", agentObj)))
	b.WriteString(trig("r_intents_ai", "INSERT", "intents", "", fromTable("intents", "id", intentCols)))
	b.WriteString(`CREATE TRIGGER IF NOT EXISTS r_intents_au AFTER UPDATE OF status ON intents WHEN ` + applyingGuard + ` AND NEW.uid IS NOT NULL BEGIN
` + fromTable("intents", "id", intentCols) + `
END;
`)
	b.WriteString(trig("r_events_ai", "INSERT", "events", "", fromTable("events", "id", eventCols)))
	b.WriteString(trig("r_notifications_ai", "INSERT", "notifications", "", fromTable("notifications", "id", noticeCols)))
	b.WriteString(trig("r_handoffs_ai", "INSERT", "handoffs", "", outboxInsert("handoffs", handoffObj)))
	b.WriteString(trig("r_contract_state_ai", "INSERT", "contract_state", "", outboxInsert("contract_state", contractObj)))
	b.WriteString(trig("r_contract_state_au", "UPDATE", "contract_state", "NEW.version IS NOT OLD.version", outboxInsert("contract_state", contractObj)))
	b.WriteString(trig("r_task_ack_ai", "INSERT", "task_ack", "", outboxInsert("task_ack", ackObj)))
	b.WriteString(trig("r_task_ack_au", "UPDATE", "task_ack", "NEW.version IS NOT OLD.version", outboxInsert("task_ack", ackObj)))
	b.WriteString(trig("r_kv_ai", "INSERT", "kv", "NEW.key='plan_approved'", outboxInsert("kv", kvObj)))
	b.WriteString(trig("r_kv_au", "UPDATE", "kv", "NEW.key='plan_approved'", outboxInsert("kv", kvObj)))
	return b.String()
}

func ensureColumn(db *sql.DB, table, col, typ string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil && name == col {
			has = true
		}
	}
	rows.Close()
	if has {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col + ` ` + typ)
	return err
}

// migrateReplication adds uid columns, the outbox and the capture triggers. It is idempotent.
func migrateReplication(db *sql.DB) error {
	for _, t := range []string{"intents", "events", "notifications"} {
		if err := ensureColumn(db, t, "uid", "TEXT"); err != nil {
			return err
		}
		// databases from before sync: give existing rows an identity so they can be referenced
		if _, err := db.Exec(`UPDATE ` + t + ` SET uid = lower(hex(randomblob(8))) WHERE uid IS NULL`); err != nil {
			return err
		}
	}
	_, err := db.Exec(replicationSchema())
	return err
}

// ---------------------------------------------------------------- local helpers

// Event is one replicated row change.
type Event struct {
	UID   string         `json:"uid"`
	Actor string         `json:"actor"`
	TS    int64          `json:"ts"`
	Tbl   string         `json:"tbl"`
	Row   map[string]any `json:"row"`
}

func (d *DB) KV(key string) string {
	var v string
	_ = d.QueryRow(`SELECT value FROM kv WHERE key=?`, key).Scan(&v)
	return v
}

// SetKV stores a machine-local value. Only the key `plan_approved` is replicated.
func (d *DB) SetKV(key, value string) {
	_, _ = d.Exec(`INSERT INTO kv(key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
}

// Actor is this clone's stable random identity in the event log.
func (d *DB) Actor() string {
	if a := d.KV("sync.actor"); a != "" {
		return a
	}
	var a string
	_ = d.QueryRow(`SELECT lower(hex(randomblob(4)))`).Scan(&a)
	d.SetKV("sync.actor", a)
	return a
}

// Pending returns unsent events (oldest first) and their outbox sequence numbers.
func (d *DB) Pending() ([]Event, []int64) {
	actor := d.Actor() // before opening rows: the pool has a single connection
	rows, err := d.Query(`SELECT seq,uid,tbl,row,ts FROM outbox WHERE state='pending' ORDER BY seq`)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()
	var evs []Event
	var seqs []int64
	for rows.Next() {
		var seq, ts int64
		var uid, tbl, raw string
		if rows.Scan(&seq, &uid, &tbl, &raw, &ts) != nil {
			continue
		}
		ev := Event{UID: uid, Actor: actor, TS: ts, Tbl: tbl}
		if json.Unmarshal([]byte(raw), &ev.Row) != nil {
			continue
		}
		evs, seqs = append(evs, ev), append(seqs, seq)
	}
	return evs, seqs
}

func (d *DB) PendingCount() int {
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM outbox WHERE state='pending'`).Scan(&n)
	return n
}

func (d *DB) MarkSent(seqs []int64, lastSeen string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	for _, s := range seqs {
		if _, err := tx.Exec(`UPDATE outbox SET state='sent' WHERE seq=?`, s); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO kv(key,value) VALUES ('sync.last_seen',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, lastSeen); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------- applying remote events

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			return r
		}
		return -1
	}, s)
}

// val converts a decoded JSON value into a SQL parameter (whole floats become integers; text is sanitized,
// because remote events end up on other people's terminals).
func val(row map[string]any, k string) any {
	switch v := row[k].(type) {
	case nil:
		return nil
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
			return int64(v)
		}
		return v
	case string:
		return clean(v)
	case bool:
		if v {
			return 1
		}
		return 0
	}
	return nil
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func applyEvent(tx execer, ev Event) error {
	r := ev.Row
	v := func(k string) any { return val(r, k) }
	var err error
	switch ev.Tbl {
	case "task_state":
		// worktree is machine-local and deliberately not touched
		_, err = tx.Exec(`INSERT INTO task_state (task_id,status,owner,agent_id,branch,claimed_at,updated_at) VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(task_id) DO UPDATE SET status=excluded.status, owner=excluded.owner, agent_id=excluded.agent_id, branch=excluded.branch,
			claimed_at=excluded.claimed_at, updated_at=excluded.updated_at`,
			v("task_id"), v("status"), v("owner"), v("agent_id"), v("branch"), v("claimed_at"), v("updated_at"))
	case "agents":
		_, err = tx.Exec(`INSERT INTO agents (id,provider,type,developer,repository,branch,status,current_task,capabilities,heartbeat) VALUES (?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET provider=excluded.provider, type=excluded.type, developer=excluded.developer, repository=excluded.repository,
			branch=excluded.branch, status=excluded.status, current_task=excluded.current_task, capabilities=excluded.capabilities, heartbeat=excluded.heartbeat`,
			v("id"), v("provider"), v("type"), v("developer"), v("repository"), v("branch"), v("status"), v("current_task"), v("capabilities"), v("heartbeat"))
	case "intents":
		_, err = tx.Exec(`INSERT INTO intents (uid,agent_id,task_id,kind,target,note,status,created_at) VALUES (?,?,?,?,?,?,?,?)
			ON CONFLICT(uid) DO UPDATE SET status=excluded.status`,
			v("uid"), v("agent_id"), v("task_id"), v("kind"), v("target"), v("note"), v("status"), v("created_at"))
	case "events":
		_, err = tx.Exec(`INSERT OR IGNORE INTO events (uid,ts,type,task_id,agent_id,payload) VALUES (?,?,?,?,?,?)`,
			v("uid"), v("ts"), v("type"), v("task_id"), v("agent_id"), v("payload"))
	case "notifications":
		_, err = tx.Exec(`INSERT OR IGNORE INTO notifications (uid,ts,task_id,agent_id,message,ref) VALUES (?,?,?,?,?,?)`,
			v("uid"), v("ts"), v("task_id"), v("agent_id"), v("message"), v("ref"))
	case "handoffs":
		_, err = tx.Exec(`INSERT OR REPLACE INTO handoffs (task_id,json,ts) VALUES (?,?,?)`, v("task_id"), v("json"), v("ts"))
	case "contract_state":
		// versions only ever move forward
		_, err = tx.Exec(`INSERT INTO contract_state (name,version,task_id,ts,note) VALUES (?,?,?,?,?)
			ON CONFLICT(name) DO UPDATE SET version=excluded.version, task_id=excluded.task_id, ts=excluded.ts, note=excluded.note
			WHERE excluded.version >= contract_state.version`, v("name"), v("version"), v("task_id"), v("ts"), v("note"))
	case "task_ack":
		_, err = tx.Exec(`INSERT INTO task_ack (task_id,contract,version) VALUES (?,?,?)
			ON CONFLICT(task_id,contract) DO UPDATE SET version=excluded.version WHERE excluded.version > task_ack.version`, v("task_id"), v("contract"), v("version"))
	case "kv":
		if r["key"] == "plan_approved" {
			_, err = tx.Exec(`INSERT INTO kv (key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, v("key"), v("value"))
		}
	default:
		// unknown tables are ignored: a newer teammate's events must not break an older client
	}
	return err
}

// claimConflict reports whether replaying a pending claim now would take a task that someone else
// already owns in the shared log. First-writer-wins: the remote log order is the truth.
func claimConflict(tx execer, ev Event) (string, bool) {
	if ev.Tbl != "task_state" {
		return "", false
	}
	mine, _ := ev.Row["agent_id"].(string)
	if mine == "" {
		return "", false
	}
	var cur, status string
	if err := tx.QueryRow(`SELECT COALESCE(agent_id,''), status FROM task_state WHERE task_id=?`, val(ev.Row, "task_id")).Scan(&cur, &status); err != nil {
		return "", false
	}
	if cur != "" && cur != mine && (status == "IN_PROGRESS" || status == "REVIEW") {
		return fmt.Sprintf("%v: %s lost the claim to %s (they pushed first)", ev.Row["task_id"], mine, cur), true
	}
	return "", false
}

// Merge applies other machines' events in log order, then replays this machine's pending events on top
// (dropping claims that lost a race). It records lastSeen so the next sync only reads newer commits.
func (d *DB) Merge(remote []Event, lastSeen string) (applied int, rejected []string, err error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, nil, err
	}
	fail := func(e error) (int, []string, error) { _ = tx.Rollback(); return 0, nil, e }
	if _, err := tx.Exec(`UPDATE sync_ctl SET v='1' WHERE k='applying'`); err != nil {
		return fail(err)
	}
	for _, ev := range remote {
		if err := applyEvent(tx, ev); err != nil {
			return fail(fmt.Errorf("applying %s event %s: %w", ev.Tbl, ev.UID, err))
		}
		applied++
	}
	rows, err := tx.Query(`SELECT seq,uid,tbl,row,ts FROM outbox WHERE state='pending' ORDER BY seq`)
	if err != nil {
		return fail(err)
	}
	type pend struct {
		seq int64
		ev  Event
	}
	var pending []pend
	for rows.Next() {
		var p pend
		var raw string
		if rows.Scan(&p.seq, &p.ev.UID, &p.ev.Tbl, &raw, &p.ev.TS) == nil && json.Unmarshal([]byte(raw), &p.ev.Row) == nil {
			pending = append(pending, p)
		}
	}
	rows.Close()
	type lostClaim struct{ agent, task string }
	var lost []lostClaim
	for _, p := range pending {
		if msg, bad := claimConflict(tx, p.ev); bad {
			rejected = append(rejected, msg)
			a, _ := p.ev.Row["agent_id"].(string)
			tk, _ := p.ev.Row["task_id"].(string)
			lost = append(lost, lostClaim{a, tk})
			if _, err := tx.Exec(`UPDATE outbox SET state='rejected' WHERE seq=?`, p.seq); err != nil {
				return fail(err)
			}
			continue
		}
		if err := applyEvent(tx, p.ev); err != nil {
			return fail(err)
		}
	}
	if _, err := tx.Exec(`UPDATE sync_ctl SET v='0' WHERE k='applying'`); err != nil {
		return fail(err)
	}
	// An agent that lost a claim is no longer working on that task. Done with replication back on, so the
	// correction is published too (otherwise teammates would see it "working" on a task it does not own).
	for _, l := range lost {
		if _, err := tx.Exec(`UPDATE agents SET status='idle', current_task=NULL WHERE id=? AND current_task=?`, l.agent, l.task); err != nil {
			return fail(err)
		}
	}
	if lastSeen != "" {
		if _, err := tx.Exec(`INSERT INTO kv(key,value) VALUES ('sync.last_seen',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, lastSeen); err != nil {
			return fail(err)
		}
	}
	return applied, rejected, tx.Commit()
}
