// Package engine is the coordination core: agents, claims, work packets, intents, contracts, inbox,
// handoff + verification, and the read-only insight views. It knows nothing about MCP or any vendor.
package engine

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shriyashish-mishra/who-broke-it/internal/blast"
	"github.com/shriyashish-mishra/who-broke-it/internal/gitx"
	"github.com/shriyashish-mishra/who-broke-it/internal/graph"
	"github.com/shriyashish-mishra/who-broke-it/internal/intent"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
	"github.com/shriyashish-mishra/who-broke-it/internal/rules"
	"github.com/shriyashish-mishra/who-broke-it/internal/state"
	"github.com/shriyashish-mishra/who-broke-it/internal/store"
)

// StaleAgentAfter is how long without a heartbeat before an active agent is flagged.
const StaleAgentAfter = 30 * time.Minute

type Engine struct {
	Store *store.Store
	DB    *state.DB
	Cwd   string
	// Warn receives non-fatal notices (e.g. "offline: changes stay local"). Defaults to stderr.
	Warn       func(string)
	warnedOnce map[string]bool
}

// New opens the graph found from cwd and its runtime state.
func New(cwd string) (*Engine, error) {
	s, err := store.Find(cwd)
	if err != nil {
		return nil, err
	}
	return NewWithStore(cwd, s)
}

func NewWithStore(cwd string, s *store.Store) (*Engine, error) {
	db, err := state.Open(state.PathFor(s.Root))
	if err != nil {
		return nil, err
	}
	return &Engine{Store: s, DB: db, Cwd: cwd, Warn: func(m string) { fmt.Fprintln(os.Stderr, m) }, warnedOnce: map[string]bool{}}, nil
}

func (e *Engine) Close() { _ = e.DB.Close() }

func (e *Engine) Root() string { return e.Store.Root }

func (e *Engine) Project() model.Project {
	p, _ := e.Store.Project()
	return p
}

func (e *Engine) Tasks() []model.Task         { return e.Store.Tasks() }
func (e *Engine) Contracts() []model.Contract { return e.Store.Contracts() }

func (e *Engine) Task(id string) (model.Task, error) { return e.Store.Task(strings.ToUpper(id)) }

func nowMs() int64 { return time.Now().UnixMilli() }

// ---------- state ----------

func (e *Engine) State(id string) model.TaskState {
	var s model.TaskState
	err := e.DB.QueryRow(`SELECT task_id,status,COALESCE(owner,''),COALESCE(agent_id,''),COALESCE(branch,''),COALESCE(worktree,''),COALESCE(claimed_at,0),COALESCE(updated_at,0) FROM task_state WHERE task_id=?`, id).
		Scan(&s.TaskID, &s.Status, &s.Owner, &s.AgentID, &s.Branch, &s.Worktree, &s.ClaimedAt, &s.UpdatedAt)
	if err != nil {
		return model.TaskState{TaskID: id, Status: model.Todo}
	}
	return s
}

func (e *Engine) States() map[string]model.TaskState {
	out := map[string]model.TaskState{}
	rows, err := e.DB.Query(`SELECT task_id,status,COALESCE(owner,''),COALESCE(agent_id,''),COALESCE(branch,''),COALESCE(worktree,''),COALESCE(claimed_at,0),COALESCE(updated_at,0) FROM task_state`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s model.TaskState
		if rows.Scan(&s.TaskID, &s.Status, &s.Owner, &s.AgentID, &s.Branch, &s.Worktree, &s.ClaimedAt, &s.UpdatedAt) == nil {
			out[s.TaskID] = s
		}
	}
	return out
}

