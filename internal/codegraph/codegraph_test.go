package codegraph

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func write(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return rel
}

func files(t *testing.T, root string, m map[string]string) []string {
	var out []string
	for f, b := range m {
		out = append(out, write(t, root, f, b))
	}
	sort.Strings(out)
	return out
}

func TestTypeScriptAndJavaScript(t *testing.T) {
	root := t.TempDir()
	fs := files(t, root, map[string]string{
		"src/billing/status.ts":   "export type S = 'a'",
		"src/billing/index.ts":    "export * from './status'",
		"src/ui/panel.tsx":        "import { S } from '../billing'\nimport React from 'react'\nconst x = require('../billing/status')",
		"src/ui/lazy.js":          "const m = await import('./panel.js')",
		"src/ui/side-effect.ts":   "import '../billing/status'",
		"node_modules/x/index.js": "import '../../src/billing/status'",
		"src/other.ts":            "import './missing'",
	})
	g := Build(root, fs)
	if got := g.Imports["src/ui/panel.tsx"]; !reflect.DeepEqual(got, []string{"src/billing/index.ts", "src/billing/status.ts"}) {
		t.Fatalf("panel imports %v", got)
	}
	if got := g.Imports["src/billing/index.ts"]; !reflect.DeepEqual(got, []string{"src/billing/status.ts"}) {
		t.Fatalf("re-export %v", got)
	}
	if got := g.Imports["src/ui/lazy.js"]; !reflect.DeepEqual(got, []string{"src/ui/panel.tsx"}) {
		t.Fatalf("TypeScript projects import ./panel.js for panel.tsx: %v", got)
	}
	if _, ok := g.Imports["node_modules/x/index.js"]; ok {
		t.Fatal("node_modules must be skipped")
	}
	if len(g.Imports["src/other.ts"]) != 0 {
		t.Fatal("unresolved imports are ignored")
	}
	direct := g.ImportedBy([]string{"src/billing/status.ts"}, false)
	if !reflect.DeepEqual(direct, []string{"src/billing/index.ts", "src/ui/panel.tsx", "src/ui/side-effect.ts"}) {
		t.Fatalf("direct importers %v", direct)
	}
}

func TestPython(t *testing.T) {
	root := t.TempDir()
	fs := files(t, root, map[string]string{
		"app/__init__.py":         "",
		"app/billing/__init__.py": "from .status import S",
		"app/billing/status.py":   "S = 1",
		"app/web/views.py":        "from app.billing import status\nfrom ..billing.status import S\nimport os, json\nimport app.billing",
		"app/web/util.py":         "from . import views",
	})
	g := Build(root, fs)
	if got := g.Imports["app/web/views.py"]; !reflect.DeepEqual(got, []string{"app/billing/__init__.py", "app/billing/status.py"}) {
		t.Fatalf("views imports %v", got)
	}
	if got := g.Imports["app/billing/__init__.py"]; !reflect.DeepEqual(got, []string{"app/billing/status.py"}) {
		t.Fatalf("relative import %v", got)
	}
	if got := g.Imports["app/web/util.py"]; !reflect.DeepEqual(got, []string{"app/web/views.py"}) {
		t.Fatalf("from . import x %v", got)
	}
}

func TestGoPackagesAndTransitiveImporters(t *testing.T) {
	root := t.TempDir()
	fs := files(t, root, map[string]string{
		"go.mod":                     "module example.com/shop\n\ngo 1.22\n",
		"internal/billing/a.go":      "package billing\n",
		"internal/billing/b.go":      "package billing\n",
		"internal/billing/a_test.go": "package billing\n",
		"internal/api/h.go":          "package api\nimport (\n\t\"fmt\"\n\t\"example.com/shop/internal/billing\"\n)\n",
		"cmd/shop/main.go":           "package main\nimport \"example.com/shop/internal/api\"\nimport alias \"example.com/shop/internal/billing\"\n",
	})
	g := Build(root, fs)
	if got := g.Imports["internal/api/h.go"]; !reflect.DeepEqual(got, []string{"internal/billing/a.go", "internal/billing/b.go"}) {
		t.Fatalf("a Go import depends on every non-test file of the package: %v", got)
	}
	trans := g.ImportedBy([]string{"internal/billing/a.go"}, true)
	if !reflect.DeepEqual(trans, []string{"cmd/shop/main.go", "internal/api/h.go"}) {
		t.Fatalf("transitive importers %v", trans)
	}
	if got := g.ImportedBy([]string{"internal/billing/a.go"}, false); !reflect.DeepEqual(got, []string{"cmd/shop/main.go", "internal/api/h.go"}) {
		t.Fatalf("direct importers (main imports billing directly too) %v", got)
	}
	if g.Edges() == 0 {
		t.Fatal("edges")
	}
}
