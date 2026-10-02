// Package graph holds the deterministic DAG algorithms over tasks: validation, cycles, waves,
// critical path, dependents and ready/blocked derivation.
package graph

import (
	"fmt"
	"strings"

	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

type Issue struct {
	Severity string // "error" | "warn"
	Message  string
}

func index(tasks []model.Task) map[string]*model.Task {
	m := make(map[string]*model.Task, len(tasks))
	for i := range tasks {
		m[tasks[i].ID] = &tasks[i]
	}
	return m
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// FindCycle returns one dependency cycle as an id path (first == last), or nil.
func FindCycle(tasks []model.Task) []string {
	m := index(tasks)
	state := map[string]int{}
	var stack []string
	var visit func(id string) []string
	visit = func(id string) []string {
		state[id] = 1
		stack = append(stack, id)
		for _, d := range m[id].DependsOn {
			if _, ok := m[d]; !ok {
				continue
			}
			if state[d] == 1 {
				i := 0
				for ; i < len(stack); i++ {
					if stack[i] == d {
						break
					}
				}
				return append(append([]string{}, stack[i:]...), d)
			}
			if state[d] == 0 {
				if c := visit(d); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
		return nil
	}
	for _, t := range tasks {
		if state[t.ID] == 0 {
			if c := visit(t.ID); c != nil {
				return c
			}
		}
	}
	return nil
}

// Validate checks structural integrity: duplicates, unknown/self deps, contract providers/consumers, cycles.
func Validate(tasks []model.Task, contracts []model.Contract) []Issue {
	var issues []Issue
	m := index(tasks)
	cm := map[string]*model.Contract{}
	for i := range contracts {
		cm[contracts[i].Name] = &contracts[i]
	}
	seen := map[string]bool{}
	for _, t := range tasks {
		if seen[t.ID] {
			issues = append(issues, Issue{"error", "Duplicate task id " + t.ID})
		}
		seen[t.ID] = true
		for _, d := range t.DependsOn {
			if d == t.ID {
				issues = append(issues, Issue{"error", t.ID + " depends on itself"})
			} else if _, ok := m[d]; !ok {
				issues = append(issues, Issue{"error", fmt.Sprintf("%s depends on unknown task %s", t.ID, d)})
			}
		}
		for _, c := range t.Consumes {
			k, ok := cm[c]
			if !ok {
				issues = append(issues, Issue{"error", fmt.Sprintf("%s consumes unknown contract %s", t.ID, c)})
			} else if k.ProvidedBy != t.ID && !contains(Ancestors(tasks, t.ID), k.ProvidedBy) {
				issues = append(issues, Issue{"warn", fmt.Sprintf("%s consumes %s but does not depend on its provider %s", t.ID, c, k.ProvidedBy)})
			}
		}
		for _, c := range t.Provides {
			k, ok := cm[c]
			if !ok {
				issues = append(issues, Issue{"error", fmt.Sprintf("%s provides unknown contract %s", t.ID, c)})
			} else if k.ProvidedBy != t.ID {
				issues = append(issues, Issue{"error", fmt.Sprintf("Contract %s is provided by %s, not %s", c, k.ProvidedBy, t.ID)})
			}
		}
	}
	if c := FindCycle(tasks); c != nil {
		issues = append(issues, Issue{"error", "Dependency cycle: " + strings.Join(c, " → ")})
	}
	return issues
}

// Dependents returns tasks that depend on id (transitively if asked), in discovery order.
func Dependents(tasks []model.Task, id string, transitive bool) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(cur string)
	walk = func(cur string) {
		for _, t := range tasks {
			if contains(t.DependsOn, cur) && !seen[t.ID] {
				seen[t.ID] = true
				out = append(out, t.ID)
				if transitive {
					walk(t.ID)
				}
			}
		}
	}
	walk(id)
	return out
}

// Ancestors returns all transitive dependencies of id.
func Ancestors(tasks []model.Task, id string) []string {
	m := index(tasks)
	var out []string
	seen := map[string]bool{}
	var walk func(cur string)
	walk = func(cur string) {
		t, ok := m[cur]
		if !ok {
			return
		}
		for _, d := range t.DependsOn {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
				walk(d)
			}
		}
	}
	walk(id)
	return out
}

// Waves groups tasks so wave N only depends on earlier waves. Assumes no cycles.
func Waves(tasks []model.Task) [][]string {
	m := index(tasks)
	level := map[string]int{}
	var lv func(id string) int
	lv = func(id string) int {
		if l, ok := level[id]; ok {
			return l
		}
		level[id] = 0
		l := 0
		for _, d := range m[id].DependsOn {
			if _, ok := m[d]; ok {
				if x := lv(d) + 1; x > l {
					l = x
				}
			}
		}
		level[id] = l
		return l
	}
	maxL := 0
	for _, t := range tasks {
		if l := lv(t.ID); l > maxL {
			maxL = l
		}
	}
	out := make([][]string, maxL+1)
	for _, t := range tasks {
		out[level[t.ID]] = append(out[level[t.ID]], t.ID)
	}
	var res [][]string
	for _, w := range out {
		if len(w) > 0 {
			res = append(res, w)
		}
	}
	return res
}

// CriticalPath is the longest dependency chain weighted by task effort.
func CriticalPath(tasks []model.Task) []string {
	m := index(tasks)
	type best struct {
		w    int
		path []string
	}
	memo := map[string]best{}
	var f func(id string) best
	f = func(id string) best {
		if b, ok := memo[id]; ok {
			return b
		}
		top := best{}
		for _, d := range m[id].DependsOn {
			if _, ok := m[d]; ok {
				if r := f(d); r.w > top.w {
					top = r
				}
			}
		}
		res := best{top.w + m[id].Effort, append(append([]string{}, top.path...), id)}
		memo[id] = res
		return res
	}
	top := best{}
	for _, t := range tasks {
		if r := f(t.ID); r.w > top.w {
			top = r
		}
	}
	return top.path
}

// DisplayStatus derives READY/BLOCKED for TODO tasks from their dependencies.
func DisplayStatus(t model.Task, status func(id string) string) string {
	s := status(t.ID)
	if s != model.Todo {
		return s
	}
	for _, d := range t.DependsOn {
		if status(d) != model.Done {
			return model.Blocked
		}
	}
	return model.Ready
}

// Concurrent reports whether two tasks are unordered by dependencies (can run at the same time).
func Concurrent(tasks []model.Task, a, b string) bool {
	return !contains(Ancestors(tasks, a), b) && !contains(Ancestors(tasks, b), a)
}
