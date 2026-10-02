package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shriyashish-mishra/who-broke-it/internal/gitx"
	"github.com/shriyashish-mishra/who-broke-it/internal/glob"
	"github.com/shriyashish-mishra/who-broke-it/internal/graph"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
	"github.com/shriyashish-mishra/who-broke-it/internal/rules"
)

// ---------------------------------------------------------------- status

type ActiveTask struct {
	Task         model.Task `json:"task"`
	AgentID      string     `json:"agentId"`
	Branch       string     `json:"branch"`
	Ahead        int        `json:"ahead"`
	PR           int        `json:"pr,omitempty"`
	Stale        bool       `json:"stale"`
	HeartbeatAgo int64      `json:"heartbeatAgoMs"`
}

type BlockedTask struct {
	Task       model.Task `json:"task"`
	WaitingFor []string   `json:"waitingFor"`
}

type ReviewTask struct {
	Task          model.Task `json:"task"`
	NeedsApproval bool       `json:"needsApproval"`
}

type StatusView struct {
	Project struct {
		Name string `json:"name"`
		Goal string `json:"goal"`
	} `json:"project"`
	Progress struct {
		Done  int `json:"done"`
		Total int `json:"total"`
		Pct   int `json:"pct"`
	} `json:"progress"`
	Active    []ActiveTask  `json:"active"`
	Ready     []model.Task  `json:"ready"`
	Blocked   []BlockedTask `json:"blocked"`
	Review    []ReviewTask  `json:"review"`
	Done      []model.Task  `json:"done"`
	Attention []string      `json:"attention"`
	Agents    []model.Agent `json:"agents"`
}

func prNumber(branch, cwd string) int {
	if os.Getenv("WBI_NO_GH") != "" {
		return 0
	}
	c := exec.Command("gh", "pr", "list", "--head", branch, "--json", "number", "--limit", "1")
	c.Dir = cwd
	done := make(chan []byte, 1)
	go func() { out, _ := c.Output(); done <- out }()
	select {
	case out := <-done:
		var r []struct {
			Number int `json:"number"`
		}
		if json.Unmarshal(out, &r) == nil && len(r) > 0 {
			return r[0].Number
		}
	case <-time.After(3 * time.Second):
		_ = c.Process.Kill()
	}
	return 0
}

// Status builds the mission-control view.
func (e *Engine) Status() StatusView {
	tasks, states := e.Tasks(), e.States()
	var v StatusView
	proj := e.Project()
	v.Project.Name, v.Project.Goal = proj.Name, proj.Goal
	disp := func(t model.Task) string { return e.Display(t, states) }
	total, doneW := 0, 0
	for _, t := range tasks {
		total += t.Effort
		if disp(t) == model.Done {
			doneW += t.Effort
			v.Progress.Done++
		}
	}
	v.Progress.Total = len(tasks)
	if total > 0 {
		v.Progress.Pct = int(float64(doneW)/float64(total)*100 + 0.5)
	}
	agents := e.Agents()
	v.Agents = agents
	now := nowMs()
	base := proj.BaseBranch
	for _, t := range tasks {
		st := states[t.ID]
		switch disp(t) {
		case model.InProgress:
			a := ActiveTask{Task: t, AgentID: st.AgentID, Branch: st.Branch}
			if st.Branch != "" && gitx.RefExists(e.Root(), st.Branch) {
				a.Ahead = len(gitx.CommitsAhead(e.Root(), base, st.Branch))
			}
			if h := e.GetHandoff(t.ID); h != nil && h.PR > 0 {
				a.PR = h.PR
			} else if st.Branch != "" {
				a.PR = prNumber(st.Branch, e.Root())
			}
			for _, ag := range agents {
				if ag.ID == st.AgentID {
					a.HeartbeatAgo = now - ag.Heartbeat
					a.Stale = a.HeartbeatAgo > StaleAgentAfter.Milliseconds()
				}
			}
			v.Active = append(v.Active, a)
		case model.Ready:
			v.Ready = append(v.Ready, t)
		case model.Blocked:
			b := BlockedTask{Task: t}
			for _, d := range t.DependsOn {
				if states[d].Status != model.Done {
					b.WaitingFor = append(b.WaitingFor, d)
				}
			}
			v.Blocked = append(v.Blocked, b)
		case model.Review:
			v.Review = append(v.Review, ReviewTask{t, len(t.Impact) > 0})
		case model.Done:
			v.Done = append(v.Done, t)
		}
	}

	type group struct {
		to    int
		by    string
		tasks []string
	}
	groups := map[string]*group{}
	var order []string
	for _, t := range tasks {
		if disp(t) == model.Done {
			continue
		}
		for _, s := range e.StaleContracts(t) {
			g := groups[s.Contract]
			if g == nil {
				g = &group{to: s.To, by: s.By}
				groups[s.Contract] = g
				order = append(order, s.Contract)
			}
			g.tasks = append(g.tasks, t.ID)
		}
	}
	for _, c := range order {
		g := groups[c]
		v.Attention = append(v.Attention, fmt.Sprintf("⚠ %s changed (v%d, by %s) → %d downstream task(s) affected: %s", c, g.to, g.by, len(g.tasks), strings.Join(g.tasks, ", ")))
	}
	for _, p := range e.IntentCollisions() {
		a, b := p[0], p[1]
		v.Attention = append(v.Attention, fmt.Sprintf("⚠ Intent collision: %s %s %s (%s) ↔ %s %s %s (%s)", a.AgentID, a.Kind, a.Target, a.TaskID, b.AgentID, b.Kind, b.Target, b.TaskID))
	}
	for _, a := range v.Active {
		if a.Stale {
			v.Attention = append(v.Attention, fmt.Sprintf("⚠ %s has not been heard from in %d min (%s)", a.AgentID, a.HeartbeatAgo/60000, a.Task.ID))
		}
	}
	for _, r := range v.Review {
		if r.NeedsApproval {
			v.Attention = append(v.Attention, fmt.Sprintf("⚠ %s (%s) awaits human approval: wbi approve %s", r.Task.ID, strings.Join(r.Task.Impact, ", "), r.Task.ID))
		}
	}
	for _, i := range graph.Validate(tasks, e.Contracts()) {
		v.Attention = append(v.Attention, fmt.Sprintf("⚠ Plan %s: %s", i.Severity, i.Message))
	}
	return v
}

