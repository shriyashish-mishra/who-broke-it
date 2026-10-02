// Package blast computes the blast radius of a change over explicit graph relationships.
// It is deterministic and does not pretend to understand code semantically.
package blast

import (
	"fmt"
	"sort"

	"github.com/shriyashish-mishra/who-broke-it/internal/codegraph"
	"github.com/shriyashish-mishra/who-broke-it/internal/glob"
	"github.com/shriyashish-mishra/who-broke-it/internal/graph"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

type Input struct {
	Tasks      []model.Task
	Contracts  []model.Contract
	Components []model.Component
	State      func(taskID string) (model.TaskState, bool)
	// Optional static import graph and the repo's files; adds code-level dependents (undeclared coupling).
	Code  *codegraph.Graph
	Files []string
}

type ActiveAgent struct{ AgentID, TaskID string }

type Result struct {
	Target        string            `json:"target"`
	Kind          string            `json:"kind"` // contract | task | path | unknown
	DirectTasks   []string          `json:"directTasks"`
	DirectComps   []string          `json:"directComponents"`
	IndirectTasks []string          `json:"indirectTasks"`
	IndirectComps []string          `json:"indirectComponents"`
	Why           map[string]string `json:"why"`
	ActiveAgents  []ActiveAgent     `json:"activeAgents"`
	Risk          string            `json:"risk"` // LOW | MEDIUM | HIGH
	Reasons       []string          `json:"reasons"`
	// CodeDependents are repo files that import the changed code (transitively), found by static scan.
	CodeDependents []string `json:"codeDependents,omitempty"`
}

func has(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func add(s []string, v string) []string {
	if has(s, v) {
		return s
	}
	return append(s, v)
}

// Radius computes what is affected if a contract, a task, or a path changes.
func Radius(in Input, target string) Result {
	tm := map[string]*model.Task{}
	for i := range in.Tasks {
		tm[in.Tasks[i].ID] = &in.Tasks[i]
	}
	why := map[string]string{}
	kind := "unknown"
	var origin, direct, contractNames []string
	var contract *model.Contract
	for i := range in.Contracts {
		if in.Contracts[i].Name == target {
			contract = &in.Contracts[i]
		}
	}
	switch {
	case contract != nil:
		kind = "contract"
		contractNames = []string{contract.Name}
		origin = []string{contract.ProvidedBy}
	case tm[target] != nil:
		kind = "task"
		contractNames = tm[target].Provides
		origin = []string{target}
		for _, t := range in.Tasks {
			if has(t.DependsOn, target) {
				direct = add(direct, t.ID)
				why[t.ID] = "depends on " + target
			}
		}
	default:
		kind = "path"
		for _, t := range in.Tasks {
			if glob.AnyOverlap(t.AllowedPaths, []string{target}) {
				origin = append(origin, t.ID)
				contractNames = append(contractNames, t.Provides...)
			}
		}
		for _, t := range in.Tasks {
			if !has(origin, t.ID) && len(t.RelevantFiles) > 0 && glob.AnyOverlap(t.RelevantFiles, []string{target}) {
				direct = add(direct, t.ID)
				why[t.ID] = "lists " + target + " as a relevant file"
			}
		}
	}
	for _, c := range contractNames {
		for _, t := range in.Tasks {
			if has(t.Consumes, c) && !has(origin, t.ID) {
				direct = add(direct, t.ID)
				if _, ok := why[t.ID]; !ok {
					why[t.ID] = "consumes " + c
				}
			}
		}
	}

	var indirect []string
	for _, d := range direct {
		for _, dep := range graph.Dependents(in.Tasks, d, true) {
			if !has(direct, dep) && !has(origin, dep) && !has(indirect, dep) {
				indirect = append(indirect, dep)
				why[dep] = fmt.Sprintf("depends on %s, which is affected", d)
			}
		}
	}
	// The provider's own dependents are downstream even if they don't consume the contract directly.
	for _, o := range origin {
		for _, dep := range graph.Dependents(in.Tasks, o, true) {
			if !has(direct, dep) && !has(indirect, dep) && !has(origin, dep) {
				indirect = append(indirect, dep)
				why[dep] = "depends on " + o
			}
		}
	}

	var codeDeps []string
	if in.Code != nil {
		var changed []string
		switch kind {
		case "path":
			for _, f := range in.Files {
				if glob.Matches(target, f) {
					changed = append(changed, f)
				}
			}
		default:
			for _, f := range in.Files {
				for _, o := range origin {
					if t := tm[o]; t != nil && glob.MatchesAny(t.AllowedPaths, f) && !isWbi(f) {
						changed = append(changed, f)
						break
					}
				}
			}
		}
		if len(changed) > 0 {
			directFiles := in.Code.ImportedBy(changed, false)
			allFiles := in.Code.ImportedBy(changed, true)
			isDirect := map[string]bool{}
			for _, f := range directFiles {
				isDirect[f] = true
			}
			ownerOf := func(f string) []string {
				var ids []string
				for _, t := range in.Tasks {
					if glob.MatchesAny(t.AllowedPaths, f) && !has(origin, t.ID) {
						ids = append(ids, t.ID)
					}
				}
				return ids
			}
			for _, f := range allFiles {
				for _, id := range ownerOf(f) {
					switch {
					case isDirect[f] && !has(direct, id):
						direct = add(direct, id)
						why[id] = "its code imports " + firstChanged(in.Code, f, changed)
					case !isDirect[f] && !has(direct, id) && !has(indirect, id):
						indirect = append(indirect, id)
						why[id] = "its code transitively imports changed code (" + f + ")"
					}
				}
			}
			codeDeps = allFiles
			if len(codeDeps) > 50 {
				codeDeps = codeDeps[:50]
			}
		}
	}

	compOf := func(ids []string) []string {
		var out []string
		for _, id := range ids {
			if t := tm[id]; t != nil && t.Component != "" {
				out = add(out, t.Component)
			}
		}
		return out
	}
	directComps := compOf(direct)
	indirectComps := compOf(indirect)
	queue := append(append([]string{}, directComps...), compOf(origin)...)
	seen := map[string]bool{}
	for _, q := range queue {
		seen[q] = true
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range in.Components {
			if has(c.DependsOn, cur) && !seen[c.ID] {
				seen[c.ID] = true
				queue = append(queue, c.ID)
				if !has(directComps, c.ID) {
					indirectComps = add(indirectComps, c.ID)
				}
			}
		}
	}

	affected := append(append([]string{}, direct...), indirect...)
	var active []ActiveAgent
	var done []string
	for _, id := range affected {
		st, ok := in.State(id)
		if !ok {
			continue
		}
		if st.AgentID != "" && (st.Status == model.InProgress || st.Status == model.Review) {
			active = append(active, ActiveAgent{st.AgentID, id})
		}
		if st.Status == model.Done {
			done = append(done, id)
		}
	}

	isPublic := contract != nil && contract.Public
	if !isPublic && kind != "path" {
		for _, t := range in.Tasks {
			if has(origin, t.ID) && has(t.Impact, model.ImpactPublicAPI) {
				isPublic = true
			}
		}
	}
	var reasons []string
	if len(active) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d agent(s) are actively building on this", len(active)))
	}
	if len(done) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d already-DONE task(s) may need rework: %s", len(done), join(done)))
	}
	if isPublic {
		reasons = append(reasons, "public contract")
	}
	if len(affected) >= 3 {
		reasons = append(reasons, fmt.Sprintf("%d downstream tasks", len(affected)))
	}
	risk := "LOW"
	if len(affected) >= 1 {
		risk = "MEDIUM"
	}
	if len(done) > 0 || (len(active) > 0 && (isPublic || len(affected) >= 3)) || (isPublic && len(affected) >= 3) {
		risk = "HIGH"
	}
	if len(reasons) == 0 && len(affected) > 0 {
		reasons = append(reasons, "limited downstream impact")
	}
	sort.Strings(codeDeps)
	return Result{CodeDependents: codeDeps, Target: target, Kind: kind, DirectTasks: nz(direct), DirectComps: nz(directComps), IndirectTasks: nz(indirect), IndirectComps: nz(indirectComps),
		Why: why, ActiveAgents: active, Risk: risk, Reasons: nz(reasons)}
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func join(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

func isWbi(f string) bool { return len(f) >= 5 && f[:5] == ".wbi/" }

// firstChanged names a changed file that f imports, for a readable "why".
func firstChanged(g *codegraph.Graph, f string, changed []string) string {
	for _, d := range g.Imports[f] {
		for _, c := range changed {
			if d == c {
				return c
			}
		}
	}
	return "changed code"
}
