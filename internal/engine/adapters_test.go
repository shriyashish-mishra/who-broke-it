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
	verified := 0
	for _, a := range engine.Adapters() {
		if a.Name == "" || a.Display == "" || a.InstructionFile == "" || seen[a.Name] {
			t.Fatalf("bad or duplicate adapter: %+v", a)
		}
		seen[a.Name] = true
		if a.Status != engine.Verified && a.Status != engine.Documented {
			t.Fatalf("%s: unknown status %q", a.Name, a.Status)
		}
		if a.Status == engine.Verified {
			verified++
		}
		cfg := engine.MCPConfig(a.Name, "/usr/local/bin/wbi")
		if a.Status != engine.Verified && !strings.Contains(cfg, "not tested") && a.MCP != engine.MCPNone {
			t.Errorf("%s is %q so its MCP snippet must say it is untested:\n%s", a.Name, a.Status, cfg)
		}
		if a.MCP == engine.MCPGeneric {
			js := cfg[:strings.Index(cfg, "\n}\n")+2]
			var m map[string]map[string]map[string]any
			if err := json.Unmarshal([]byte(js), &m); err != nil || m["mcpServers"]["wbi"]["command"] != "/usr/local/bin/wbi" {
				t.Errorf("%s: generic snippet must be valid JSON: %v\n%s", a.Name, err, js)
			}
		}
	}
	// Only Claude Code has been run for real. If this changes, docs/REAL-AGENTS.md must change with it.
	if verified != 1 || !seen["claude"] {
		t.Fatalf("exactly one verified adapter (claude) expected, got %d", verified)
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