// ---------------------------------------------------------------- simulate

type Conflict struct {
	A   string `json:"a"`
	B   string `json:"b"`
	Why string `json:"why"`
}

type Simulation struct {
	Tasks          int            `json:"tasks"`
	Agents         int            `json:"agents"`
	AgentsAssumed  bool           `json:"agentsAssumed"`
	Parallelizable int            `json:"parallelizable"`
	CriticalPath   []string       `json:"criticalPath"`
	Waves          [][]model.Task `json:"waves"`
	Conflicts      []Conflict     `json:"conflicts"`
	MissingDeps    []string       `json:"missingDeps"`
	NeedsApproval  []string       `json:"needsApproval"`
	MinRounds      int            `json:"minRounds"`
}

// Simulate dry-runs the plan: waves, critical path, overlapping scopes, missing dependencies.
// agentsFlag <= 0 means "use registered agents, else assume 3".
func (e *Engine) Simulate(agentsFlag int) Simulation {
	tasks := e.Tasks()
	registered := 0
	for _, a := range e.Agents() {
		if a.Provider != "human" {
			registered++
		}
	}
	agents := agentsFlag
	assumed := false
	if agents <= 0 {
		agents = registered
		if agents == 0 {
			agents, assumed = 3, true
		}
	}
	ws := graph.Waves(tasks)
	tm := map[string]model.Task{}
	for _, t := range tasks {
		tm[t.ID] = t
	}
	s := Simulation{Tasks: len(tasks), Agents: agents, AgentsAssumed: assumed, CriticalPath: graph.CriticalPath(tasks)}
	for i := 0; i < len(tasks); i++ {
		for j := i + 1; j < len(tasks); j++ {
			a, b := tasks[i], tasks[j]
			if !graph.Concurrent(tasks, a.ID, b.ID) {
				continue
			}
			for _, pa := range a.AllowedPaths {
				for _, pb := range b.AllowedPaths {
					if glob.Overlap(pa, pb) {
						s.Conflicts = append(s.Conflicts, Conflict{a.ID, b.ID, fmt.Sprintf("%s overlaps %s", pa, pb)})
					}
				}
			}
		}
	}
	contracts := e.Contracts()
	for _, i := range graph.Validate(tasks, contracts) {
		s.MissingDeps = append(s.MissingDeps, i.Message)
	}
	for _, t := range tasks {
		for _, c := range t.Consumes {
			for _, k := range contracts {
				if k.Name != c || k.ProvidedBy == t.ID || contains(graph.Ancestors(tasks, t.ID), k.ProvidedBy) {
					continue
				}
				dup := false
				for _, m := range s.MissingDeps {
					if strings.Contains(m, t.ID) && strings.Contains(m, c) {
						dup = true
					}
				}
				if !dup {
					s.MissingDeps = append(s.MissingDeps, fmt.Sprintf("%s consumes %s but does not depend on %s", t.ID, c, k.ProvidedBy))
				}
			}
		}
	}
	for _, w := range ws {
		if len(w) > 1 {
			s.Parallelizable += len(w)
		}
		var wt []model.Task
		for _, id := range w {
			wt = append(wt, tm[id])
		}
		s.Waves = append(s.Waves, wt)
		s.MinRounds += (len(w) + agents - 1) / agents
	}
	for _, t := range tasks {
		if len(t.Impact) > 0 {
			s.NeedsApproval = append(s.NeedsApproval, t.ID)
		}
	}
	return s
}

