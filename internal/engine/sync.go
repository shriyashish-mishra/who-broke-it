package engine

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shriyashish-mishra/who-broke-it/internal/gitx"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
	"github.com/shriyashish-mishra/who-broke-it/internal/replicate"
)

// SyncConfig resolves the effective sync settings. ok=false means sync is off; why says so when it is
// configured but unusable (e.g. the remote does not exist on this machine).
func (e *Engine) SyncConfig() (cfg replicate.Config, ok bool, why string) {
	if os.Getenv("WBI_NO_SYNC") != "" {
		return cfg, false, "disabled by WBI_NO_SYNC"
	}
	p := e.Project()
	if p.Sync == nil {
		return cfg, false, "not configured (run: wbi sync init)"
	}
	cfg.Remote = firstNonEmpty(os.Getenv("WBI_SYNC_REMOTE"), p.Sync.Remote, "origin")
	cfg.Ref = firstNonEmpty(p.Sync.Ref, replicate.DefaultRef)
	if _, has := gitx.Try(e.Root(), "remote", "get-url", cfg.Remote); !has {
		return cfg, false, fmt.Sprintf("git remote %q does not exist here (set WBI_SYNC_REMOTE to another remote)", cfg.Remote)
	}
	return cfg, true, ""
}

// Sync pulls teammates' state, replays local changes on top, and publishes them.
func (e *Engine) Sync() (replicate.Report, error) {
	cfg, ok, why := e.SyncConfig()
	if !ok {
		return replicate.Report{}, model.Errf("sync is off: %s", why)
	}
	rep, err := replicate.Sync(e.DB, e.Root(), cfg)
	if err == nil {
		e.DB.SetKV("sync.last_ms", strconv.FormatInt(nowMs(), 10))
	}
	return rep, err
}

func (e *Engine) warnOnce(key, msg string) {
	if e.warnedOnce[key] {
		return
	}
	e.warnedOnce[key] = true
	e.Warn(msg)
}

// autoSync syncs best-effort: being offline never blocks local work.
func (e *Engine) autoSync(warnRejected bool) replicate.Report {
	rep, err := e.Sync()
	if err != nil {
		if _, off := err.(*replicate.ErrOffline); off {
			e.warnOnce("offline", "wbi: "+err.Error()+" — working from local state; changes sync when you are back online (wbi sync)")
		} else {
			e.warnOnce("syncerr", "wbi: sync failed: "+err.Error())
		}
		return rep
	}
	if warnRejected {
		for _, r := range rep.Rejected {
			e.Warn("wbi: ⚠ " + r)
		}
	}
	return rep
}

// PullIfStale refreshes from the team if the last sync is older than maxAge. Used before read commands.
func (e *Engine) PullIfStale(maxAge time.Duration) {
	if _, ok, _ := e.SyncConfig(); !ok {
		return
	}
	if ms, _ := strconv.ParseInt(e.DB.KV("sync.last_ms"), 10, 64); ms > 0 && time.Since(time.UnixMilli(ms)) < maxAge {
		return
	}
	e.autoSync(true)
}

// Synced wraps a command: pull first so decisions use fresh team state, run it, and (for writes) publish.
// If a claim in the batch lost a race against a teammate, the error says so.
func (e *Engine) Synced(write bool, fn func() error) error {
	if _, ok, _ := e.SyncConfig(); !ok {
		return fn()
	}
	e.autoSync(true)
	err := fn()
	if write {
		if rep := e.autoSync(false); err == nil && len(rep.Rejected) > 0 {
			return model.Errf("%s", strings.Join(rep.Rejected, "\n"))
		}
	}
	return err
}

type SyncInfo struct {
	Configured bool
	Why        string
	Remote     string
	Ref        string
	Actor      string
	Pending    int
	Rejected   int
	LastSeen   string
	LastSyncMs int64
}

func (e *Engine) SyncStatus() SyncInfo {
	cfg, ok, why := e.SyncConfig()
	info := SyncInfo{Configured: ok, Why: why, Remote: cfg.Remote, Ref: cfg.Ref, Actor: e.DB.Actor(), Pending: e.DB.PendingCount(), LastSeen: e.DB.KV("sync.last_seen")}
	_ = e.DB.QueryRow(`SELECT COUNT(*) FROM outbox WHERE state='rejected'`).Scan(&info.Rejected)
	info.LastSyncMs, _ = strconv.ParseInt(e.DB.KV("sync.last_ms"), 10, 64)
	return info
}

// EnableSync writes the sync config into project.json. Commit it so teammates sync automatically.
func (e *Engine) EnableSync(remote, ref string) error {
	p := e.Project()
	p.Sync = &model.SyncConfig{Remote: remote, Ref: ref}
	if p.Sync.Remote == "origin" {
		p.Sync.Remote = ""
	}
	if p.Sync.Ref == replicate.DefaultRef {
		p.Sync.Ref = ""
	}
	return e.Store.SaveProject(p)
}
