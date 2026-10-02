// Package dashboard renders the Engineering Graph as one self-contained HTML file (data inlined, no network,
// no build step). Open it locally, commit it, or host it anywhere static. `wbi dashboard --serve` keeps it live.
package dashboard

import (
	_ "embed"
	"encoding/json"
	"html"
	"strings"

	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

//go:embed template.html
var tpl string

// Data is everything the page needs, in one JSON document.
type Data struct {
	Project struct {
		Name string `json:"name"`
		Goal string `json:"goal"`
	} `json:"project"`
	Generated string                  `json:"generated"`
	Status    engine.StatusView       `json:"status"`
	Tasks     []TaskView              `json:"tasks"`
	Contracts []model.Contract        `json:"contracts"`
	Drift     engine.DriftReport      `json:"drift"`
	Simulate  engine.Simulation       `json:"simulate"`
	Intents   []model.Intent          `json:"intents"`
	Handoffs  []model.Handoff         `json:"handoffs"`
	Blast     map[string]blastSummary `json:"blast"`
}

type TaskView struct {
	model.Task
	Display string `json:"display"`
	Agent   string `json:"agent,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Branch  string `json:"branch,omitempty"`
}

type blastSummary struct {
	Direct   []string `json:"direct"`
	Indirect []string `json:"indirect"`
	Risk     string   `json:"risk"`
}

// Collect gathers the live state.
func Collect(e *engine.Engine, now string) Data {
	var d Data
	p := e.Project()
	d.Project.Name, d.Project.Goal = p.Name, p.Goal
	d.Generated = now
	d.Status = e.Status()
	states := e.States()
	for _, t := range e.Tasks() {
		st := states[t.ID]
		d.Tasks = append(d.Tasks, TaskView{Task: t, Display: e.Display(t, states), Agent: st.AgentID, Owner: st.Owner, Branch: st.Branch})
	}
	d.Contracts = e.Contracts()
	d.Drift = e.Drift()
	d.Simulate = e.Simulate(0)
	d.Intents = e.Intents(false)
	for _, t := range e.Tasks() {
		if h := e.GetHandoff(t.ID); h != nil {
			d.Handoffs = append(d.Handoffs, *h)
		}
	}
	d.Blast = map[string]blastSummary{}
	for _, c := range d.Contracts {
		b := e.Blast(c.Name)
		d.Blast[c.Name] = blastSummary{Direct: b.DirectTasks, Indirect: b.IndirectTasks, Risk: b.Risk}
	}
	return d
}

// HTML returns the complete page for the data.
func HTML(d Data) string {
	b, _ := json.Marshal(d)
	// "</" inside inlined JSON could close the script tag early
	js := strings.ReplaceAll(string(b), "</", `<\/`)
	out := strings.Replace(tpl, "/*__WBI_DATA__*/null", js, 1)
	return strings.Replace(out, "__WBI_TITLE__", html.EscapeString(d.Project.Name), 1)
}
