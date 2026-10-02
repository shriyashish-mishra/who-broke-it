package main

import (
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	a := parseArgs([]string{"handoff", "TASK-1", "--summary", "did it", "--contract", "A=x", "--contract=B", "--worktree", "--attest=1,2", "--limitation", "no refunds"})
	if !reflect.DeepEqual(a.pos, []string{"handoff", "TASK-1"}) {
		t.Fatalf("pos %v", a.pos)
	}
	if a.get("summary") != "did it" || a.get("attest") != "1,2" || !a.has("worktree") {
		t.Fatalf("flags %v", a.flags)
	}
	if !reflect.DeepEqual(a.multi["contract"], []string{"A=x", "B"}) || !reflect.DeepEqual(a.multi["limitation"], []string{"no refunds"}) {
		t.Fatalf("multi %v", a.multi)
	}
	if !parseArgs([]string{"-h"}).has("help") {
		t.Fatal("-h should mean help")
	}
}
