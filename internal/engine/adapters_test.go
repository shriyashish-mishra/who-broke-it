package engine_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
	"github.com/shriyashish-mishra/who-broke-it/internal/testutil"
)

func TestEveryAdapterInstallsIdempotentlyAndPreservesUserContent(t *testing.T) {
	for _, a := range engine.Adapters() {
		t.Run(a.Name, func(t *testing.T) {
			dir := testutil.MakeRepo(t, map[string]string{a.InstructionFile: "# mine\nkeep this line\n"})
			for i := 0; i < 3; i++ {
				f, err := engine.InstallGuidance(dir, a.Name)
				if err != nil || f != a.InstructionFile {
					t.Fatalf("install: %q %v", f, err)
				}
			}
			body := testutil.ReadFile(t, dir, a.InstructionFile)
			if !strings.Contains(body, "keep this line") || strings.Count(body, "wbi:begin") != 1 || !strings.Contains(body, "wbi handoff") {
				t.Fatalf("%s:\n%s", a.InstructionFile, body)
			}
			// a fresh file gets the wrapper when the tool needs one (Cursor front matter)
			fresh := testutil.MakeRepo(t, nil)
			if _, err := engine.InstallGuidance(fresh, a.Name); err != nil {
				t.Fatal(err)
			}
			if a.Wrap != nil && !strings.HasPrefix(testutil.ReadFile(t, fresh, a.InstructionFile), "---\n") {
				t.Fatal("wrapped adapters must write their front matter on a new file")
			}
		})
	}
}

func TestAdapterRegistryIsHonestAndConsistent(t *testing.T) {
	seen := map[string]bool{}
	status := map[string]string{}
	for _, a := range engine.Adapters() {
		if a.Name == "" || a.Display == "" || a.InstructionFile == "" || seen[a.Name] {
			t.Fatalf("bad or duplicate adapter: %+v", a)
		}
		seen[a.Name] = true
		status[a.Name] = a.Status
		switch a.Status {
		case engine.Verified, engine.Partial:
			if a.Tested == "" || a.Notes == "" {
				t.Errorf("%s is %s so it must record the tested version and setup notes", a.Name, a.Status)
			}
		case engine.Documented:
			if a.Tested != "" {
				t.Errorf("%s is only documented, it cannot have a tested version", a.Name)
			}
		default:
			t.Fatalf("%s: unknown status %q", a.Name, a.Status)
		}
		cfg := engine.MCPConfig(a.Name, "/usr/local/bin/wbi")
		if a.Status == engine.Documented && !strings.Contains(cfg, "not fully tested") {
			t.Errorf("%s is documented only, so its snippet must say so:\n%s", a.Name, cfg)
		}
		if a.MCP == engine.MCPGeneric {
			js := cfg[:strings.Index(cfg, "\n}\n")+2]
			var m map[string]map[string]map[string]any
			if err := json.Unmarshal([]byte(js), &m); err != nil || m["mcpServers"]["wbi"]["command"] != "/usr/local/bin/wbi" {
				t.Errorf("%s: generic snippet must be valid JSON: %v\n%s", a.Name, err, js)
			}
		}
	}
	// Pinned on purpose: this is the list of agents that were actually run (docs/REAL-AGENTS.md). Changing it
	// means someone ran another agent, or something regressed. Update both together.
	want := map[string]string{"claude": "verified", "codex": "verified", "antigravity": "verified", "cursor": "verified", "opencode": "verified"}
	for name, st := range want {
		if status[name] != st {
			t.Errorf("%s should be %s, is %q", name, st, status[name])
		}
	}
	if len(status) != len(want) {
		t.Errorf("unexpected adapters: %v", status)
	}
	// Codex needs two settings or it silently cannot work; the snippet must carry them.
	codex := engine.MCPConfig("codex", "wbi")
	if !strings.Contains(codex, "default_tools_approval_mode") || !strings.Contains(codex, "writable_roots") {
		t.Errorf("codex snippet must include the required settings:\n%s", codex)
	}
	if _, err := engine.InstallGuidance(t.TempDir(), "nope"); err == nil {
		t.Fatal("unknown adapter must error")
	}
}

func TestProviderDetectionComesFromTheRegistry(t *testing.T) {
	t.Setenv("CLAUDECODE", "")
	if engine.DetectProvider() != "" {
		t.Fatal("nothing announced itself")
	}
	t.Setenv("CLAUDECODE", "1")
	if engine.DetectProvider() != "claude" {
		t.Fatal("Claude Code must be auto-detected")
	}
}
