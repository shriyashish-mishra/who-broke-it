// Package codegraph builds a static import graph of a repository (JS/TS, Python, Go) so blast radius can
// see coupling that nobody declared. It is deliberately simple and deterministic: regex scanning of import
// statements, resolved to files inside the repo. Aliases (tsconfig paths, webpack), dynamic imports with
// computed names and generated code are not followed; anything that does not resolve to a repo file is ignored.
package codegraph

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Graph maps each repo file to the repo files it imports.
type Graph struct {
	Imports map[string][]string
	rev     map[string][]string
}

const maxFileSize = 512 * 1024

var skipDirs = []string{"node_modules/", "vendor/", "dist/", "build/", ".git/", ".wbi/", "__pycache__/", ".venv/", "target/"}

func skip(f string) bool {
	for _, d := range skipDirs {
		if strings.HasPrefix(f, d) || strings.Contains(f, "/"+d) {
			return true
		}
	}
	return false
}

var (
	jsExts = []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}
	jsRe   = regexp.MustCompile(`(?m)(?:\bimport\s+(?:[^'"]*?\s+from\s+)?|\bexport\s+[^'"]*?\s+from\s+|\brequire\(\s*|\bimport\(\s*)['"]([^'"]+)['"]`)
	pyRe   = regexp.MustCompile(`(?m)^[ \t]*(?:from\s+(\.*[\w.]*)\s+import\s+([\w, *()]+)|import\s+([\w.]+(?:\s*,\s*[\w.]+)*))`)
	goBlk  = regexp.MustCompile(`(?s)import\s*\((.*?)\)`)
	goOne  = regexp.MustCompile(`(?m)^\s*import\s+(?:\w+\s+)?"([^"]+)"`)
	goStr  = regexp.MustCompile(`"([^"]+)"`)
	modRe  = regexp.MustCompile(`(?m)^module\s+(\S+)`)
)

// Build scans files (repo-relative paths) under root.
func Build(root string, files []string) *Graph {
	set := map[string]bool{}
	var keep []string
	for _, f := range files {
		if !skip(f) {
			set[f] = true
			keep = append(keep, f)
		}
	}
	sort.Strings(keep)
	goMod := ""
	if b, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		if m := modRe.FindStringSubmatch(string(b)); m != nil {
			goMod = m[1]
		}
	}
	// package directory → its non-test .go files (a Go import depends on the whole package)
	goPkg := map[string][]string{}
	for _, f := range keep {
		if strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") {
			d := path.Dir(f)
			goPkg[d] = append(goPkg[d], f)
		}
	}

	g := &Graph{Imports: map[string][]string{}}
	for _, f := range keep {
		ext := path.Ext(f)
		var specs []string
		switch {
		case contains(jsExts, ext):
			specs = scan(root, f, func(src string) []string {
				var out []string
				for _, m := range jsRe.FindAllStringSubmatch(src, -1) {
					out = append(out, m[1])
				}
				return out
			})
			g.add(f, resolveJS(f, specs, set))
		case ext == ".py":
			specs = scan(root, f, func(src string) []string {
				var out []string
				for _, m := range pyRe.FindAllStringSubmatch(src, -1) {
					if m[1] != "" {
						out = append(out, m[1])
						// `from pkg import mod` may import the submodule pkg/mod.py
						for _, n := range strings.Split(strings.Trim(m[2], "() "), ",") {
							if n = strings.TrimSpace(n); n != "" && n != "*" {
								sep := "."
								if strings.HasSuffix(m[1], ".") { // "from . import x" → ".x", not "..x"
									sep = ""
								}
								out = append(out, m[1]+sep+strings.Fields(n)[0])
							}
						}
					} else {
						for _, n := range strings.Split(m[3], ",") {
							out = append(out, strings.TrimSpace(n))
						}
					}
				}
				return out
			})
			g.add(f, resolvePy(f, specs, set))
		case ext == ".go" && goMod != "":
			specs = scan(root, f, func(src string) []string {
				var out []string
				for _, b := range goBlk.FindAllStringSubmatch(src, -1) {
					for _, s := range goStr.FindAllStringSubmatch(b[1], -1) {
						out = append(out, s[1])
					}
				}
				for _, s := range goOne.FindAllStringSubmatch(src, -1) {
					out = append(out, s[1])
				}
				return out
			})
			var deps []string
			for _, s := range specs {
				if s == goMod || strings.HasPrefix(s, goMod+"/") {
					dir := strings.TrimPrefix(strings.TrimPrefix(s, goMod), "/")
					if dir == "" {
						dir = "."
					}
					for _, gf := range goPkg[dir] {
						if gf != f {
							deps = append(deps, gf)
						}
					}
				}
			}
			g.add(f, deps)
		}
	}
	g.rev = map[string][]string{}
	for f, deps := range g.Imports {
		for _, d := range deps {
			g.rev[d] = append(g.rev[d], f)
		}
	}
	for k := range g.rev {
		sort.Strings(g.rev[k])
	}
	return g
}

func (g *Graph) add(f string, deps []string) {
	seen := map[string]bool{f: true}
	var out []string
	for _, d := range deps {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	if len(out) > 0 {
		g.Imports[f] = out
	}
}

func scan(root, f string, extract func(string) []string) []string {
	st, err := os.Stat(filepath.Join(root, f))
	if err != nil || st.Size() > maxFileSize {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(root, f))
	if err != nil {
		return nil
	}
	return extract(string(b))
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func resolveJS(from string, specs []string, set map[string]bool) []string {
	var out []string
	dir := path.Dir(from)
	for _, s := range specs {
		if !strings.HasPrefix(s, ".") {
			continue // bare package or alias: not a repo file we can resolve
		}
		base := path.Clean(path.Join(dir, s))
		cands := []string{base}
		for _, e := range jsExts {
			cands = append(cands, base+e, path.Join(base, "index"+e))
		}
		// TS projects import "./x.js" for x.ts
		if e := path.Ext(base); e == ".js" || e == ".jsx" || e == ".mjs" {
			stem := strings.TrimSuffix(base, e)
			cands = append(cands, stem+".ts", stem+".tsx")
		}
		for _, c := range cands {
			if set[c] && c != from {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

func resolvePy(from string, specs []string, set map[string]bool) []string {
	var out []string
	for _, s := range specs {
		var base string
		if strings.HasPrefix(s, ".") {
			dots := len(s) - len(strings.TrimLeft(s, "."))
			dir := path.Dir(from)
			for i := 1; i < dots; i++ {
				dir = path.Dir(dir)
			}
			rest := strings.ReplaceAll(strings.TrimLeft(s, "."), ".", "/")
			base = path.Join(dir, rest)
		} else {
			base = strings.ReplaceAll(s, ".", "/")
		}
		for _, c := range []string{base + ".py", path.Join(base, "__init__.py")} {
			if set[c] && c != from {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// ImportedBy returns the files that (transitively, if asked) import any of the given files.
func (g *Graph) ImportedBy(files []string, transitive bool) []string {
	seen := map[string]bool{}
	in := map[string]bool{}
	queue := append([]string{}, files...)
	for _, f := range files {
		in[f] = true
	}
	var out []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range g.rev[cur] {
			if seen[d] || in[d] {
				continue
			}
			seen[d] = true
			out = append(out, d)
			if transitive {
				queue = append(queue, d)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Edges counts import edges (for reporting).
func (g *Graph) Edges() int {
	n := 0
	for _, d := range g.Imports {
		n += len(d)
	}
	return n
}