// SetState applies patch to the task's state and persists it.
func (e *Engine) SetState(id string, patch func(*model.TaskState)) {
	s := e.State(id)
	patch(&s)
	s.TaskID, s.UpdatedAt = id, nowMs()
	_, _ = e.DB.Exec(`INSERT INTO task_state (task_id,status,owner,agent_id,branch,worktree,claimed_at,updated_at) VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(task_id) DO UPDATE SET status=excluded.status, owner=excluded.owner, agent_id=excluded.agent_id, branch=excluded.branch,
		worktree=excluded.worktree, claimed_at=excluded.claimed_at, updated_at=excluded.updated_at`,
		id, s.Status, nullS(s.Owner), nullS(s.AgentID), nullS(s.Branch), nullS(s.Worktree), nullI(s.ClaimedAt), s.UpdatedAt)
}

func nullS(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func nullI(i int64) any {
	if i == 0 {
		return nil
	}
	return i
}

// Display derives READY/BLOCKED for TODO tasks.
func (e *Engine) Display(t model.Task, states map[string]model.TaskState) string {
	return graph.DisplayStatus(t, func(id string) string {
		if s, ok := states[id]; ok {
			return s.Status
		}
		return model.Todo
	})
}

func (e *Engine) Event(typ, taskID, agentID string, payload any) {
	b, _ := json.Marshal(payload)
	_, _ = e.DB.Exec(`INSERT INTO events (ts,type,task_id,agent_id,payload) VALUES (?,?,?,?,?)`, nowMs(), typ, nullS(taskID), nullS(agentID), string(b))
	e.dispatch(typ, taskID, agentID, payload) // only the machine that performed the action notifies, so each event posts once
}

func (e *Engine) Notify(taskID, agentID, message, ref string) {
	_, _ = e.DB.Exec(`INSERT INTO notifications (ts,task_id,agent_id,message,ref) VALUES (?,?,?,?,?)`, nowMs(), nullS(taskID), nullS(agentID), message, ref)
}

// ---------- agents ----------

type AgentOpts struct {
	Agent, As, Session string
	Capabilities       []string
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func (e *Engine) Agents() []model.Agent {
	var out []model.Agent
	rows, err := e.DB.Query(`SELECT id,provider,type,developer,repository,branch,status,COALESCE(current_task,''),COALESCE(capabilities,'[]'),COALESCE(heartbeat,0) FROM agents ORDER BY id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var a model.Agent
		var caps string
		if rows.Scan(&a.ID, &a.Provider, &a.Type, &a.Developer, &a.Repository, &a.Branch, &a.Status, &a.CurrentTask, &caps, &a.Heartbeat) == nil {
			_ = json.Unmarshal([]byte(caps), &a.Capabilities)
			if a.Capabilities == nil {
				a.Capabilities = []string{}
			}
			out = append(out, a)
		}
	}
	return out
}

// ResolveAgent identifies (and registers / heartbeats) the calling agent from flags or WBI_* env vars.
func (e *Engine) ResolveAgent(o AgentOpts) (model.Agent, error) {
	provider := strings.ToLower(firstNonEmpty(o.Agent, os.Getenv("WBI_AGENT"), DetectProvider()))
	if provider == "" {
		return model.Agent{}, model.Errf("Which agent are you? Pass --agent <claude|codex|gemini|cursor|aider|human|...> or set WBI_AGENT.")
	}
	developer := firstNonEmpty(o.As, os.Getenv("WBI_DEVELOPER"), gitx.UserName(e.Root()))
	if developer == "" {
		if u, err := user.Current(); err == nil {
			developer = u.Username
		}
	}
	id := provider + "@" + slug(developer)
	if sess := firstNonEmpty(o.Session, os.Getenv("WBI_SESSION")); sess != "" {
		id += "#" + slug(sess)
	}
	typ := "coding-agent"
	if provider == "human" {
		typ = "human"
	}
	caps, _ := json.Marshal(append([]string{}, o.Capabilities...))
	_, err := e.DB.Exec(`INSERT INTO agents (id,provider,type,developer,repository,branch,status,current_task,capabilities,heartbeat) VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET branch=excluded.branch, heartbeat=excluded.heartbeat,
		capabilities=CASE WHEN excluded.capabilities='[]' THEN agents.capabilities ELSE excluded.capabilities END`,
		id, provider, typ, developer, filepath.Base(e.Root()), gitx.CurrentBranch(e.Cwd), "idle", nil, string(caps), nowMs())
	if err != nil {
		return model.Agent{}, err
	}
	for _, a := range e.Agents() {
		if a.ID == id {
			return a, nil
		}
	}
	return model.Agent{}, model.Errf("failed to register agent %s", id)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func (e *Engine) SetAgent(id, status, task string) {
	_, _ = e.DB.Exec(`UPDATE agents SET status=?, current_task=?, heartbeat=? WHERE id=?`, status, nullS(task), nowMs(), id)
}

// ---------- claiming ----------

// ExplainBlocked says exactly why a task is blocked and what it is waiting for.
func (e *Engine) ExplainBlocked(id string) string {
	t, err := e.Task(id)
	if err != nil {
		return err.Error()
	}
	states := e.States()
	contracts := e.Contracts()
	lines := []string{t.ID + " BLOCKED", "", "Waiting for:"}
	for _, d := range t.DependsOn {
		st := states[d].Status
		if st == "" {
			st = model.Todo
		}
		if st == model.Done {
			continue
		}
		dep, _ := e.Task(d)
		var shared []string
		for _, cn := range t.Consumes {
			for _, c := range contracts {
				if c.Name == cn && c.ProvidedBy == d {
					shared = append(shared, c.Name)
				}
			}
		}
		lines = append(lines, fmt.Sprintf("  %s %s (%s)", d, dep.Title, st))
		if len(shared) > 0 {
			lines = append(lines, fmt.Sprintf("    Reason: %s has not been finalized.", strings.Join(shared, ", ")))
		} else {
			lines = append(lines, "    Reason: it must be completed first.")
		}
	}
	return strings.Join(lines, "\n")
}

type ClaimResult struct {
	Task  model.Task
	Agent model.Agent
	State model.TaskState
}

func (e *Engine) Claim(taskID string, o AgentOpts, force bool) (*ClaimResult, error) {
	t, err := e.Task(taskID)
	if err != nil {
		return nil, err
	}
	agent, err := e.ResolveAgent(o)
	if err != nil {
		return nil, err
	}
	st := e.State(t.ID)
	if st.Status == model.Done {
		return nil, model.Errf("%s is already DONE.", t.ID)
	}
	if st.AgentID != "" && st.AgentID != agent.ID && st.Status != model.Todo {
		return nil, model.Errf("%s is already claimed by %s (you are %s; pass --agent/--as or set WBI_AGENT/WBI_DEVELOPER to act as someone else).", t.ID, st.AgentID, agent.ID)
	}
	if e.Display(t, e.States()) == model.Blocked && !force {
		return nil, model.Errf("%s\n\n(use --force to start against unfinished dependencies)", e.ExplainBlocked(t.ID))
	}
	e.SetState(t.ID, func(s *model.TaskState) {
		if s.Status != model.Review {
			s.Status = model.InProgress
		}
		s.Owner, s.AgentID = agent.Developer, agent.ID
		if s.Branch == "" {
			s.Branch = "wbi/" + t.ID
		}
		if s.ClaimedAt == 0 {
			s.ClaimedAt = nowMs()
		}
	})
	e.SetAgent(agent.ID, "working", t.ID)
	e.Event("claimed", t.ID, agent.ID, map[string]any{})
	return &ClaimResult{t, agent, e.State(t.ID)}, nil
}

func (e *Engine) Release(taskID string) error {
	t, err := e.Task(taskID)
	if err != nil {
		return err
	}
	st := e.State(t.ID)
	if st.AgentID != "" {
		e.SetAgent(st.AgentID, "idle", "")
	}
	e.SetState(t.ID, func(s *model.TaskState) { s.Status, s.Owner, s.AgentID = model.Todo, "", "" })
	e.ReleaseIntents(t.ID)
	e.Event("released", t.ID, "", map[string]any{})
	return nil
}

type StartResult struct {
	Task        model.Task
	Agent       model.Agent
	Branch      string
	Workdir     string
	ContextFile string
	Context     string
	Env         map[string]string
}

// Start claims the task if needed, prepares the branch (or worktree) and writes the work packet.
func (e *Engine) Start(taskID string, o AgentOpts, worktree, force bool) (*StartResult, error) {
	t, err := e.Task(taskID)
	if err != nil {
		return nil, err
	}
	agent, err := e.ResolveAgent(o)
	if err != nil {
		return nil, err
	}
	if e.State(t.ID).AgentID != agent.ID {
		if _, err := e.Claim(t.ID, o, force); err != nil {
			return nil, err
		}
	}
	branch := e.State(t.ID).Branch
	workdir := e.Root()
	if worktree {
		path := filepath.Join(e.Root(), ".wbi", "worktrees", t.ID)
		EnsureIgnored(e.Root(), ".wbi/worktrees/")
		if _, err := os.Stat(path); err != nil {
			var gerr error
			if gitx.RefExists(e.Root(), branch) {
				_, gerr = gitx.Git(e.Root(), "worktree", "add", path, branch)
			} else {
				_, gerr = gitx.Git(e.Root(), "worktree", "add", "-b", branch, path, e.Project().BaseBranch)
			}
			if gerr != nil {
				return nil, model.Errf("could not create worktree (does the repo have a commit?): %v", gerr)
			}
		}
		workdir = path
	}
	e.SetState(t.ID, func(s *model.TaskState) {
		s.Worktree = ""
		if worktree {
			s.Worktree = workdir
		}
	})
	ctx, err := e.Context(t.ID)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(workdir, ".wbi", "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, t.ID+".context.md")
	if err := os.WriteFile(file, []byte(ctx), 0o644); err != nil {
		return nil, err
	}
	e.Event("started", t.ID, agent.ID, map[string]string{"workdir": workdir})
	return &StartResult{t, agent, branch, workdir, file, ctx, map[string]string{"WBI_AGENT": agent.Provider, "WBI_DEVELOPER": agent.Developer}}, nil
}

// ---------- contracts ----------

func (e *Engine) EffectiveVersion(c model.Contract) int {
	var v int
	if err := e.DB.QueryRow(`SELECT version FROM contract_state WHERE name=?`, c.Name).Scan(&v); err == nil && v > c.Version {
		return v
	}
	return c.Version
}

func (e *Engine) ackedVersion(t model.Task, contract string) int {
	var v int
	if err := e.DB.QueryRow(`SELECT version FROM task_ack WHERE task_id=? AND contract=?`, t.ID, contract).Scan(&v); err == nil {
		return v
	}
	if v, ok := t.ContractVersion[contract]; ok {
		return v
	}
	return 1
}

type Stale struct {
	Contract string
	From, To int
	By, Note string
}

// StaleContracts lists consumed contracts whose live version is newer than what the task last acknowledged.
func (e *Engine) StaleContracts(t model.Task) []Stale {
	var out []Stale
	contracts := e.Contracts()
	for _, name := range t.Consumes {
		for _, c := range contracts {
			if c.Name != name {
				continue
			}
			eff, acked := e.EffectiveVersion(c), e.ackedVersion(t, name)
			if eff > acked {
				by, note := c.ProvidedBy, ""
				var tid, n sql.NullString
				if e.DB.QueryRow(`SELECT task_id, note FROM contract_state WHERE name=?`, name).Scan(&tid, &n) == nil {
					if tid.Valid {
						by = tid.String
					}
					note = n.String
				}
				out = append(out, Stale{name, acked, eff, by, note})
			}
		}
	}
	return append(out, e.externalStale(t)...)
}

func (e *Engine) Ack(taskID string) error {
	t, err := e.Task(taskID)
	if err != nil {
		return err
	}
	for _, s := range e.StaleContracts(t) {
		_, _ = e.DB.Exec(`INSERT OR REPLACE INTO task_ack (task_id,contract,version) VALUES (?,?,?)`, t.ID, s.Contract, s.To)
	}
	_, _ = e.DB.Exec(`UPDATE notifications SET read=1 WHERE task_id=? AND ref LIKE 'contract:%'`, t.ID)
	e.Event("acked", t.ID, e.State(t.ID).AgentID, map[string]any{})
	return nil
}

func (e *Engine) Blast(target string) blast.Result {
	if strings.Contains(target, ":") {
		if r, ok := e.blastExternal(target); ok {
			return r
		}
	}
	states := e.States()
	code, files := e.CodeGraph()
	return blast.Radius(blast.Input{
		Tasks: e.Tasks(), Contracts: e.Contracts(), Components: e.Store.Components(),
		State: func(id string) (model.TaskState, bool) { s, ok := states[id]; return s, ok },
		Code:  code, Files: files,
	}, target)
}

// ---------- intents ----------

func (e *Engine) Intents(all bool) []model.Intent {
	q := `SELECT id,agent_id,task_id,kind,target,COALESCE(note,''),status,created_at FROM intents `
	if !all {
		q += `WHERE status='active' `
	}
	rows, err := e.DB.Query(q + `ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []model.Intent
	for rows.Next() {
		var i model.Intent
		if rows.Scan(&i.ID, &i.AgentID, &i.TaskID, &i.Kind, &i.Target, &i.Note, &i.Status, &i.CreatedAt) == nil {
			out = append(out, i)
		}
	}
	return out
}

func (e *Engine) ReleaseIntents(taskID string) {
	_, _ = e.DB.Exec(`UPDATE intents SET status='released' WHERE task_id=? AND status='active'`, taskID)
}

type DeclareResult struct {
	Intent    *model.Intent
	Conflicts []model.Conflict
	Blocked   bool
}

// DeclareIntent declares what an agent is about to change BEFORE it edits, and returns conflicts.
// Blocked intents are not registered.
func (e *Engine) DeclareIntent(o AgentOpts, taskID, kind, target, note string) (*DeclareResult, error) {
	t, err := e.Task(taskID)
	if err != nil {
		return nil, err
	}
	agent, err := e.ResolveAgent(o)
	if err != nil {
		return nil, err
	}
	tasks, states := e.Tasks(), e.States()
	var others []model.Intent
	for _, i := range e.Intents(false) {
		if i.AgentID != agent.ID {
			others = append(others, i)
		}
	}
	var protected []string
	for _, x := range tasks {
		if contains(x.Impact, model.ImpactMigration) || contains(x.Impact, model.ImpactInfra) {
			protected = append(protected, x.AllowedPaths...)
		}
	}
	conflicts := intent.Detect(kind, target, intent.Ctx{
		Task: t, Tasks: tasks, Contracts: e.Contracts(), AgentID: agent.ID, Active: others, ProtectedPaths: protected,
		State: func(id string) (model.TaskState, bool) { s, ok := states[id]; return s, ok },
		ConsumersActive: func(c string) []intent.ActiveConsumer {
			var out []intent.ActiveConsumer
			for _, x := range tasks {
				if s := states[x.ID]; contains(x.Consumes, c) && s.AgentID != "" && s.Status == model.InProgress {
					out = append(out, intent.ActiveConsumer{TaskID: x.ID, AgentID: s.AgentID})
				}
			}
			return out
		},
	})
	if states[t.ID].AgentID != agent.ID {
		conflicts = append(conflicts, model.Conflict{Code: "NOT_CLAIMED", Severity: model.Warn, Message: fmt.Sprintf("%s is not claimed by %s. Run `wbi claim %s` first.", t.ID, agent.ID, t.ID)})
	}
	blocked := false
	for _, c := range conflicts {
		if c.Severity == model.Block {
			blocked = true
		}
	}
	res := &DeclareResult{Conflicts: conflicts, Blocked: blocked}
	if !blocked {
		r, err := e.DB.Exec(`INSERT INTO intents (agent_id,task_id,kind,target,note,status,created_at) VALUES (?,?,?,?,?,?,?)`, agent.ID, t.ID, kind, target, note, "active", nowMs())
		if err != nil {
			return nil, err
		}
		id, _ := r.LastInsertId()
		for _, i := range e.Intents(false) {
			if i.ID == id {
				i := i
				res.Intent = &i
			}
		}
	}
	var codes []string
	for _, c := range conflicts {
		codes = append(codes, c.Code)
	}
	typ := "intent"
	if blocked {
		typ = "intent_blocked"
	}
	e.Event(typ, t.ID, agent.ID, map[string]any{"kind": kind, "target": target, "conflicts": codes})
	return res, nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// IntentCollisions returns overlapping active intents from different agents.
func (e *Engine) IntentCollisions() [][2]model.Intent {
	act := e.Intents(false)
	var out [][2]model.Intent
	for i := 0; i < len(act); i++ {
		for j := i + 1; j < len(act); j++ {
			a, b := act[i], act[j]
			if a.AgentID == b.AgentID {
				continue
			}
			if model.IsWrite(a.Kind) && model.IsWrite(b.Kind) && globOverlap(a.Target, b.Target) {
				out = append(out, [2]model.Intent{a, b})
			} else if a.Kind == model.ChangeContract && b.Kind == model.ChangeContract && a.Target == b.Target {
				out = append(out, [2]model.Intent{a, b})
			}
		}
	}
	return out
}

// ---------- inbox, decisions, approvals ----------

type Notice struct {
	ID     int64
	TS     int64
	TaskID string
	Msg    string
}

// Inbox returns unread notifications for an agent (their own plus those for tasks they own); empty agentID = all.
func (e *Engine) Inbox(agentID string, markRead bool) []Notice {
	owned := map[string]bool{}
	for _, t := range e.Tasks() {
		if agentID != "" && e.State(t.ID).AgentID == agentID {
			owned[t.ID] = true
		}
	}
	rows, err := e.DB.Query(`SELECT id,ts,COALESCE(task_id,''),COALESCE(agent_id,''),message FROM notifications WHERE read=0 ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Notice
	var ids []string
	for rows.Next() {
		var n Notice
		var aid string
		if rows.Scan(&n.ID, &n.TS, &n.TaskID, &aid, &n.Msg) != nil {
			continue
		}
		if agentID == "" || aid == agentID || owned[n.TaskID] {
			out = append(out, n)
			ids = append(ids, fmt.Sprint(n.ID))
		}
	}
	rows.Close()
	if markRead && len(ids) > 0 {
		_, _ = e.DB.Exec(`UPDATE notifications SET read=1 WHERE id IN (` + strings.Join(ids, ",") + `)`)
	}
	return out
}

func (e *Engine) Decide(title, why string, taskIDs []string) (model.Decision, error) {
	d := model.Decision{ID: fmt.Sprintf("ADR-%03d", len(e.Store.Decisions())+1), Title: title, Why: why, Status: "accepted", Tasks: append([]string{}, taskIDs...), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := e.Store.SaveDecision(d); err != nil {
		return d, err
	}
	for _, id := range taskIDs {
		t, err := e.Task(id)
		if err != nil {
			return d, err
		}
		if !contains(t.Decisions, d.ID) {
			t.Decisions = append(t.Decisions, d.ID)
			if err := e.Store.SaveTask(t); err != nil {
				return d, err
			}
		}
	}
	e.Event("decision", "", "", map[string]string{"id": d.ID, "title": title})
	return d, nil
}

// Approve is the human sign-off that moves a REVIEW task to DONE.
func (e *Engine) Approve(taskID, by string) error {
	t, err := e.Task(taskID)
	if err != nil {
		return err
	}
	st := e.State(t.ID)
	if st.Status != model.Review {
		return model.Errf("%s is %s; only tasks in REVIEW can be approved.", t.ID, st.Status)
	}
	e.SetState(t.ID, func(s *model.TaskState) { s.Status = model.Done })
	if st.AgentID != "" {
		e.SetAgent(st.AgentID, "idle", "")
	}
	e.Event("approved", t.ID, "", map[string]string{"by": by})
	e.NotifyUnblocked(t.ID)
	return nil
}

// NotifyUnblocked tells owners of tasks that just became READY.
func (e *Engine) NotifyUnblocked(doneID string) {
	states := e.States()
	for _, t := range e.Tasks() {
		if contains(t.DependsOn, doneID) && e.Display(t, states) == model.Ready {
			e.Notify(t.ID, states[t.ID].AgentID, fmt.Sprintf("%s is now READY (%s is DONE).", t.ID, doneID), "ready")
		}
	}
}

func (e *Engine) GetHandoff(taskID string) *model.Handoff {
	var js string
	if e.DB.QueryRow(`SELECT json FROM handoffs WHERE task_id=?`, taskID).Scan(&js) == nil {
		var h model.Handoff
		if json.Unmarshal([]byte(js), &h) == nil {
			return &h
		}
	}
	for _, h := range e.Store.Handoffs() {
		if h.TaskID == taskID {
			h := h
			return &h
		}
	}
	return nil
}

// ---------- agent work packet ----------

func code(ss []string) string {
	if len(ss) == 0 {
		return "(none)"
	}
	var out []string
	for _, s := range ss {
		out = append(out, "`"+s+"`")
	}
	return strings.Join(out, ", ")
}

// Context renders the work packet for a task: scope, contracts, rules, criteria, upstream handoffs.
func (e *Engine) Context(taskID string) (string, error) {
	t, err := e.Task(taskID)
	if err != nil {
		return "", err
	}
	states := e.States()
	st, claimed := states[t.ID]
	contracts := e.Contracts()
	rs := rules.ForTask(rules.Load(e.Root()), t)
	downstream := graph.Dependents(e.Tasks(), t.ID, false)
	or := func(s, d string) string {
		if s == "" {
			return d
		}
		return s
	}
	var L []string
	p := func(format string, a ...any) { L = append(L, fmt.Sprintf(format, a...)) }
	p("# %s: %s", t.ID, t.Title)
	p("")
	p("**Goal:** %s", t.Goal)
	p("")
	p("**Owner:** %s  |  **Executor:** %s  |  **Layer:** %s  |  **Branch:** %s", or(st.Owner, "(unclaimed)"), or(st.AgentID, "(unclaimed)"), t.Layer, or(st.Branch, "wbi/"+t.ID))
	p("")
	if len(t.Requirements) > 0 {
		reqs := e.Store.Requirements()
		var rr []string
		for _, r := range t.Requirements {
			txt := r
			for _, q := range reqs {
				if q.ID == r {
					txt = fmt.Sprintf("%s (%s)", r, q.Text)
				}
			}
			rr = append(rr, txt)
		}
		p("**Requirements:** %s", strings.Join(rr, "; "))
		p("")
	}
	if t.Layer == "architecture" {
		p("## All project contracts (reviewing these is this task)")
		if len(contracts) == 0 {
			p("- none yet")
		}
		for _, c := range contracts {
			pub := ""
			if c.Public {
				pub = ", public"
			}
			p("- **%s** v%d (%s%s), owned by %s: `%s`", c.Name, e.EffectiveVersion(c), c.Kind, pub, c.ProvidedBy, c.Shape)
		}
		p("")
	}
	p("## Depends on")
	if len(t.DependsOn) == 0 {
		p("- nothing")
	}
	for _, d := range t.DependsOn {
		dt, _ := e.Task(d)
		s := states[d].Status
		if s == "" {
			s = model.Todo
		}
		p("- %s %s: %s", d, dt.Title, s)
	}
	p("")
	p("## Scope")
	p("- Allowed paths: %s", code(t.AllowedPaths))
	p("- Restricted paths: %s", code(t.RestrictedPaths))
	rf := code(t.RelevantFiles)
	if len(t.RelevantFiles) == 0 {
		rf = "(none found)"
	}
	p("- Relevant files: %s", rf)
	p("")
	p("## Contracts")
	listContracts := func(names []string, label string) {
		for _, n := range names {
			for _, c := range contracts {
				if c.Name == n {
					pub := ""
					if c.Public {
						pub = ", public"
					}
					p("- %s **%s** v%d (%s%s): `%s` %s", label, c.Name, e.EffectiveVersion(c), c.Kind, pub, c.Shape, c.Description)
				}
			}
		}
	}
	listContracts(t.Provides, "PROVIDES")
	listContracts(t.Consumes, "CONSUMES")
	if len(t.Provides) == 0 && len(t.Consumes) == 0 {
		p("- none")
	}
	if stale := e.StaleContracts(t); len(stale) > 0 {
		p("")
		p("## ⚠ Contract changes since you started (read before continuing)")
		for _, s := range stale {
			note := ""
			if s.Note != "" {
				note = ": " + s.Note
			}
			p("- **%s** v%d → v%d by %s%s. Adapt, then run `wbi ack %s`.", s.Contract, s.From, s.To, s.By, note, t.ID)
		}
	}
	p("")
	p("## Acceptance criteria")
	for i, a := range t.Acceptance {
		how := "_(attest in handoff)_"
		if a.Check != nil {
			how = "_(auto-checked)_"
		}
		if a.Human {
			how = "_(human sign-off: do not attest; your handoff goes to REVIEW)_"
		}
		p("%d. %s %s", i+1, a.Text, how)
	}
	if len(t.Risks) > 0 {
		p("")
		p("## Known risks")
		for _, r := range t.Risks {
			p("- %s", r)
		}
	}
	if len(t.Impact) > 0 {
		p("")
		p("**High-impact (%s):** a human must approve before this task is DONE.", strings.Join(t.Impact, ", "))
	}
	if len(downstream) > 0 {
		p("")
		p("## Downstream tasks waiting on you")
		p("%s", strings.Join(downstream, ", "))
	}
	p("")
	p("## Constitution (rules that apply to this task)")
	for _, r := range rs {
		mc := ""
		if r.Forbid != nil {
			mc = " _(machine-checked)_"
		}
		p("- **%s** %s%s", r.ID, r.Text, mc)
	}
	var ups []*model.Handoff
	for _, d := range t.DependsOn {
		if h := e.GetHandoff(d); h != nil {
			ups = append(ups, h)
		}
	}
	if len(ups) > 0 {
		p("")
		p("## Handoffs from upstream tasks")
		for _, h := range ups {
			p("### %s by %s", h.TaskID, h.AgentID)
			p("%s", h.Implemented)
			files := h.ChangedFiles
			if len(files) > 10 {
				files = files[:10]
			}
			p("- Changed: %s", strings.Join(files, ", "))
			for _, c := range h.ContractChanges {
				p("- Contract %s → v%d: %s", c.Name, c.Version, c.Note)
			}
			if h.Tests.Ran {
				sum := h.Tests.Summary
				if sum == "" {
					sum = "failed"
					if h.Tests.Passed != nil && *h.Tests.Passed {
						sum = "passed"
					}
				}
				p("- Tests: %s", sum)
			}
			for _, l := range h.Limitations {
				p("- Known limitation: %s", l)
			}
		}
	}
	var nearby []model.Intent
	for _, i := range e.Intents(false) {
		if i.TaskID != t.ID {
			nearby = append(nearby, i)
		}
	}
	if len(nearby) > 0 {
		p("")
		p("## Other agents are currently working on")
		for _, i := range nearby {
			p("- %s (%s): %s `%s`", i.AgentID, i.TaskID, i.Kind, i.Target)
		}
	}
	_ = claimed
	p("")
	p("## How to coordinate")
	p("Before editing code:")
	p("  wbi intent declare MODIFY <path-or-glob> --task %s     (or CREATE / DELETE / CHANGE_CONTRACT <name> / DEPEND_ON <task>)", t.ID)
	p("When finished:")
	p(`  wbi handoff %s --summary "what you built" --tests "<test command>" --attest 1,2`, t.ID)
	p("MCP-capable agents can call the wbi_* tools instead (see docs/INTEGRATING.md).")
	return strings.Join(L, "\n") + "\n", nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
