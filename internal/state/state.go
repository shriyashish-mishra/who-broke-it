// Package state is the runtime coordination store (SQLite, WAL). It lives in the git *common* dir so
// every worktree on a machine shares it, and it is never committed.
package state

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, single static binary

	"github.com/shriyashish-mishra/who-broke-it/internal/gitx"
)

const schema = `
CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY, provider TEXT, type TEXT, developer TEXT, repository TEXT, branch TEXT,
  status TEXT, current_task TEXT, capabilities TEXT, heartbeat INTEGER
);
CREATE TABLE IF NOT EXISTS task_state (
  task_id TEXT PRIMARY KEY, status TEXT NOT NULL, owner TEXT, agent_id TEXT, branch TEXT, worktree TEXT,
  claimed_at INTEGER, updated_at INTEGER
);
CREATE TABLE IF NOT EXISTS intents (
  id INTEGER PRIMARY KEY AUTOINCREMENT, agent_id TEXT, task_id TEXT, kind TEXT, target TEXT, note TEXT,
  status TEXT DEFAULT 'active', created_at INTEGER
);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER, type TEXT, task_id TEXT, agent_id TEXT, payload TEXT
);
CREATE TABLE IF NOT EXISTS notifications (
  id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER, task_id TEXT, agent_id TEXT, message TEXT, ref TEXT, read INTEGER DEFAULT 0
);
CREATE TABLE IF NOT EXISTS handoffs (task_id TEXT PRIMARY KEY, json TEXT, ts INTEGER);
CREATE TABLE IF NOT EXISTS contract_state (name TEXT PRIMARY KEY, version INTEGER, task_id TEXT, ts INTEGER, note TEXT);
CREATE TABLE IF NOT EXISTS task_ack (task_id TEXT, contract TEXT, version INTEGER, PRIMARY KEY (task_id, contract));
CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT);
`

type DB struct{ *sql.DB }

// PathFor returns <git-common-dir>/wbi/state.db, or .wbi/state/state.db outside git.
func PathFor(root string) string {
	if c := gitx.CommonDir(root); c != "" {
		return filepath.Join(c, "wbi", "state.db")
	}
	return filepath.Join(root, ".wbi", "state", "state.db")
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	if err := migrateReplication(db); err != nil {
		return nil, err
	}
	return &DB{db}, nil
}
