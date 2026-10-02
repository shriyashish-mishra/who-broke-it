// Package rules parses the project constitution and enforces its machine-checkable rules.
package rules

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shriyashish-mishra/who-broke-it/internal/gitx"
	"github.com/shriyashish-mishra/who-broke-it/internal/glob"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

type Rule struct {
	ID     string
	Text   string
	Paths  []string
	Except []string
	Forbid *regexp.Regexp
}

// DefaultConstitution is written by `wbi init`.
var DefaultConstitution = strings.ReplaceAll(`# Project Constitution

Durable rules every human and agent must follow. Edit freely; this file is yours.

Rule syntax: ¤- **C<n>** text¤ followed by optional directives in backticks:
¤paths: glob, glob¤ scopes a rule to matching files, ¤forbid: /regex/¤ makes it
machine-checkable (enforced by ¤wbi verify¤ on a task's diff and by ¤wbi drift¤ on the whole repo),
¤except: glob¤ exempts files from a forbid rule. Regexes use Go (RE2) syntax.

- **C1** Contracts first: never change a shared contract (see ¤.wbi/contracts/¤) without declaring a CHANGE_CONTRACT intent and recording it in the handoff.
- **C2** Stay inside your task's allowed paths. If you need something outside them, declare an intent or ask the owner.
- **C3** Database migrations are only edited by the task that owns them. ¤paths: migrations/**¤
- **C4** No hard-coded secrets in source. ¤forbid: /(api[_-]?key|secret|password)\s*[:=]\s*["'][A-Za-z0-9_\-]{16,}["']/i¤
- **C5** Every task ships with tests that exercise its acceptance criteria.
`, "¤", "`")

var (
	ruleRe  = regexp.MustCompile("^\\s*-\\s+\\*\\*([A-Za-z0-9_-]+)\\*\\*\\s+(.*)$")
	dirRe   = regexp.MustCompile("`([a-z]+):\\s*([^`]*)`")
	litRe   = regexp.MustCompile(`(?s)^/(.*)/([a-z]*)$`)
	splitRe = regexp.MustCompile(`\s*,\s*`)
)

func splitList(v string) []string {
	var out []string
	for _, s := range splitRe.Split(strings.TrimSpace(v), -1) {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Parse extracts rules from constitution markdown.
func Parse(md string) []Rule {
	var out []Rule
	for _, line := range strings.Split(md, "\n") {
		m := ruleRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		r := Rule{ID: m[1]}
		for _, d := range dirRe.FindAllStringSubmatch(m[2], -1) {
			switch d[1] {
			case "paths":
				r.Paths = splitList(d[2])
			case "except":
				r.Except = splitList(d[2])
			case "forbid":
				if l := litRe.FindStringSubmatch(strings.TrimSpace(d[2])); l != nil {
					pat := l[1]
					var flags string
					for _, f := range l[2] {
						if f == 'i' || f == 's' || f == 'm' {
							flags += string(f)
						}
					}
					if flags != "" {
						pat = "(?" + flags + ")" + pat
					}
					if re, err := regexp.Compile(pat); err == nil {
						r.Forbid = re
					}
				}
			}
		}
		r.Text = strings.Join(strings.Fields(dirRe.ReplaceAllString(m[2], "")), " ")
		out = append(out, r)
	}
	return out
}

// Load reads <root>/.wbi/constitution.md; a missing file yields no rules.
func Load(root string) []Rule {
	b, err := os.ReadFile(filepath.Join(root, ".wbi", "constitution.md"))
	if err != nil {
		return nil
	}
	return Parse(string(b))
}

// ForTask returns global rules plus rules scoped to paths the task may touch.
func ForTask(rules []Rule, t model.Task) []Rule {
	paths := append(append(append([]string{}, t.AllowedPaths...), t.RestrictedPaths...), t.RelevantFiles...)
	var out []Rule
	for _, r := range rules {
		if len(r.Paths) == 0 || glob.AnyOverlap(r.Paths, paths) {
			out = append(out, r)
		}
	}
	return out
}

type Violation struct {
	Rule    Rule   `json:"-"`
	RuleID  string `json:"rule"`
	File    string `json:"file"`
	Excerpt string `json:"excerpt"`
	// Branch is set when found on an unmerged task branch rather than the working tree.
	Branch string `json:"branch,omitempty"`
}

// CheckLines applies every forbid rule to the given per-file lines.
func CheckLines(rules []Rule, files []gitx.FileLines) []Violation {
	var out []Violation
	for _, r := range rules {
		if r.Forbid == nil {
			continue
		}
		for _, f := range files {
			if len(r.Paths) > 0 && !glob.MatchesAny(r.Paths, f.File) {
				continue
			}
			if len(r.Except) > 0 && glob.MatchesAny(r.Except, f.File) {
				continue
			}
			for _, l := range f.Lines {
				if r.Forbid.MatchString(l) {
					ex := strings.TrimSpace(l)
					if len(ex) > 100 {
						ex = ex[:100]
					}
					out = append(out, Violation{Rule: r, RuleID: r.ID, File: f.File, Excerpt: ex})
					break
				}
			}
		}
	}
	return out
}

var skipRe = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|ico|pdf|lock|woff2?)$`)

// ScanRepo scans the working tree for forbid-rule violations (architecture drift).
func ScanRepo(root string, rules []Rule) []Violation {
	var files []gitx.FileLines
	for _, f := range gitx.LsFiles(root) {
		if strings.HasPrefix(f, ".wbi/") || skipRe.MatchString(f) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		files = append(files, gitx.FileLines{File: f, Lines: strings.Split(string(b), "\n")})
	}
	return CheckLines(rules, files)
}
