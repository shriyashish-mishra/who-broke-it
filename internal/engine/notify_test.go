package engine_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
	"github.com/shriyashish-mishra/who-broke-it/internal/testutil"
)

type hook struct {
	mu   sync.Mutex
	hits []map[string]any
}

func (h *hook) server(t *testing.T, status int) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		h.mu.Lock()
		h.hits = append(h.hits, m)
		h.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestNotificationsReachSlackDiscordAndGenericWebhooks(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	var slack, discord, generic hook
	t.Setenv("WBI_T_SLACK", slack.server(t, 200).URL)
	t.Setenv("WBI_T_DISCORD", discord.server(t, 204).URL)
	t.Setenv("WBI_T_HOOK", generic.server(t, 200).URL)
	for _, a := range [][2]string{{"slack", "WBI_T_SLACK"}, {"discord", "WBI_T_DISCORD"}, {"webhook", "WBI_T_HOOK"}} {
		if err := e.AddNotify(a[0], a[1], nil); err != nil {
			t.Fatal(err)
		}
	}
	through(t, e, "TASK-001", "TASK-002", "TASK-004")
	slack.hits, discord.hits, generic.hits = nil, nil, nil
	finish(t, e, "TASK-007", claude, map[string]string{"src/api/billing/status.ts": "export type S = 'paused'"}, func(o *engine.HandoffOpts) {
		o.Contracts = []string{"BillingStatus=now supports paused"}
	})

	find := func(h *hook, key, sub string) bool {
		for _, m := range h.hits {
			if s, _ := m[key].(string); strings.Contains(s, sub) {
				return true
			}
		}
		return false
	}
	if !find(&slack, "text", "BillingStatus") || !find(&slack, "text", "changed to v2") {
		t.Fatalf("slack did not get the contract change: %+v", slack.hits)
	}
	if !find(&discord, "content", "**BillingStatus**") {
		t.Fatalf("discord must use its own format and markdown: %+v", discord.hits)
	}
	var sawEvent bool
	for _, m := range generic.hits {
		if m["event"] == "contract_changed" && m["task"] == "TASK-007" && m["data"] != nil && m["ts"] != nil {
			sawEvent = true
		}
	}
	if !sawEvent {
		t.Fatalf("generic webhook must get structured JSON: %+v", generic.hits)
	}
	// high-impact handoff announces the need for a human
	if !find(&slack, "text", "ready for human review") {
		t.Fatalf("review request missing: %+v", slack.hits)
	}
}

func TestNotificationsAreFilteredAndNeverBreakOrLeak(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	var only hook
	t.Setenv("WBI_T_ONLY", only.server(t, 200).URL)
	if err := e.AddNotify("slack", "WBI_T_ONLY", []string{"claimed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Claim("TASK-001", claude, false); err != nil {
		t.Fatal(err)
	}
	if len(only.hits) != 1 || !strings.Contains(only.hits[0]["text"].(string), "claimed TASK-001") {
		t.Fatalf("only subscribed events are sent: %+v", only.hits)
	}
	e.Release("TASK-001") // not subscribed
	if len(only.hits) != 1 {
		t.Fatalf("release must not notify: %+v", only.hits)
	}

	// a failing webhook warns without leaking the URL and never fails the command
	var warned []string
	e.Warn = func(s string) { warned = append(warned, s) }
	var bad hook
	srv := bad.server(t, 500)
	t.Setenv("WBI_T_BAD", srv.URL+"/secret-token-123")
	if err := e.AddNotify("webhook", "WBI_T_BAD", []string{"claimed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Claim("TASK-001", claude, false); err != nil {
		t.Fatalf("a dead webhook must not break the command: %v", err)
	}
	joined := strings.Join(warned, "\n")
	if !strings.Contains(joined, "notification failed") || strings.Contains(joined, "secret-token-123") {
		t.Fatalf("must warn, and must not print the URL: %q", joined)
	}
	// URLs must never be stored in the repo
	if err := e.AddNotify("slack", "https://hooks.slack.com/services/T/B/X", nil); err == nil {
		t.Fatal("a URL in place of an env var name must be refused")
	}
	if err := e.AddNotify("pagerduty", "X", nil); err == nil {
		t.Fatal("unknown sink type must be refused")
	}
}
