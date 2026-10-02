package mcp_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"wbi/internal/mcp"
	"wbi/internal/testutil"
)

type rpc struct {
	Result struct {
		Tools []struct {
			Name        string
			InputSchema map[string]any
		} `json:"tools"`
		Content []struct{ Text string } `json:"content"`
		IsError bool                    `json:"isError"`
	} `json:"result"`
	Error *struct{ Code int } `json:"error"`
}

func TestProtocolRoundTrip(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	call := func(name string, args map[string]any) (string, bool) {
		args["agent"], args["developer"] = "codex", "DevB"
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
		var r rpc
		if err := json.Unmarshal(mustJSON(t, mcp.Handle(e, b)), &r); err != nil {
			t.Fatal(err)
		}
		return r.Result.Content[0].Text, r.Result.IsError
	}

	var list rpc
	_ = json.Unmarshal(mustJSON(t, mcp.Handle(e, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))), &list)
	if len(list.Result.Tools) != 15 {
		t.Fatalf("want 15 tools, got %d", len(list.Result.Tools))
	}
	for _, tl := range list.Result.Tools {
		if tl.InputSchema["type"] != "object" {
			t.Errorf("%s: schema must be an object", tl.Name)
		}
	}
	if mcp.Handle(e, []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)) != nil {
		t.Fatal("notifications must not be answered")
	}
	var bad rpc
	_ = json.Unmarshal(mustJSON(t, mcp.Handle(e, []byte(`{"jsonrpc":"2.0","id":9,"method":"nope"}`))), &bad)
	if bad.Error == nil || bad.Error.Code != -32601 {
		t.Fatalf("want method-not-found, got %+v", bad.Error)
	}

	txt, isErr := call("wbi_list_tasks", map[string]any{"status": "ready"})
	var ready []map[string]any
	_ = json.Unmarshal([]byte(txt), &ready)
	if isErr || len(ready) != 1 || ready[0]["id"] != "TASK-001" {
		t.Fatalf("ready tasks: %s", txt)
	}
	if _, isErr := call("wbi_claim_task", map[string]any{"task_id": "TASK-002"}); !isErr {
		t.Fatal("claiming a blocked task must be an error result")
	}
	if _, isErr := call("wbi_claim_task", map[string]any{"task_id": "TASK-001"}); isErr {
		t.Fatal("claiming a READY task must succeed")
	}
	if ctx, _ := call("wbi_get_context", map[string]any{"task_id": "TASK-001"}); !strings.Contains(ctx, "Acceptance criteria") {
		t.Fatal("work packet missing")
	}
	soft, _ := call("wbi_declare_intent", map[string]any{"task_id": "TASK-001", "kind": "MODIFY", "target": "migrations/x.sql"})
	var s struct {
		Blocked   bool
		Conflicts []struct{ Code string }
	}
	_ = json.Unmarshal([]byte(strings.ToLower(soft[:0])+soft), &s)
	if s.Blocked || len(s.Conflicts) != 2 {
		t.Fatalf("out-of-scope without a restriction is a soft warning: %s", soft)
	}
	hard, _ := call("wbi_declare_intent", map[string]any{"task_id": "TASK-001", "kind": "CHANGE_CONTRACT", "target": "Session"})
	_ = json.Unmarshal([]byte(hard), &s)
	if !s.Blocked {
		t.Fatalf("only the provider may change a contract: %s", hard)
	}
	if _, isErr := call("nope", map[string]any{}); !isErr {
		t.Fatal("unknown tool must be an error result")
	}
}

func TestServeNewlineDelimited(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n\n" + `not json` + "\n")
	var out bytes.Buffer
	if err := mcp.Serve(e, in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"protocolVersion":"2025-06-18"`) || !strings.Contains(lines[1], "-32700") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
