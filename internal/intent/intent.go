// Package intent detects conflicts between a declared intent and everything else going on.
// It is a pure function: given the world (tasks, contracts, other intents), it returns conflicts.
package intent

import (
	"fmt"

	"wbi/internal/glob"
	"wbi/internal/model"
)

type ActiveConsumer struct{ TaskID, AgentID string }

// Ctx is the snapshot an intent is checked against.
type Ctx struct {
	Task      model.Task
	Tasks     []model.Task
	Contracts []model.Contract
	AgentID   string
	// Active holds other agents' active intents.
	Active []model.Intent
	State  func(taskID string) (model.TaskState, bool)
	// ConsumersActive returns agents currently building on a contract.
	ConsumersActive func(contract string) []ActiveConsumer
	// ProtectedPaths are high-impact areas (migrations, infra).
	ProtectedPaths []string
}

func c(code string, sev model.Severity, msg, agent, task string) model.Conflict {
	return model.Conflict{Code: code, Severity: sev, Message: msg, AgentID: agent, TaskID: task}
}

func taskByID(tasks []model.Task, id string) *model.Task {
	for i := range tasks {
		if tasks[i].ID == id {
			return &tasks[i]
		}
	}
	return nil
}

// Detect compares a new intent against the task's scope, soft ownership, other agents' intents and the
// contract registry. Severity Block means "do not proceed without a human decision".
func Detect(kind, target string, x Ctx) []model.Conflict {
	var out []model.Conflict
	task := x.Task

	switch kind {
	case model.DependOn:
		if taskByID(x.Tasks, target) == nil {
			return append(out, c("UNKNOWN_TASK", model.Block, target+" does not exist.", "", ""))
		}
		if st, ok := x.State(target); !ok || st.Status != model.Done {
			status := model.Todo
			if ok {
				status = st.Status
			}
			out = append(out, c("UNMET_DEPENDENCY", model.Warn, fmt.Sprintf("%s is %s. Work depending on it will be blocked or built against a moving target.", target, status), "", target))
		}
		return out

	case model.ChangeContract:
		var ct *model.Contract
		for i := range x.Contracts {
			if x.Contracts[i].Name == target {
				ct = &x.Contracts[i]
			}
		}
		if ct == nil {
			return []model.Conflict{c("UNKNOWN_CONTRACT", model.Block, fmt.Sprintf("Contract %s is not in the registry.", target), "", "")}
		}
		if ct.ProvidedBy != task.ID {
			out = append(out, c("CONTRACT_NOT_OWNED", model.Block, fmt.Sprintf("%s is owned by %s. Only its provider may change it; ask %s or propose a decision.", target, ct.ProvidedBy, ct.ProvidedBy), "", ct.ProvidedBy))
		}
		for _, i := range x.Active {
			if i.Kind == model.ChangeContract && i.Target == target {
				out = append(out, c("CONTRACT_COLLISION", model.Block, fmt.Sprintf("%s (%s) is already changing %s.", i.AgentID, i.TaskID, target), i.AgentID, i.TaskID))
			}
		}
		for _, a := range x.ConsumersActive(target) {
			if a.AgentID != x.AgentID {
				out = append(out, c("ACTIVE_CONSUMER", model.Warn, fmt.Sprintf("%s is building %s on %s right now; they will be notified when you hand off.", a.AgentID, a.TaskID, target), a.AgentID, a.TaskID))
			}
		}
		return out
	}

	// CREATE / MODIFY / DELETE on paths
	if !glob.MatchesAny(task.AllowedPaths, target) && !glob.AnyOverlap(task.AllowedPaths, []string{target}) {
		var owner *model.Task
		for i := range x.Tasks {
			if x.Tasks[i].ID != task.ID && glob.AnyOverlap(x.Tasks[i].AllowedPaths, []string{target}) {
				owner = &x.Tasks[i]
				break
			}
		}
		ownerID, ownerNote := "", ""
		if owner != nil {
			ownerID = owner.ID
		}
		if glob.MatchesAny(task.RestrictedPaths, target) || glob.AnyOverlap(task.RestrictedPaths, []string{target}) {
			if owner != nil {
				ownerNote = fmt.Sprintf(" (owned by %s)", owner.ID)
			}
			out = append(out, c("RESTRICTED_PATH", model.Block, fmt.Sprintf("%s is restricted for %s%s.", target, task.ID, ownerNote), "", ownerID))
		} else {
			if owner != nil {
				ownerNote = " Soft owner: " + owner.ID + "."
			}
			out = append(out, c("OUT_OF_SCOPE", model.Warn, fmt.Sprintf("%s is outside %s's allowed paths (%v).%s", target, task.ID, task.AllowedPaths, ownerNote), "", ownerID))
		}
		if owner != nil {
			if st, ok := x.State(owner.ID); ok && st.AgentID != "" && st.AgentID != x.AgentID && (st.Status == model.InProgress || st.Status == model.Review) {
				out = append(out, c("OWNERSHIP_CONFLICT", model.Warn, fmt.Sprintf("%s is actively working in %s, which owns that area.", st.AgentID, owner.ID), st.AgentID, owner.ID))
			}
		}
	}
	if len(x.ProtectedPaths) > 0 && glob.AnyOverlap(x.ProtectedPaths, []string{target}) && model.IsWrite(kind) {
		out = append(out, c("RISKY_PATH", model.Warn, target+" is high-impact (migrations/infra). Verification will require human approval.", "", ""))
	}
	for _, i := range x.Active {
		if i.AgentID == x.AgentID || !model.IsWrite(i.Kind) || !glob.Overlap(i.Target, target) {
			continue
		}
		switch {
		case kind == model.Delete || i.Kind == model.Delete:
			out = append(out, c("DELETE_CONFLICT", model.Block, fmt.Sprintf("%s (%s) declared %s on %s, which overlaps your %s.", i.AgentID, i.TaskID, i.Kind, i.Target, kind), i.AgentID, i.TaskID))
		case kind == model.Create && i.Kind == model.Create:
			out = append(out, c("DUPLICATE_WORK", model.Block, fmt.Sprintf("%s (%s) is already creating %s.", i.AgentID, i.TaskID, i.Target), i.AgentID, i.TaskID))
		default:
			out = append(out, c("OVERLAP", model.Warn, fmt.Sprintf("%s (%s) declared %s on %s, which overlaps %s.", i.AgentID, i.TaskID, i.Kind, i.Target, target), i.AgentID, i.TaskID))
		}
	}
	return out
}
