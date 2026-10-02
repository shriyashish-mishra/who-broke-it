package engine

import (
	"sort"

	"github.com/shriyashish-mishra/who-broke-it/internal/codegraph"
	"github.com/shriyashish-mishra/who-broke-it/internal/gitx"
	"github.com/shriyashish-mishra/who-broke-it/internal/glob"
	"github.com/shriyashish-mishra/who-broke-it/internal/graph"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

// CodeGraph scans the working tree's imports (JS/TS, Python, Go). Best effort; see package codegraph.
func (e *Engine) CodeGraph() (*codegraph.Graph, []string) {
	files := gitx.LsFiles(e.Root())
	return codegraph.Build(e.Root(), files), files
}

// Coupling is code in one task's area importing code in another's, with no declared dependency between them.
type Coupling struct {
	From     string `json:"from"`     // the task whose code does the importing
	To       string `json:"to"`       // the task that owns the imported code
	FromFile string `json:"fromFile"` // an example importing file
	ToFile   string `json:"toFile"`   // an example imported file
	Count    int    `json:"count"`    // how many import edges between the two areas
}

// ownersByFile maps a file to the single task whose allowed paths cover it ("" if none or ambiguous).
func ownersByFile(tasks []model.Task, f string) string {
	owner := ""
	for _, t := range tasks {
		if len(t.AllowedPaths) == 0 || !glob.MatchesAny(t.AllowedPaths, f) || isWbiPath(f) {
			continue
		}
		if owner != "" {
			return "" // ambiguous ownership: do not guess
		}
		owner = t.ID
	}
	return owner
}

func isWbiPath(f string) bool { return len(f) >= 5 && f[:5] == ".wbi/" }

// UndeclaredCoupling finds imports that cross task areas without a dependency edge in the graph.
// If restrict is non-nil, only importing files in it are considered (used by the merge gate).
func (e *Engine) UndeclaredCoupling(restrict map[string]bool) []Coupling {
	g, _ := e.CodeGraph()
	tasks := e.Tasks()
	byPair := map[[2]string]*Coupling{}
	for from, deps := range g.Imports {
		if restrict != nil && !restrict[from] {
			continue
		}
		a := ownersByFile(tasks, from)
		if a == "" {
			continue
		}
		for _, d := range deps {
			b := ownersByFile(tasks, d)
			if b == "" || b == a {
				continue
			}
			if contains(graph.Ancestors(tasks, a), b) { // declared: a depends (transitively) on b
				continue
			}
			k := [2]string{a, b}
			c := byPair[k]
			if c == nil {
				c = &Coupling{From: a, To: b, FromFile: from, ToFile: d}
				byPair[k] = c
			}
			c.Count++
		}
	}
	out := make([]Coupling, 0, len(byPair))
	for _, c := range byPair {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}
