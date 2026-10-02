// Package model defines the Engineering Graph schema. Everything under `.wbi/` is one of these,
// serialized as JSON with the same field names the TypeScript prototype used.
package model

import (
	"encoding/json"
	"fmt"
)

// WbiError is a user-facing error: the CLI prints it without a stack trace.
type WbiError struct{ Msg string }

func (e *WbiError) Error() string { return e.Msg }

// Errf builds a user-facing error.
func Errf(format string, a ...any) error { return &WbiError{Msg: fmt.Sprintf(format, a...)} }

// SyncConfig turns on cross-machine sync. It is committed in project.json so teammates get it by pulling.
type SyncConfig struct {
	Remote string `json:"remote,omitempty"` // git remote to publish the event log to (default origin)
	Ref    string `json:"ref,omitempty"`    // ref holding the log (default refs/wbi/sync)
}

// NotifyConfig sends selected events to Slack, Discord or any JSON webhook. The URL is a secret and is read
// from the environment variable named by URLEnv; it is never stored in the repository.
type NotifyConfig struct {
	Type   string   `json:"type"`             // slack | discord | webhook
	URLEnv string   `json:"urlEnv"`           // name of the env var holding the webhook URL
	Events []string `json:"events,omitempty"` // default: contract_changed, verify_failed, verify_review, handoff, approved
}

type Project struct {
	Version    int            `json:"version"`
	Name       string         `json:"name"`
	Goal       string         `json:"goal"`
	CreatedAt  string         `json:"createdAt"`
	BaseBranch string         `json:"baseBranch"`
	TestCmd    string         `json:"testCmd,omitempty"`
	Sync       *SyncConfig    `json:"sync,omitempty"`
	Notify     []NotifyConfig `json:"notify,omitempty"`
}

// Check is a deterministic acceptance check: command | file-exists | contains.
type Check struct {
	Type    string `json:"type"`
	Cmd     string `json:"cmd,omitempty"`
	Path    string `json:"path,omitempty"`
	Pattern string `json:"pattern,omitempty"`
}

// Criterion is an acceptance criterion. Without a Check it must be attested in the handoff.
type Criterion struct {
	Text  string `json:"text"`
	Check *Check `json:"check,omitempty"`
	// Human marks criteria only a person can sign off ("the team agreed…"). An agent cannot attest them;
	// the task goes to REVIEW and `wbi approve` is the sign-off.
	Human bool `json:"human,omitempty"`
}

// UnmarshalJSON accepts either a bare string or an object, so hand/agent-written plans can be terse.
func (c *Criterion) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*c = Criterion{Text: s}
		return nil
	}
	type alias Criterion
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*c = Criterion(a)
	return nil
}

// Impact tags mark work that needs a human to approve it.
const (
	ImpactMigration = "migration"
	ImpactSecurity  = "security"
	ImpactPublicAPI = "public-api"
	ImpactInfra     = "infra"
)

// Task is an agent work packet: everything an agent needs to start without rediscovering the project.
type Task struct {
	ID              string         `json:"id"`
	Title           string         `json:"title"`
	Goal            string         `json:"goal"`
	Layer           string         `json:"layer"`
	Component       string         `json:"component"`
	Requirements    []string       `json:"requirements"`
	DependsOn       []string       `json:"dependsOn"`
	AllowedPaths    []string       `json:"allowedPaths"`
	RestrictedPaths []string       `json:"restrictedPaths"`
	RelevantFiles   []string       `json:"relevantFiles"`
	Provides        []string       `json:"provides"`
	Consumes        []string       `json:"consumes"`
	Acceptance      []Criterion    `json:"acceptance"`
	Risks           []string       `json:"risks"`
	Impact          []string       `json:"impact"`
	Decisions       []string       `json:"decisions"`
	Effort          int            `json:"effort"`
	ContractVersion map[string]int `json:"contractVersions"`
	CreatedAt       string         `json:"createdAt"`
}

func nz[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Normalize replaces nil slices/maps with empty ones so JSON output has [] not null.
func (t *Task) Normalize() {
	t.Requirements, t.DependsOn = nz(t.Requirements), nz(t.DependsOn)
	t.AllowedPaths, t.RestrictedPaths, t.RelevantFiles = nz(t.AllowedPaths), nz(t.RestrictedPaths), nz(t.RelevantFiles)
	t.Provides, t.Consumes, t.Acceptance = nz(t.Provides), nz(t.Consumes), nz(t.Acceptance)
	t.Risks, t.Impact, t.Decisions = nz(t.Risks), nz(t.Impact), nz(t.Decisions)
	if t.ContractVersion == nil {
		t.ContractVersion = map[string]int{}
	}
	if t.Effort == 0 {
		t.Effort = 2
	}
}

type HistoryEntry struct {
	Version int    `json:"version"`
	TaskID  string `json:"taskId"`
	At      string `json:"at"`
	Note    string `json:"note"`
}

// Contract is a named interface with exactly one owning (providing) task.
type Contract struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"` // type | http | event | function
	Version     int            `json:"version"`
	ProvidedBy  string         `json:"providedBy"`
	Shape       string         `json:"shape"`
	Description string         `json:"description"`
	Public      bool           `json:"public"`
	History     []HistoryEntry `json:"history"`
}

func (c *Contract) Normalize() {
	c.History = nz(c.History)
	if c.Version == 0 {
		c.Version = 1
	}
	if c.Kind == "" {
		c.Kind = "type"
	}
}

