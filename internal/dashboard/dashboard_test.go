package dashboard_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/dashboard"
	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
	"github.com/shriyashish-mishra/who-broke-it/internal/testutil"
)

func TestDashboardIsSelfContainedValidAndSafe(t *testing.T) {
	_, e := testutil.PlannedRepo(t)
	if _, err := e.Claim("TASK-001", engine.AgentOpts{Agent: "claude", As: "Maya"}, false); err != nil {
		t.Fatal(err)
	}
	// hostile text in the plan must not break out of the inlined JSON
	t1, _ := e.Task("TASK-001")
	t1.Title = `</script><script>alert("pwned")</script>`
	if err := e.Store.SaveTask(t1); err != nil {
		t.Fatal(err)
	}
	d := dashboard.Collect(e, "2026-01-01 00:00:00")
	page := dashboard.HTML(d)

	if strings.Contains(page, `</script><script>alert`) {
		t.Fatal("the data block must escape </script>")
	}
	// the inlined JSON must be valid and complete
	m := regexp.MustCompile(`(?s)const D = (\{.*?\});\nconst \$`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("data block not found")
	}
	var back dashboard.Data
	if err := json.Unmarshal([]byte(strings.ReplaceAll(m[1], `<\/`, `</`)), &back); err != nil {
		t.Fatalf("inlined data is not valid JSON: %v", err)
	}
	if len(back.Tasks) != len(d.Tasks) || back.Project.Name != d.Project.Name || len(back.Contracts) == 0 || back.Tasks[0].Agent != "claude@maya" {
		t.Fatalf("data lost in transit: %d tasks, agent %q", len(back.Tasks), back.Tasks[0].Agent)
	}
	// static-hostable: no external resources of any kind
	for _, bad := range []string{`src="http`, `href="http`, `@import`, `url(http`, `<link `, `fetch(`, `XMLHttpRequest`} {
		if strings.Contains(page, bad) {
			t.Fatalf("dashboard must be fully self-contained, found %q", bad)
		}
	}
	for _, want := range []string{"WHO BROKE IT?", "Needs attention", "blast", "Execution plan"} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}
