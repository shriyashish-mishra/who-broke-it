// Package render formats engine results for the terminal. Colors follow NO_COLOR / FORCE_COLOR / tty detection.
package render

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shriyashish-mishra/who-broke-it/internal/blast"
	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

var on = func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}()

func wrap(code string) func(string) string {
	return func(s string) string {
		if !on {
			return s
		}
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}
}

var (
	Bold, Dim, Red, Green, Yellow, Blue, Magenta, Cyan, Gray = wrap("1"), wrap("2"), wrap("31"), wrap("32"), wrap("33"), wrap("34"), wrap("35"), wrap("36"), wrap("90")
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// pad right-pads by visible width (ANSI escapes do not count).
func pad(s string, n int) string {
	w := utf8.RuneCountInString(ansiRe.ReplaceAllString(s, ""))
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

func Header() string { return Bold(Cyan("WHO BROKE IT?")) }

func Bar(pct, width int) string {
	f := pct * width / 100
	return Green(strings.Repeat("█", f)) + Gray(strings.Repeat("░", width-f))
}

// Ago renders a unix-millis timestamp as a relative time.
func Ago(ms int64) string {
	s := (time.Now().UnixMilli() - ms) / 1000
	if s < 0 {
		s = 0
	}
	switch {
	case s < 60:
		return fmt.Sprintf("%ds ago", s)
	case s < 3600:
		return fmt.Sprintf("%d min ago", (s+30)/60)
	case s < 86400:
		return fmt.Sprintf("%d h ago", (s+1800)/3600)
	}
	return fmt.Sprintf("%d d ago", (s+43200)/86400)
}

func agentName(id string) string {
	if id == "" {
		return "—"
	}
	n := strings.SplitN(id, "@", 2)[0]
	return strings.ToUpper(n[:1]) + n[1:]
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Status renders mission control.
func Status(v engine.StatusView) string {
	var L []string
	add := func(s ...string) { L = append(L, s...) }
	add(Header(), "", Bold("PROJECT:")+" "+v.Project.Name, Dim(v.Project.Goal), "",
		fmt.Sprintf("%s %s %d%%  %s", Bold("Progress:"), Bar(v.Progress.Pct, 20), v.Progress.Pct, Dim(fmt.Sprintf("(%d/%d tasks)", v.Progress.Done, v.Progress.Total))), "")
	sec := func(title string, color func(string) string, lines []string) {
		if len(lines) > 0 {
			add(color(Bold(title)))
			add(lines...)
			add("")
		}
	}
	var act []string
	for _, a := range v.Active {
		var extra []string
		if a.Ahead > 0 {
			extra = append(extra, fmt.Sprintf("%d %s", a.Ahead, plural(a.Ahead, "commit", "commits")))
		}
		if a.PR > 0 {
			extra = append(extra, fmt.Sprintf("PR #%d", a.PR))
		}
		if a.Stale {
			extra = append(extra, Yellow("stale"))
		}
		tail := ""
		if len(extra) > 0 {
			tail = Dim("  [" + strings.Join(extra, ", ") + "]")
		}
		act = append(act, fmt.Sprintf("%s %s → %s %s%s", Green("●"), pad(agentName(a.AgentID), 8), a.Task.ID, a.Task.Title, tail))
	}
	sec("ACTIVE", Green, act)
	var rev []string
	for _, r := range v.Review {
		note := Dim("  ready for review")
		if r.NeedsApproval {
			note = Yellow("  needs human approval")
		}
		rev = append(rev, fmt.Sprintf("%s %s %s%s", Magenta("◆"), r.Task.ID, r.Task.Title, note))
	}
	sec("REVIEW", Magenta, rev)
	var ready []string
	for _, t := range v.Ready {
		ready = append(ready, fmt.Sprintf("%s %s %s", Cyan("○"), t.ID, t.Title))
	}
	sec("READY", Cyan, ready)
	var blocked []string
	shown := v.Blocked
	if len(shown) > 5 {
		shown = shown[:5]
	}
	for _, b := range shown {
		blocked = append(blocked, fmt.Sprintf("%s %s %s", Gray("○"), b.Task.ID, b.Task.Title), Dim("  waiting for "+strings.Join(b.WaitingFor, ", ")))
	}
	if len(v.Blocked) > len(shown) {
		blocked = append(blocked, Dim(fmt.Sprintf("  … and %d more (wbi tasks --status BLOCKED)", len(v.Blocked)-len(shown))))
	}
	sec("BLOCKED", Gray, blocked)
	if len(v.Attention) > 0 {
		add(Yellow(Bold("ATTENTION")))
		for _, a := range v.Attention {
			add(Yellow(a))
		}
		add("")
	}
	if len(v.Active) == 0 && len(v.Ready) == 0 && len(v.Review) == 0 && v.Progress.Total > 0 && v.Progress.Done == v.Progress.Total {
		add(Green("All tasks DONE. Nobody broke it. Suspicious."), "")
	}
	return strings.Join(L, "\n")
}

// TaskRow renders one line of `wbi tasks`.
func TaskRow(t model.Task, status, owner string) string {
	col := Gray
	switch status {
	case model.Done:
		col = Green
	case model.InProgress:
		col = Blue
	case model.Ready:
		col = Cyan
	case model.Review:
		col = Magenta
	}
	o := ""
	if owner != "" {
		o = Dim("  (" + owner + ")")
	}
	return fmt.Sprintf("%s %s %s %s%s", pad(t.ID, 9), pad(col(status), 12), pad(t.Layer, 12), t.Title, o)
}

func Blast(b blast.Result) string {
	L := []string{Bold(Red("BLAST RADIUS")) + Dim(fmt.Sprintf("  %s (%s)", b.Target, b.Kind)), ""}
	list := func(title string, items []string) {
		L = append(L, Bold(title))
		if len(items) == 0 {
			L = append(L, Dim("  none"))
		}
		for _, i := range items {
			L = append(L, "  "+i)
		}
		L = append(L, "")
	}
	tasks := func(ids []string) []string {
		var out []string
		for _, t := range ids {
			out = append(out, t+"  "+Dim(b.Why[t]))
		}
		return out
	}
	var direct, indirect, agents []string
	for _, c := range b.DirectComps {
		direct = append(direct, "component "+c)
	}
	direct = append(direct, tasks(b.DirectTasks)...)
	for _, c := range b.IndirectComps {
		indirect = append(indirect, "component "+c)
	}
	indirect = append(indirect, tasks(b.IndirectTasks)...)
	for _, a := range b.ActiveAgents {
		agents = append(agents, fmt.Sprintf("%s → %s", agentName(a.AgentID), a.TaskID))
	}
	list("Direct:", direct)
	list("Indirect:", indirect)
	list("Active agents:", agents)
	col := Green
	switch b.Risk {
	case "HIGH":
		col = Red
	case "MEDIUM":
		col = Yellow
	}
	reasons := ""
	if len(b.Reasons) > 0 {
		reasons = Dim("  " + strings.Join(b.Reasons, "; "))
	}
	L = append(L, Bold("Risk:")+" "+col(Bold(b.Risk))+reasons)
	return strings.Join(L, "\n")
}

func Conflicts(cs []model.Conflict) string {
	var L []string
	for _, c := range cs {
		tag := Dim("ℹ INFO ")
		switch c.Severity {
		case model.Block:
			tag = Red("✖ BLOCK")
		case model.Warn:
			tag = Yellow("⚠ WARN ")
		}
		L = append(L, fmt.Sprintf("  %s %s %s", tag, Dim(c.Code), c.Message))
	}
	return strings.Join(L, "\n")
}

func Verification(v engine.Verification) string {
	icon := map[string]string{"pass": Green("✓"), "fail": Red("✗"), "warn": Yellow("!"), "skip": Gray("–")}
	L := []string{Bold(v.TaskID + " VERIFICATION"), ""}
	for _, c := range v.Checks {
		L = append(L, fmt.Sprintf("%s %s %s", icon[c.Status], pad(c.Name, 20), Dim(c.Detail)))
	}
	var st string
	switch v.Verdict {
	case "DONE":
		st = Green("DONE (verified)")
	case "READY_FOR_REVIEW":
		why := "high-impact"
		if len(v.PendingHuman) > 0 {
			why = "needs a person to sign off criteria " + strings.Trim(strings.Join(strings.Fields(fmt.Sprint(v.PendingHuman)), ","), "[]")
		}
		st = Magenta("READY FOR REVIEW") + Dim("  "+why+": wbi approve "+v.TaskID)
	default:
		st = Red("FAILED") + Dim("  sent back to IN_PROGRESS")
	}
	L = append(L, "", Bold("Status:")+" "+st)
	return strings.Join(L, "\n")
}

func Handoff(h model.Handoff) string {
	L := []string{Bold(h.TaskID+" HANDOFF") + Dim("  by "+h.AgentID), "", Bold("Implemented:"), "  " + h.Implemented, "", Bold("Changed:")}
	for i, f := range h.ChangedFiles {
		if i == 12 {
			L = append(L, Dim(fmt.Sprintf("  … %d more", len(h.ChangedFiles)-12)))
			break
		}
		L = append(L, "  "+f)
	}
	if len(h.ContractChanges) > 0 {
		L = append(L, "", Bold("Contract:"))
		for _, c := range h.ContractChanges {
			note := ""
			if c.Note != "" {
				note = ": " + c.Note
			}
			L = append(L, fmt.Sprintf("  %s → v%d%s", c.Name, c.Version, note))
		}
	}
	tests := "no evidence"
	if h.Tests.Ran {
		tests = h.Tests.Summary
		if tests == "" {
			tests = "failed"
			if h.Tests.Passed != nil && *h.Tests.Passed {
				tests = "passed"
			}
		}
	}
	L = append(L, "", Bold("Tests:"), "  "+tests)
	if len(h.Limitations) > 0 {
		L = append(L, "", Bold("Known limitation:"))
		for _, l := range h.Limitations {
			L = append(L, "  "+l)
		}
	}
	if len(h.Affected) > 0 {
		L = append(L, "", Bold("Affected:"))
		for _, a := range h.Affected {
			L = append(L, "  "+a)
		}
	}
	return strings.Join(L, "\n")
}

func Simulation(s engine.Simulation) string {
	assumed := ""
	if s.AgentsAssumed {
		assumed = Dim(" (assumed; none registered, use --agents N)")
	}
	L := []string{Bold(Cyan("EXECUTION PLAN")), "", fmt.Sprintf("%d tasks", s.Tasks), fmt.Sprintf("%d agents%s", s.Agents, assumed), fmt.Sprintf("%d parallelizable tasks", s.Parallelizable), "",
		Bold("Critical path:"), fmt.Sprintf("%d tasks  %s", len(s.CriticalPath), Dim(strings.Join(s.CriticalPath, " → "))), "", Bold("Potential conflicts:"), fmt.Sprint(len(s.Conflicts))}
	for i, c := range s.Conflicts {
		if i == 6 {
			break
		}
		L = append(L, Yellow(fmt.Sprintf("  %s ↔ %s: %s", c.A, c.B, c.Why)))
	}
	L = append(L, "", Bold("Missing dependencies:"), fmt.Sprint(len(s.MissingDeps)))
	for _, m := range s.MissingDeps {
		L = append(L, Yellow("  "+m))
	}
	L = append(L, "")
	for i, w := range s.Waves {
		mode := "serial"
		if len(w) > 1 {
			mode = "parallel"
		}
		L = append(L, Bold(fmt.Sprintf("WAVE %d", i+1))+Dim(fmt.Sprintf("  (%d %s, %s)", len(w), plural(len(w), "task", "tasks"), mode)))
		for _, t := range w {
			note := ""
			if len(t.Impact) > 0 {
				note = Yellow("  [needs approval: " + strings.Join(t.Impact, ", ") + "]")
			}
			L = append(L, fmt.Sprintf("  %s %s%s", t.ID, t.Title, note))
		}
		L = append(L, "")
	}
	L = append(L, Dim(fmt.Sprintf("≈ %d sequential rounds with %d agent(s).", s.MinRounds, s.Agents)))
	return strings.Join(L, "\n")
}

func Drift(d engine.DriftReport) string {
	L := []string{Bold("DRIFT REPORT"), "", Bold("Product drift")}
	ic := func(s string) string {
		switch s {
		case "done":
			return Green("✓")
		case "wip":
			return Yellow("…")
		}
		return Red("✗")
	}
	for _, r := range d.Product {
		mark := Red("✗")
		if r.Satisfied {
			mark = Green("✓")
		}
		var parts []string
		for _, l := range r.Layers {
			p := pad(l.Layer, 9) + " " + ic(l.State)
			if l.State == "missing" {
				p += Dim(" (no task)")
			}
			parts = append(parts, p)
		}
		L = append(L, fmt.Sprintf("%s %s %s", mark, r.Req, r.Text), "  "+strings.Join(parts, "   "))
	}
	L = append(L, "", Bold("Architecture drift"))
	if len(d.Architecture) == 0 {
		L = append(L, Green("  ✓ no constitution violations found"))
	}
	for _, v := range d.Architecture {
		where := v.File
		if v.Branch != "" {
			where += " on " + v.Branch + ", unmerged"
		}
		L = append(L, Red(fmt.Sprintf("  ✗ %s %s", v.Rule.ID, v.Rule.Text)), Dim(fmt.Sprintf("     %s: %s", where, v.Excerpt)))
	}
	L = append(L, "", Bold("Context drift"))
	if len(d.Context) == 0 {
		L = append(L, Green("  ✓ every open task is planned against current contracts"))
	}
	for _, x := range d.Context {
		L = append(L, Yellow(fmt.Sprintf("  ! %s was planned against %s v%d; now v%d (changed by %s) → wbi ack %s", x.Task, x.Contract, x.From, x.To, x.By, x.Task)))
	}
	return strings.Join(L, "\n")
}

func Blame(path string, r engine.BlameResult) string {
	L := []string{Bold("$ wbi blame " + path), "", Bold("Last significant changes:"), ""}
	if len(r.Entries) == 0 {
		L = append(L, Dim("  no commits touch this path yet. Nobody broke it (yet)."))
	}
	for _, e := range r.Entries {
		who := "🧑 " + pad(e.Who, 8)
		if e.IsAgent {
			who = "🤖 " + pad(strings.ToUpper(e.Provider[:1])+e.Provider[1:], 8)
		}
		pr := "        "
		if e.PR > 0 {
			pr = pad(fmt.Sprintf("PR #%d", e.PR), 8)
		}
		subj := e.Subject
		if len(subj) > 44 {
			subj = subj[:44]
		}
		tail := ""
		if e.Ref != "" && e.Ref != "main" && e.Ref != "master" {
			tail = Dim(fmt.Sprintf("  [%s, unmerged]", e.Ref))
		}
		L = append(L, fmt.Sprintf("%s %s %s %s  %s%s", who, pad(e.TaskID, 9), pr, Dim(Ago(e.TS)), Dim(subj), tail))
	}
	for _, a := range r.ContractAlerts {
		L = append(L, "", Yellow(fmt.Sprintf("⚠ Contract %s changed by %s", a.Contract, a.Task)),
			Yellow(fmt.Sprintf("→ %d downstream task(s) affected: %s", len(a.Affected), strings.Join(a.Affected, ", "))))
	}
	for _, i := range r.Active {
		L = append(L, "", Cyan(fmt.Sprintf("👀 Right now: %s declared %s %s (%s)", i.AgentID, i.Kind, i.Target, i.TaskID)))
	}
	return strings.Join(L, "\n")
}

// Check renders the merge-gate report for a terminal.
func Check(r engine.CheckReport) string {
	title := "WBI CHECK"
	if r.Task != "" {
		title += "  " + r.Task
	}
	L := []string{Bold(title) + Dim(fmt.Sprintf("  %s...%s, %d file(s)", r.Base, r.Head, len(r.Files))), ""}
	for _, f := range r.Findings {
		icon := map[string]string{"error": Red("✗"), "warn": Yellow("!"), "notice": Cyan("ℹ")}[f.Level]
		where := ""
		if f.File != "" {
			where = Dim("  " + f.File)
		}
		L = append(L, fmt.Sprintf("%s %s %s%s", icon, pad(Dim(f.Code), 22), f.Message, where))
	}
	if len(r.Findings) == 0 {
		L = append(L, Green("✓ nothing to report"))
	}
	L = append(L, "")
	if r.OK {
		L = append(L, Green(Bold("PASS")))
	} else {
		L = append(L, Red(Bold("FAIL"))+Dim("  the Engineering Graph says this change should not merge as-is"))
	}
	return strings.Join(L, "\n") + "\n"
}

// CheckGitHub renders GitHub Actions workflow commands, which show up as inline PR annotations.
func CheckGitHub(r engine.CheckReport) string {
	var b strings.Builder
	esc := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	for _, f := range r.Findings {
		lvl := map[string]string{"error": "error", "warn": "warning", "notice": "notice"}[f.Level]
		file := ""
		if f.File != "" {
			file = ",file=" + f.File
		}
		fmt.Fprintf(&b, "::%s title=%s%s::%s\n", lvl, f.Code, file, esc.Replace(f.Message))
	}
	status := "PASS"
	if !r.OK {
		status = "FAIL"
	}
	fmt.Fprintf(&b, "wbi check %s: %s (%d finding(s), %d file(s))\n", r.Task, status, len(r.Findings), len(r.Files))
	return b.String()
}

// CheckMarkdown renders the report for $GITHUB_STEP_SUMMARY.
func CheckMarkdown(r engine.CheckReport) string {
	var b strings.Builder
	status := "✅ PASS"
	if !r.OK {
		status = "❌ FAIL"
	}
	task := r.Task
	if task == "" {
		task = "no task"
	}
	fmt.Fprintf(&b, "## Who Broke It? merge gate: %s\n\n`%s` · `%s...%s` · %d file(s) changed\n\n", status, task, r.Base, r.Head, len(r.Files))
	if len(r.Findings) == 0 {
		b.WriteString("Nothing to report. Nobody broke it.\n")
		return b.String()
	}
	b.WriteString("| | Code | Finding | File |\n|---|---|---|---|\n")
	for _, f := range r.Findings {
		icon := map[string]string{"error": "❌", "warn": "⚠️", "notice": "ℹ️"}[f.Level]
		file := ""
		if f.File != "" {
			file = "`" + f.File + "`"
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n", icon, f.Code, strings.ReplaceAll(f.Message, "|", "\\|"), file)
	}
	return b.String()
}