type Component struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Paths       []string `json:"paths"`
	DependsOn   []string `json:"dependsOn"`
}

func (c *Component) Normalize() { c.Paths, c.DependsOn = nz(c.Paths), nz(c.DependsOn) }

type Requirement struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// Layers that must be implemented for the requirement to be truly satisfied.
	Layers []string `json:"layers"`
}

type Decision struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Why       string   `json:"why"`
	Status    string   `json:"status"` // accepted | proposed
	Tasks     []string `json:"tasks"`
	CreatedAt string   `json:"createdAt"`
}

type Plan struct {
	Components        []Component   `json:"components"`
	Requirements      []Requirement `json:"requirements"`
	Contracts         []Contract    `json:"contracts"`
	Tasks             []Task        `json:"tasks"`
	Decisions         []Decision    `json:"decisions"`
	ConstitutionRules []string      `json:"constitutionRules,omitempty"`
}

// Stored task statuses. READY and BLOCKED are derived from the DAG, see graph.DisplayStatus.
const (
	Todo       = "TODO"
	InProgress = "IN_PROGRESS"
	Review     = "REVIEW"
	Done       = "DONE"
	Ready      = "READY"
	Blocked    = "BLOCKED"
)

type TaskState struct {
	TaskID    string
	Status    string
	Owner     string
	AgentID   string
	Branch    string
	Worktree  string
	ClaimedAt int64
	UpdatedAt int64
}

type Agent struct {
	ID           string   `json:"id"`
	Provider     string   `json:"provider"`
	Type         string   `json:"type"`
	Developer    string   `json:"developer"`
	Repository   string   `json:"repository"`
	Branch       string   `json:"branch"`
	Status       string   `json:"status"`
	CurrentTask  string   `json:"currentTask"`
	Capabilities []string `json:"capabilities"`
	Heartbeat    int64    `json:"heartbeat"`
}

// Intent kinds.
const (
	Create         = "CREATE"
	Modify         = "MODIFY"
	Delete         = "DELETE"
	ChangeContract = "CHANGE_CONTRACT"
	DependOn       = "DEPEND_ON"
)

type Intent struct {
	ID        int64  `json:"id"`
	AgentID   string `json:"agentId"`
	TaskID    string `json:"taskId"`
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	Note      string `json:"note"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"createdAt"`
}

// IsWrite reports whether the intent kind edits paths.
func IsWrite(kind string) bool { return kind == Create || kind == Modify || kind == Delete }

type Severity string

const (
	Info  Severity = "info"
	Warn  Severity = "warn"
	Block Severity = "block"
)

type Conflict struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	AgentID  string   `json:"agentId,omitempty"`
	TaskID   string   `json:"taskId,omitempty"`
}

type ContractChange struct {
	Name    string `json:"name"`
	Note    string `json:"note"`
	Version int    `json:"version"`
}

type TestEvidence struct {
	Ran     bool   `json:"ran"`
	Passed  *bool  `json:"passed,omitempty"`
	Command string `json:"command,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type Handoff struct {
	TaskID          string           `json:"taskId"`
	AgentID         string           `json:"agentId"`
	At              string           `json:"at"`
	Implemented     string           `json:"implemented"`
	ChangedFiles    []string         `json:"changedFiles"`
	ContractChanges []ContractChange `json:"contractChanges"`
	Tests           TestEvidence     `json:"tests"`
	Limitations     []string         `json:"limitations"`
	Attested        []int            `json:"attested"`
	Affected        []string         `json:"affected"`
	Branch          string           `json:"branch,omitempty"`
	PR              int              `json:"pr,omitempty"`
	Commits         []string         `json:"commits"`
}

func (h *Handoff) Normalize() {
	h.ChangedFiles, h.ContractChanges = nz(h.ChangedFiles), nz(h.ContractChanges)
	h.Limitations, h.Attested, h.Affected, h.Commits = nz(h.Limitations), nz(h.Attested), nz(h.Affected), nz(h.Commits)
}

// NormalizePlan fills defaults so sparse hand/agent-written plans are valid.
func (p *Plan) Normalize(now string) {
	for i := range p.Tasks {
		t := &p.Tasks[i]
		if t.Title == "" {
			t.Title = t.ID
		}
		if t.Goal == "" {
			t.Goal = t.Title
		}
		if t.Layer == "" {
			t.Layer = "backend"
		}
		if t.Component == "" {
			t.Component = "core"
		}
		if t.CreatedAt == "" {
			t.CreatedAt = now
		}
		t.Normalize()
		for _, c := range append(append([]string{}, t.Provides...), t.Consumes...) {
			if _, ok := t.ContractVersion[c]; !ok {
				t.ContractVersion[c] = 1
			}
		}
	}
	for i := range p.Contracts {
		p.Contracts[i].Normalize()
	}
	for i := range p.Components {
		p.Components[i].Normalize()
	}
	for i := range p.Requirements {
		p.Requirements[i].Layers = nz(p.Requirements[i].Layers)
	}
	for i := range p.Decisions {
		d := &p.Decisions[i]
		d.Tasks = nz(d.Tasks)
		if d.Status == "" {
			d.Status = "proposed"
		}
		if d.CreatedAt == "" {
			d.CreatedAt = now
		}
	}
	p.Components, p.Requirements, p.Contracts, p.Tasks, p.Decisions =
		nz(p.Components), nz(p.Requirements), nz(p.Contracts), nz(p.Tasks), nz(p.Decisions)
}
