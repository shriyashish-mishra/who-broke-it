package render

import (
	"strings"
	"testing"
	"time"

	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

func TestPadIgnoresANSIAndAgoBuckets(t *testing.T) {
	if got := pad("\x1b[31mab\x1b[0m", 5); got != "\x1b[31mab\x1b[0m   " {
		t.Fatalf("pad counted escape codes: %q", got)
	}
	if pad("abcdef", 3) != "abcdef" {
		t.Fatal("pad must never truncate")
	}
	now := time.Now().UnixMilli()
	for ms, want := range map[int64]string{now - 5_000: "s ago", now - 5*60_000: "min ago", now - 3*3600_000: "h ago", now - 3*86400_000: "d ago"} {
		if got := Ago(ms); !strings.HasSuffix(got, want) {
			t.Errorf("Ago(%d)=%q want suffix %q", now-ms, got, want)
		}
	}
	if strings.Count(Bar(50, 10), "█") != 5 {
		t.Fatal("bar")
	}
}

func TestStatusAndCheckRender(t *testing.T) {
	var v engine.StatusView
	v.Project.Name, v.Project.Goal = "demo", "ship it"
	v.Progress.Done, v.Progress.Total, v.Progress.Pct = 1, 4, 25
	v.Ready = []model.Task{{ID: "TASK-002", Title: "DB"}}
	for i := 0; i < 8; i++ {
		v.Blocked = append(v.Blocked, engine.BlockedTask{Task: model.Task{ID: "TASK-1" + string(rune('0'+i)), Title: "x"}, WaitingFor: []string{"TASK-002"}})
	}
	v.Attention = []string{"⚠ BillingStatus changed"}
	out := ansiRe.ReplaceAllString(Status(v), "") // colours depend on the terminal; the content must not
	for _, want := range []string{"WHO BROKE IT?", "PROJECT: demo", "25%", "READY", "TASK-002", "… and 3 more", "ATTENTION", "BillingStatus changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("status missing %q", want)
		}
	}
	rep := engine.CheckReport{Task: "TASK-7", Base: "main", Head: "HEAD", Files: []string{"a"}, Findings: []engine.Finding{
		{Level: "error", Code: "SCOPE", Message: "a is outside | scope", File: "a"}, {Level: "warn", Code: "NO_HANDOFF", Message: "m"}}}
	gh := ansiRe.ReplaceAllString(CheckGitHub(rep), "")
	if !strings.Contains(gh, "::error title=SCOPE,file=a::a is outside | scope") || !strings.Contains(gh, "::warning title=NO_HANDOFF::m") || !strings.Contains(gh, "FAIL") {
		t.Fatalf("github format:\n%s", gh)
	}
	md := ansiRe.ReplaceAllString(CheckMarkdown(rep), "")
	if !strings.Contains(md, "❌ FAIL") || !strings.Contains(md, `a is outside \| scope`) {
		t.Fatalf("markdown must escape table pipes:\n%s", md)
	}
	rep.Findings, rep.OK = nil, true
	if !strings.Contains(CheckMarkdown(rep), "Nobody broke it") {
		t.Fatal("clean markdown")
	}
}