// ---------------------------------------------------------------- drift

type LayerState struct {
	Layer string   `json:"layer"`
	State string   `json:"state"` // done | wip | todo | missing
	Tasks []string `json:"tasks"`
}

type ProductDrift struct {
	Req       string       `json:"req"`
	Text      string       `json:"text"`
	Layers    []LayerState `json:"layers"`
	Satisfied bool         `json:"satisfied"`
}

type ContextDrift struct {
	Task     string `json:"task"`
	Contract string `json:"contract"`
	By       string `json:"by"`
	From     int    `json:"from"`
	To       int    `json:"to"`
}

type DriftReport struct {
	Product      []ProductDrift    `json:"product"`
	Architecture []rules.Violation `json:"architecture"`
	Context      []ContextDrift    `json:"context"`
	Coupling     []Coupling        `json:"coupling"`
}

// Drift reports product (requirement × layer), architecture (constitution) and context (stale contract) drift.
func (e *Engine) Drift() DriftReport {
	tasks, states := e.Tasks(), e.States()
	var r DriftReport
	for _, req := range e.Store.Requirements() {
		pd := ProductDrift{Req: req.ID, Text: req.Text, Satisfied: true}
		for _, layer := range req.Layers {
			var ids []string
			all, wip := true, false
			for _, t := range tasks {
				if contains(t.Requirements, req.ID) && t.Layer == layer {
					ids = append(ids, t.ID)
					switch states[t.ID].Status {
					case model.Done:
					case model.InProgress, model.Review:
						all, wip = false, true
					default:
						all = false
					}
				}
			}
			state := "todo"
			switch {
			case len(ids) == 0:
				state = "missing"
			case all:
				state = "done"
			case wip:
				state = "wip"
			}
			if state != "done" {
				pd.Satisfied = false
			}
			pd.Layers = append(pd.Layers, LayerState{layer, state, ids})
		}
		r.Product = append(r.Product, pd)
	}
	rs := rules.Load(e.Root())
	r.Architecture = rules.ScanRepo(e.Root(), rs)
	// Also inspect unmerged work: drift on a task branch is cheapest to fix before it merges.
	for _, t := range tasks {
		st := states[t.ID]
		if st.Branch == "" || st.Status == model.Done || !gitx.RefExists(e.Root(), st.Branch) {
			continue
		}
		for _, v := range rules.CheckLines(rs, gitx.AddedLines(e.Root(), e.Project().BaseBranch, st.Branch)) {
			v.Branch = st.Branch
			r.Architecture = append(r.Architecture, v)
		}
	}
	for _, t := range tasks {
		if states[t.ID].Status == model.Done {
			continue
		}
		for _, s := range e.StaleContracts(t) {
			r.Context = append(r.Context, ContextDrift{t.ID, s.Contract, s.By, s.From, s.To})
		}
	}
	r.Coupling = e.UndeclaredCoupling(nil)
	return r
}

// ---------------------------------------------------------------- blame / why

type BlameEntry struct {
	SHA      string `json:"sha"`
	Who      string `json:"who"`
	IsAgent  bool   `json:"isAgent"`
	Provider string `json:"provider,omitempty"`
	TaskID   string `json:"taskId,omitempty"`
	PR       int    `json:"pr,omitempty"`
	TS       int64  `json:"ts"`
	Subject  string `json:"subject"`
	Ref      string `json:"ref,omitempty"`
}

var (
	taskTrailerRe  = regexp.MustCompile(`(?mi)^WBI-Task:\s*(.+)$`)
	agentTrailerRe = regexp.MustCompile(`(?mi)^WBI-Agent:\s*(.+)$`)
	coAuthorRe     = regexp.MustCompile(`(?mi)^Co-Authored-By:\s*(.+)$`)
	taskInTextRe   = regexp.MustCompile(`\bTASK-\d{3,}\b`)
	prParenRe      = regexp.MustCompile(`\(#(\d+)\)`)
	prMergeRe      = regexp.MustCompile(`(?i)pull request #(\d+)`)
	agentNameRes   = regexp.MustCompile(`(?i)claude|codex|openai|chatgpt|gemini|cursor|aider|copilot|opencode`)
)

