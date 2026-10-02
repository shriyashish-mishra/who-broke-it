package engine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/shriyashish-mishra/who-broke-it/internal/model"
	"github.com/shriyashish-mishra/who-broke-it/internal/replicate"
	"github.com/shriyashish-mishra/who-broke-it/internal/state"
)

func (e *Engine) stateDir() string { return filepath.Dir(state.PathFor(e.Root())) }

// Identity is this clone's signing identity (created on first use).
func (e *Engine) Identity() (replicate.Identity, error) { return replicate.LoadIdentity(e.stateDir()) }

func (e *Engine) writeTeam(t replicate.Team) error {
	t.Version = 1
	if t.Members == nil {
		t.Members = []replicate.Member{}
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return e.Store.WriteText("team.json", string(b)+"\n")
}

// TeamJoin adds this clone's public key to .wbi/team.json under a name. Commit the file via PR to authorize it.
func (e *Engine) TeamJoin(name string, enforce bool) (replicate.Team, error) {
	id, err := e.Identity()
	if err != nil {
		return replicate.Team{}, err
	}
	if name == "" {
		return replicate.Team{}, model.Errf("give your name: wbi team join --name <you>")
	}
	t, _ := replicate.LoadTeam(e.Root())
	key := id.PubB64()
	found := false
	for i := range t.Members {
		if t.Members[i].Key == key {
			t.Members[i].Name, found = name, true
		}
	}
	if !found {
		t.Members = append(t.Members, replicate.Member{Name: name, Key: key})
	}
	if enforce {
		t.Enforce = true
	}
	return t, e.writeTeam(t)
}

// TeamAdd authorizes someone else's public key (they send it to you; `wbi team key` prints it).
func (e *Engine) TeamAdd(name, key string) error {
	t, _ := replicate.LoadTeam(e.Root())
	for i := range t.Members {
		if t.Members[i].Key == key {
			t.Members[i].Name = name
			return e.writeTeam(t)
		}
	}
	t.Members = append(t.Members, replicate.Member{Name: name, Key: key})
	return e.writeTeam(t)
}

func (e *Engine) TeamRemove(name string) error {
	t, _ := replicate.LoadTeam(e.Root())
	var keep []replicate.Member
	for _, m := range t.Members {
		if m.Name != name {
			keep = append(keep, m)
		}
	}
	if len(keep) == len(t.Members) {
		return model.Errf("no team member named %q", name)
	}
	t.Members = keep
	return e.writeTeam(t)
}

func (e *Engine) TeamSetEnforce(on bool) error {
	t, _ := replicate.LoadTeam(e.Root())
	t.Enforce = on
	return e.writeTeam(t)
}

// Compact collapses the replicated log into one snapshot commit (see replicate.Compact).
func (e *Engine) Compact(keepEvents int) (replicate.Report, error) {
	cfg, ok, why := e.SyncConfig()
	if !ok {
		return replicate.Report{}, model.Errf("sync is off: %s", why)
	}
	cfg.Policy = replicate.LoadPolicy(e.Root())
	cfg.StateDir = e.stateDir()
	return replicate.Compact(e.DB, e.Root(), cfg, keepEvents)
}

// Watch syncs every interval until ctx is cancelled, calling onChange whenever teammates' events arrived or
// something needs attention. It is the "live" mode: run it in a spare terminal.
func (e *Engine) Watch(ctx context.Context, interval time.Duration, onChange func(replicate.Report)) error {
	tick := func() {
		rep, err := e.Sync()
		if err != nil {
			e.warnOnce("watch:"+err.Error(), "wbi: "+err.Error())
			return
		}
		if rep.Applied > 0 || len(rep.Rejected) > 0 || len(rep.Quarantined) > 0 {
			onChange(rep)
		}
	}
	tick()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			tick()
		}
	}
}
