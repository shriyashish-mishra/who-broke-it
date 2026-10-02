package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

// DefaultNotifyEvents are what a sink gets when it does not list events itself: the moments a team needs to know.
var DefaultNotifyEvents = []string{"contract_changed", "verify_failed", "verify_review", "handoff", "approved"}

// notifyMessage turns an event into a short human message ("" = not interesting).
func notifyMessage(name, taskID, agentID string, p map[string]any) string {
	str := func(k string) string { s, _ := p[k].(string); return s }
	num := func(k string) int { f, _ := p[k].(float64); return int(f) }
	n := func(k string) int {
		if l, ok := p[k].([]any); ok {
			return len(l)
		}
		return 0
	}
	switch name {
	case "contract_changed":
		return fmt.Sprintf("⚠️ *%s* changed to v%d by %s (%s): %d downstream task(s) affected, risk %s", str("contract"), num("version"), taskID, agentID, n("affected"), str("risk"))
	case "verify_failed":
		return fmt.Sprintf("❌ %s failed verification and went back to IN_PROGRESS (%s)", taskID, agentID)
	case "verify_review":
		return fmt.Sprintf("🔐 %s is ready for human review (high-impact or needs sign-off): `wbi approve %s`", taskID, taskID)
	case "handoff":
		return fmt.Sprintf("📦 %s handed off by %s (%d file(s))", taskID, agentID, num("files"))
	case "approved":
		return fmt.Sprintf("✅ %s approved by %s and is DONE", taskID, str("by"))
	case "claimed":
		return fmt.Sprintf("🙋 %s claimed %s", agentID, taskID)
	case "released":
		return fmt.Sprintf("↩️ %s was released", taskID)
	case "intent_blocked":
		return fmt.Sprintf("🚫 %s tried a blocked change on %s: %v", agentID, taskID, p["conflicts"])
	}
	return ""
}

// eventName maps a stored event to the name sinks subscribe to (verify is split by verdict).
func eventName(typ string, p map[string]any) string {
	if typ == "verify" {
		switch v, _ := p["verdict"].(string); v {
		case "FAILED":
			return "verify_failed"
		case "READY_FOR_REVIEW":
			return "verify_review"
		default:
			return "verify_done"
		}
	}
	return typ
}

// dispatch sends an event to every configured sink that subscribes to it. Best effort and bounded: a dead
// webhook can never break or hang a command. The URL comes from an environment variable and is never logged.
func (e *Engine) dispatch(typ, taskID, agentID string, payload any) {
	sinks := e.Project().Notify
	if len(sinks) == 0 {
		return
	}
	var p map[string]any
	if b, err := json.Marshal(payload); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	name := eventName(typ, p)
	msg := notifyMessage(name, taskID, agentID, p)
	if msg == "" {
		return
	}
	for _, s := range sinks {
		events := s.Events
		if len(events) == 0 {
			events = DefaultNotifyEvents
		}
		if !contains(events, name) {
			continue
		}
		url := os.Getenv(s.URLEnv)
		if url == "" {
			e.warnOnce("notify:"+s.URLEnv, fmt.Sprintf("wbi: notify sink %q is configured but $%s is not set", s.Type, s.URLEnv))
			continue
		}
		if err := postWebhook(s.Type, url, name, taskID, agentID, msg, p); err != nil {
			e.warnOnce("notifyfail:"+s.URLEnv, "wbi: notification failed ("+s.Type+"): "+redact(err.Error(), url))
		}
	}
}

func redact(s, secret string) string { return strings.ReplaceAll(s, secret, "<webhook>") }

func postWebhook(kind, url, name, taskID, agentID, msg string, p map[string]any) error {
	var body any
	switch kind {
	case "slack":
		body = map[string]any{"text": msg}
	case "discord":
		body = map[string]any{"content": strings.ReplaceAll(msg, "*", "**")}
	default: // generic JSON for anything else (Teams, Zapier, n8n, your own service, Linear/Jira bridges)
		body = map[string]any{"event": name, "task": taskID, "agent": agentID, "message": msg, "data": p, "ts": time.Now().UTC().Format(time.RFC3339)}
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// AddNotify registers a sink (type slack|discord|webhook) reading its URL from the named environment variable.
func (e *Engine) AddNotify(kind, urlEnv string, events []string) error {
	if kind != "slack" && kind != "discord" && kind != "webhook" {
		return model.Errf("type must be slack, discord or webhook")
	}
	if urlEnv == "" || strings.Contains(urlEnv, "://") {
		return model.Errf("--env takes the NAME of an environment variable holding the URL (never put the URL itself in the repo)")
	}
	p := e.Project()
	var keep []model.NotifyConfig
	for _, n := range p.Notify {
		if !(n.Type == kind && n.URLEnv == urlEnv) {
			keep = append(keep, n)
		}
	}
	p.Notify = append(keep, model.NotifyConfig{Type: kind, URLEnv: urlEnv, Events: events})
	return e.Store.SaveProject(p)
}

// TestNotify sends a sample message to every sink and reports per-sink success.
func (e *Engine) TestNotify() []string {
	var out []string
	for _, s := range e.Project().Notify {
		url := os.Getenv(s.URLEnv)
		if url == "" {
			out = append(out, fmt.Sprintf("%s ($%s): not set", s.Type, s.URLEnv))
			continue
		}
		if err := postWebhook(s.Type, url, "test", "TASK-000", "wbi", "👋 Who Broke It? notifications are working. Nobody broke it. Yet.", map[string]any{}); err != nil {
			out = append(out, fmt.Sprintf("%s ($%s): %s", s.Type, s.URLEnv, redact(err.Error(), url)))
		} else {
			out = append(out, fmt.Sprintf("%s ($%s): ok", s.Type, s.URLEnv))
		}
	}
	return out
}
