// Package store reads and writes the repo-native Engineering Graph under `.wbi/`.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/shriyashish-mishra/who-broke-it/internal/gitx"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

type Store struct{ Root string }

func (s *Store) Dir() string { return filepath.Join(s.Root, ".wbi") }

// Find walks up from cwd for `.wbi/project.json`. Linked worktrees may not have `.wbi` checked out
// yet, so it falls back to the main worktree.
func Find(cwd string) (*Store, error) {
	d, _ := filepath.Abs(cwd)
	for {
		if _, err := os.Stat(filepath.Join(d, ".wbi", "project.json")); err == nil {
			return &Store{Root: d}, nil
		}
		p := filepath.Dir(d)
		if p == d {
			break
		}
		d = p
	}
	if main := gitx.MainWorktree(cwd); main != "" {
		if _, err := os.Stat(filepath.Join(main, ".wbi", "project.json")); err == nil {
			return &Store{Root: main}, nil
		}
	}
	return nil, model.Errf("Not a Who Broke It? repo. Run `wbi init` first.")
}

func Exists(cwd string) bool { _, err := Find(cwd); return err == nil }

func readAll[T any](dir string) []T {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []T
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		var v T
		if json.Unmarshal(b, &v) == nil {
			out = append(out, v)
		}
	}
	return out
}

var unsafeRe = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func (s *Store) write(sub, name string, v any) error {
	// contract names like "GET /api/billing" must be filename-safe
	return writeJSON(filepath.Join(s.Dir(), sub, unsafeRe.ReplaceAllString(name, "_")+".json"), v)
}

func (s *Store) Project() (model.Project, error) {
	var p model.Project
	b, err := os.ReadFile(filepath.Join(s.Dir(), "project.json"))
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(b, &p)
}

func (s *Store) SaveProject(p model.Project) error {
	return writeJSON(filepath.Join(s.Dir(), "project.json"), p)
}

func (s *Store) Tasks() []model.Task { return readAll[model.Task](filepath.Join(s.Dir(), "tasks")) }
func (s *Store) Contracts() []model.Contract {
	return readAll[model.Contract](filepath.Join(s.Dir(), "contracts"))
}
func (s *Store) Components() []model.Component {
	return readAll[model.Component](filepath.Join(s.Dir(), "components"))
}
func (s *Store) Requirements() []model.Requirement {
	return readAll[model.Requirement](filepath.Join(s.Dir(), "requirements"))
}
func (s *Store) Decisions() []model.Decision {
	return readAll[model.Decision](filepath.Join(s.Dir(), "decisions"))
}
func (s *Store) Handoffs() []model.Handoff {
	return readAll[model.Handoff](filepath.Join(s.Dir(), "handoffs"))
}

func (s *Store) Task(id string) (model.Task, error) {
	for _, t := range s.Tasks() {
		if t.ID == id {
			return t, nil
		}
	}
	return model.Task{}, model.Errf("Unknown task %s", id)
}

func (s *Store) SaveTask(t model.Task) error { t.Normalize(); return s.write("tasks", t.ID, t) }
func (s *Store) SaveContract(c model.Contract) error {
	c.Normalize()
	return s.write("contracts", c.Name, c)
}
func (s *Store) SaveDecision(d model.Decision) error { return s.write("decisions", d.ID, d) }
func (s *Store) SaveHandoff(h model.Handoff) error {
	h.Normalize()
	return s.write("handoffs", h.TaskID, h)
}

func (s *Store) HasPlan() bool { return len(s.Tasks()) > 0 }

// WritePlan replaces tasks, contracts, components, requirements and decisions.
func (s *Store) WritePlan(p model.Plan) error {
	for _, sub := range []string{"tasks", "contracts", "components", "requirements", "decisions"} {
		_ = os.RemoveAll(filepath.Join(s.Dir(), sub))
	}
	for _, t := range p.Tasks {
		if err := s.SaveTask(t); err != nil {
			return err
		}
	}
	for _, c := range p.Contracts {
		if err := s.SaveContract(c); err != nil {
			return err
		}
	}
	for _, c := range p.Components {
		c.Normalize()
		if err := s.write("components", c.ID, c); err != nil {
			return err
		}
	}
	for _, r := range p.Requirements {
		if err := s.write("requirements", r.ID, r); err != nil {
			return err
		}
	}
	for _, d := range p.Decisions {
		if err := s.SaveDecision(d); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ReadText(rel string) string {
	b, _ := os.ReadFile(filepath.Join(s.Dir(), rel))
	return string(b)
}

func (s *Store) WriteText(rel, body string) error {
	p := filepath.Join(s.Dir(), rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(body), 0o644)
}
