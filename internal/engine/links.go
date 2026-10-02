package engine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shriyashish-mishra/who-broke-it/internal/blast"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
	"github.com/shriyashish-mishra/who-broke-it/internal/store"
)

// Multi-repo links: the smallest useful foundation. Repos are linked by name and local path; a task consumes a
// contract of another repo as "<link>:<Contract>". Everything downstream (staleness, work-packet banner, ack,
// status, drift) reuses the single-repo machinery because an external contract is just a contract whose
// version is read from the linked repo's committed .wbi/contracts. Impact propagates one hop per repo;
// a chain (backend → sdk → web) is expressed by each repo linking to its upstream.

func (e *Engine) Links() []model.RepoLink { return e.Project().Links }

func (e *Engine) linkStore(name string) (*store.Store, *model.RepoLink, error) {
	for _, l := range e.Links() {
		if l.Name != name {
			continue
		}
		p := l.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(e.Root(), p)
		}
		s, err := store.Find(p)
		if err != nil {
			return nil, &l, model.Errf("linked repo %q: no wbi graph at %s (is it checked out and `wbi init`ed?)", name, p)
		}
		return s, &l, nil
	}
	return nil, nil, model.Errf("no linked repo named %q (wbi link add %s <path>)", name, name)
}

// LinkAdd records a link. The path must contain a wbi graph so mistakes surface immediately.
func (e *Engine) LinkAdd(name, path, role string) error {
	if name == "" || strings.ContainsAny(name, ": /") {
		return model.Errf("link names are short words without ':' or '/' (they namespace contracts: <name>:<Contract>)")
	}
	if name == e.Project().Name {
		return model.Errf("a repo cannot link to itself")
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(e.Root(), abs)
	}
	if _, err := store.Find(abs); err != nil {
		return model.Errf("no wbi graph at %s: run `wbi init` in that repo first", abs)
	}
	p := e.Project()
	var keep []model.RepoLink
	for _, l := range p.Links {
		if l.Name != name {
			keep = append(keep, l)
		}
	}
	p.Links = append(keep, model.RepoLink{Name: name, Path: path, Role: role})
	return e.Store.SaveProject(p)
}

func (e *Engine) LinkRemove(name string) error {
	p := e.Project()
	var keep []model.RepoLink
	for _, l := range p.Links {
		if l.Name != name {
			keep = append(keep, l)
		}
	}
	if len(keep) == len(p.Links) {
		return model.Errf("no linked repo named %q", name)
	}
	p.Links = keep
	return e.Store.SaveProject(p)
}

// ExternalContract resolves "<link>:<Contract>" from the linked repo's committed graph.
func (e *Engine) ExternalContract(ref string) (model.Contract, error) {
	i := strings.Index(ref, ":")
	if i < 0 {
		return model.Contract{}, model.Errf("%q is not a cross-repo reference", ref)
	}
	s, _, err := e.linkStore(ref[:i])
	if err != nil {
		return model.Contract{}, err
	}
	for _, c := range s.Contracts() {
		if c.Name == ref[i+1:] {
			return c, nil
		}
	}
	return model.Contract{}, model.Errf("repo %q has no contract %q", ref[:i], ref[i+1:])
}

type ExternalUse struct {
	Task     string `json:"task"`
	Ref      string `json:"ref"`
	Acked    int    `json:"acked"`
	Current  int    `json:"current"`
	Stale    bool   `json:"stale"`
	Problem  string `json:"problem,omitempty"`
	Provider string `json:"provider,omitempty"` // task id in the other repo that owns the contract
}

// ExternalUses lists every cross-repo contract this repo's tasks consume, with drift.
func (e *Engine) ExternalUses() []ExternalUse {
	var out []ExternalUse
	for _, t := range e.Tasks() {
		for _, c := range t.Consumes {
			if !strings.Contains(c, ":") {
				continue
			}
			u := ExternalUse{Task: t.ID, Ref: c, Acked: e.ackedVersion(t, c)}
			ct, err := e.ExternalContract(c)
			if err != nil {
				u.Problem = err.Error()
			} else {
				u.Current, u.Provider = ct.Version, ct.ProvidedBy
				u.Stale = ct.Version > u.Acked
			}
			out = append(out, u)
		}
	}
	return out
}