// ParseCommit extracts task, agent and PR attribution from a commit.
func ParseCommit(c gitx.CommitInfo) BlameEntry {
	text := c.Subject + "\n" + c.Body
	e := BlameEntry{SHA: c.SHA, Who: c.Author, TS: c.TS, Subject: c.Subject, Ref: c.Ref}
	if len(e.SHA) > 7 {
		e.SHA = e.SHA[:7]
	}
	if m := taskTrailerRe.FindStringSubmatch(text); m != nil {
		e.TaskID = strings.ToUpper(strings.TrimSpace(m[1]))
	} else if m := taskInTextRe.FindString(text); m != "" {
		e.TaskID = m
	}
	if m := agentTrailerRe.FindStringSubmatch(text); m != nil {
		e.Provider = strings.SplitN(strings.TrimSpace(m[1]), "@", 2)[0]
	} else if m := coAuthorRe.FindStringSubmatch(text); m != nil {
		e.Provider = strings.ToLower(agentNameRes.FindString(m[1]))
	}
	if m := prParenRe.FindStringSubmatch(text); m != nil {
		e.PR, _ = strconv.Atoi(m[1])
	} else if m := prMergeRe.FindStringSubmatch(text); m != nil {
		e.PR, _ = strconv.Atoi(m[1])
	}
	if e.Provider != "" {
		e.IsAgent, e.Who = true, e.Provider
	}
	return e
}

type ContractAlert struct {
	Contract string   `json:"contract"`
	Task     string   `json:"task"`
	Affected []string `json:"affected"`
	Version  int      `json:"version"`
}

type BlameResult struct {
	Entries        []BlameEntry    `json:"entries"`
	ContractAlerts []ContractAlert `json:"contractAlerts"`
	Active         []model.Intent  `json:"active"`
}

// Blame answers: who changed this path, under which task, and what does it affect? It includes unmerged branches.
func (e *Engine) Blame(path string, limit int) BlameResult {
	var r BlameResult
	for _, c := range gitx.LogFor(e.Root(), path, 30, nil, true) {
		if len(r.Entries) >= limit {
			break
		}
		r.Entries = append(r.Entries, ParseCommit(c))
	}
	tasks := e.Tasks()
	rows, err := e.DB.Query(`SELECT task_id, payload FROM events WHERE type='contract_changed' ORDER BY id DESC`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tid, payload string
			if rows.Scan(&tid, &payload) != nil {
				continue
			}
			var p struct {
				Contract string   `json:"contract"`
				Version  int      `json:"version"`
				Affected []string `json:"affected"`
			}
			if json.Unmarshal([]byte(payload), &p) != nil {
				continue
			}
			for _, t := range tasks {
				if t.ID == tid && glob.AnyOverlap(t.AllowedPaths, []string{path}) {
					dup := false
					for _, a := range r.ContractAlerts {
						if a.Contract == p.Contract {
							dup = true
						}
					}
					if !dup {
						r.ContractAlerts = append(r.ContractAlerts, ContractAlert{p.Contract, tid, p.Affected, p.Version})
					}
				}
			}
		}
	}
	target := path
	if strings.HasSuffix(path, "/") {
		target = path + "**"
	}
	for _, i := range e.Intents(false) {
		if model.IsWrite(i.Kind) && glob.Overlap(i.Target, target) {
			r.Active = append(r.Active, i)
		}
	}
	return r
}

type WhyResult struct {
	Introducing *BlameEntry
	Latest      *BlameEntry
	Task        *model.Task
	Reqs        []model.Requirement
	Decisions   []model.Decision
	Executor    string
	Owner       string
}

// Why traces a path back: requirement → task → agent → commit → PR.
func (e *Engine) Why(path string) WhyResult {
	var r WhyResult
	all := gitx.LogFor(e.Root(), path, 200, []string{"--follow"}, true)
	if len(all) > 0 {
		i, l := ParseCommit(all[len(all)-1]), ParseCommit(all[0])
		r.Introducing, r.Latest = &i, &l
	}
	tasks := e.Tasks()
	if r.Introducing != nil && r.Introducing.TaskID != "" {
		for i := range tasks {
			if tasks[i].ID == r.Introducing.TaskID {
				r.Task = &tasks[i]
			}
		}
	}
	if r.Task == nil {
		for i := range tasks {
			if glob.AnyOverlap(tasks[i].AllowedPaths, []string{path}) {
				r.Task = &tasks[i]
				break
			}
		}
	}
	if r.Task != nil {
		for _, q := range e.Store.Requirements() {
			if contains(r.Task.Requirements, q.ID) {
				r.Reqs = append(r.Reqs, q)
			}
		}
		for _, d := range e.Store.Decisions() {
			if contains(r.Task.Decisions, d.ID) || contains(d.Tasks, r.Task.ID) {
				r.Decisions = append(r.Decisions, d)
			}
		}
		st := e.State(r.Task.ID)
		r.Executor, r.Owner = st.AgentID, st.Owner
	}
	return r
}