// externalStale feeds cross-repo drift into StaleContracts so banners, status, drift and `wbi ack` just work.
func (e *Engine) externalStale(t model.Task) []Stale {
	var out []Stale
	for _, c := range t.Consumes {
		if !strings.Contains(c, ":") {
			continue
		}
		ct, err := e.ExternalContract(c)
		if err != nil {
			continue
		}
		if acked := e.ackedVersion(t, c); ct.Version > acked {
			note := ""
			if n := len(ct.History); n > 0 {
				note = ct.History[n-1].Note
			}
			out = append(out, Stale{Contract: c, From: acked, To: ct.Version, By: c[:strings.Index(c, ":")] + "/" + ct.ProvidedBy, Note: note})
		}
	}
	return out
}

// blastExternal answers "what in THIS repo is affected if backend:PaymentStatus changes?".
func (e *Engine) blastExternal(ref string) (blast.Result, bool) {
	ct, err := e.ExternalContract(ref)
	if err != nil {
		return blast.Result{}, false
	}
	states := e.States()
	synthetic := model.Contract{Name: ref, Kind: ct.Kind, Version: ct.Version, ProvidedBy: "(" + ref[:strings.Index(ref, ":")] + ")", Public: ct.Public}
	r := blast.Radius(blast.Input{
		Tasks: e.Tasks(), Contracts: []model.Contract{synthetic}, Components: e.Store.Components(),
		State: func(id string) (model.TaskState, bool) { s, ok := states[id]; return s, ok },
	}, ref)
	r.Kind = "external-contract"
	return r, true
}

type LinkImpact struct {
	Repo  string   `json:"repo"`
	Tasks []string `json:"tasks"`
	Stale []string `json:"stale"` // tasks there that have not acknowledged the current version
	Error string   `json:"error,omitempty"`
}

// ImpactOnLinks: which tasks in linked repos consume THIS repo's contract? Reads their committed graphs, so it
// works for any repo you have checked out next to this one, with no runtime state or network.
func (e *Engine) ImpactOnLinks(contract string) ([]LinkImpact, error) {
	c, found := model.Contract{}, false
	for _, x := range e.Contracts() {
		if x.Name == contract {
			c, found = x, true
		}
	}
	if !found {
		return nil, model.Errf("unknown contract %q in this repo", contract)
	}
	me := e.Project().Name
	ref := me + ":" + contract
	var out []LinkImpact
	for _, l := range e.Links() {
		s, _, err := e.linkStore(l.Name)
		if err != nil {
			out = append(out, LinkImpact{Repo: l.Name, Error: err.Error()})
			continue
		}
		// the other repo may link back to us under a different name; match by our project name or its link to our path
		names := map[string]bool{ref: true}
		if p, perr := s.Project(); perr == nil {
			for _, back := range p.Links {
				bp := back.Path
				if !filepath.IsAbs(bp) {
					bp = filepath.Join(s.Root, bp)
				}
				if same(bp, e.Root()) {
					names[back.Name+":"+contract] = true
				}
			}
		}
		li := LinkImpact{Repo: l.Name}
		for _, t := range s.Tasks() {
			for _, cs := range t.Consumes {
				if names[cs] {
					li.Tasks = append(li.Tasks, t.ID)
					if v, ok := t.ContractVersion[cs]; ok && v < c.Version && cs != "" {
						li.Stale = append(li.Stale, t.ID)
					}
				}
			}
		}
		sort.Strings(li.Tasks)
		out = append(out, li)
	}
	return out, nil
}

func same(a, b string) bool {
	ea, _ := filepath.EvalSymlinks(a)
	eb, _ := filepath.EvalSymlinks(b)
	if ea == "" || eb == "" {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	if fi, err := os.Stat(ea); err == nil {
		if fj, err := os.Stat(eb); err == nil {
			return os.SameFile(fi, fj)
		}
	}
	return ea == eb
}
