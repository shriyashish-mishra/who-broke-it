// Package planner inspects a repository and produces the initial Engineering Graph.
//
// The built-in planner is deliberately template-based and deterministic. For LLM-authored plans,
// use AgentPrompt (print a prompt for any agent), RunAgentCmd (pipe it to an agent CLI) or a plan file.
package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shriyashish-mishra/who-broke-it/internal/gitx"
	"github.com/shriyashish-mishra/who-broke-it/internal/graph"
	"github.com/shriyashish-mishra/who-broke-it/internal/model"
)

type Roots struct{ API, Web, DB, Migrations, Tests string }

type RepoFacts struct {
	Name       string
	Files      []string
	Languages  []string
	Frameworks []string
	TestCmd    string
	Roots      Roots
}

var langByExt = map[string]string{"ts": "TypeScript", "tsx": "TypeScript", "js": "JavaScript", "jsx": "JavaScript", "py": "Python", "go": "Go", "rs": "Rust", "java": "Java", "rb": "Ruby", "swift": "Swift", "kt": "Kotlin"}
var knownFrameworks = []string{"next", "react", "vue", "svelte", "express", "fastify", "@nestjs/core", "prisma", "drizzle-orm"}

// Inspect does deterministic repository inspection: language mix, frameworks, layout, test command.
func Inspect(root string) RepoFacts {
	var files []string
	for _, f := range gitx.LsFiles(root) {
		if !strings.HasPrefix(f, ".wbi/") {
			files = append(files, f)
		}
	}
	counts := map[string]int{}
	for _, f := range files {
		if i := strings.LastIndex(f, "."); i >= 0 {
			if l, ok := langByExt[f[i+1:]]; ok {
				counts[l]++
			}
		}
	}
	var langs []string
	for l := range counts {
		langs = append(langs, l)
	}
	sort.Slice(langs, func(i, j int) bool {
		if counts[langs[i]] != counts[langs[j]] {
			return counts[langs[i]] > counts[langs[j]]
		}
		return langs[i] < langs[j]
	})
	facts := RepoFacts{Name: filepath.Base(root), Files: files, Languages: langs}

	read := func(f string) string { b, _ := os.ReadFile(filepath.Join(root, f)); return string(b) }
	if pkg := read("package.json"); pkg != "" {
		var p struct {
			Scripts         map[string]string `json:"scripts"`
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal([]byte(pkg), &p) == nil {
			for _, f := range knownFrameworks {
				if _, ok := p.Dependencies[f]; ok {
					facts.Frameworks = append(facts.Frameworks, f)
				} else if _, ok := p.DevDependencies[f]; ok {
					facts.Frameworks = append(facts.Frameworks, f)
				}
			}
			if t := p.Scripts["test"]; t != "" && !strings.Contains(t, "no test specified") {
				facts.TestCmd = "npm test"
			}
		}
	}
	py := strings.ToLower(read("pyproject.toml") + read("requirements.txt"))
	for _, f := range []string{"django", "flask", "fastapi"} {
		if strings.Contains(py, f) {
			facts.Frameworks = append(facts.Frameworks, f)
		}
	}
	if facts.TestCmd == "" && py != "" {
		facts.TestCmd = "pytest"
	}
	if facts.TestCmd == "" && read("go.mod") != "" {
		facts.TestCmd = "go test ./..."
	}
	if facts.TestCmd == "" && read("Cargo.toml") != "" {
		facts.TestCmd = "cargo test"
	}
	pick := func(cands []string, dflt string) string {
		for _, c := range cands {
			for _, f := range files {
				if strings.HasPrefix(f, c+"/") {
					return c
				}
			}
		}
		return dflt
	}
	facts.Roots = Roots{
		API:        pick([]string{"src/api", "server", "backend", "api", "app/api"}, "src/api"),
		Web:        pick([]string{"src/web", "src/ui", "web", "frontend", "client", "src/app"}, "src/web"),
		DB:         pick([]string{"src/db", "db", "database", "prisma"}, "src/db"),
		Migrations: pick([]string{"migrations", "database/migrations", "prisma/migrations", "db/migrations"}, "migrations"),
		Tests:      pick([]string{"tests", "test", "__tests__"}, "tests"),
	}
	return facts
}

type featContract struct {
	name, kind, shape, desc string
	public                  bool
}

type feature struct {
	key, name, requirement string
	kw                     *regexp.Regexp
	contracts              []featContract
	risks                  []string
	impact                 []string
	ui                     bool
	rule                   func(r Roots, n int) string
}

func kw(s string) *regexp.Regexp { return regexp.MustCompile(`(?i)\b(` + s + `)\b`) }

var features = []feature{
	{
		key: "auth", name: "Authentication", kw: kw(`auth\w*|login|sign.?in|sso|oauth|accounts?`),
		requirement: "Users can sign up, sign in and sign out securely",
		contracts: []featContract{
			{"Session", "type", "{ userId: string; tenantId: string; expiresAt: string }", "The authenticated session every other feature reads.", false},
			{"POST /api/auth/login", "http", "body { email, password } → 200 { session: Session } | 401", "Credential login.", true},
		},
		risks:  []string{"Session fixation and token storage mistakes", "Every other feature depends on Session, so changes here ripple widely"},
		impact: []string{model.ImpactSecurity, model.ImpactPublicAPI}, ui: true,
	},
	{
		key: "billing", name: "Billing", kw: kw(`billing|payments?|subscriptions?|invoices?|stripe|checkout`),
		requirement: "Customers can subscribe, see their plan and cancel",
		contracts: []featContract{
			{"BillingStatus", "type", "'active' | 'past_due' | 'canceled'", "Subscription state shared by API, UI and analytics.", false},
			{"GET /api/billing", "http", "→ 200 { status: BillingStatus; plan: string; renewsAt: string }", "Current subscription for the session tenant.", true},
		},
		risks:  []string{"Money handling: idempotency keys and webhook retries", "Status enum changes break every consumer"},
		impact: []string{model.ImpactSecurity, model.ImpactPublicAPI}, ui: true,
		rule: func(r Roots, n int) string {
			return fmt.Sprintf("- **C%d** All billing mutations must go through BillingService. `paths: **/*` `except: %s/billing/**` `forbid: /\\b(stripe\\.(charges|subscriptions|invoices)\\.(create|update|cancel)|db\\.billing\\.(insert|update|delete))/`", n, r.API)
		},
	},
	{
		key: "analytics", name: "Analytics", kw: kw(`analytics|metrics|reports?|insights|tracking`),
		requirement: "Admins can see usage analytics for their tenant",
		contracts: []featContract{
			{"AnalyticsEvent", "event", "{ name: string; tenantId: string; at: string; props: Record<string, unknown> }", "Event emitted by features, consumed by analytics.", false},
			{"GET /api/analytics/summary", "http", "→ 200 { series: { day: string; count: number }[] }", "Aggregated usage for charts.", true},
		},
		risks: []string{"PII leaking into event properties", "Unbounded aggregation queries"}, ui: true,
	},
	{
		key: "assistant", name: "AI assistant", kw: kw(`ai|assistant|chat\w*|llm|copilot|agent`),
		requirement: "Users can ask the AI assistant questions about their data",
		contracts: []featContract{
			{"AssistantMessage", "type", `{ role: "user" | "assistant"; content: string; at: string }`, "Message shape for the assistant thread.", false},
			{"POST /api/assistant/chat", "http", "body { messages: AssistantMessage[] } → 200 stream", "Streaming chat endpoint.", true},
		},
		risks: []string{"Prompt injection from user-supplied data", "Unbounded model spend without per-tenant limits"}, ui: true,
		rule: func(r Roots, n int) string {
			return fmt.Sprintf("- **C%d** Never interpolate raw user input into prompts; use the sanitizer in %s/assistant.", n, r.API)
		},
	},
	{
		key: "notifications", name: "Notifications", kw: kw(`notifications?|alerts?|emails?`),
		requirement: "Users are notified about events they care about",
		contracts:   []featContract{{"Notification", "type", "{ id: string; userId: string; kind: string; readAt?: string }", "Notification record.", false}},
		risks:       []string{"Duplicate sends on retry"}, ui: true,
	},
}

var (
	multiTenantRe = regexp.MustCompile(`(?i)multi.?tenan|tenant|workspaces?|organi[sz]ations?`)
	mobileRe      = regexp.MustCompile(`(?i)\b(mobile|ios|android|react native|flutter)\b`)
	uiGoalRe      = regexp.MustCompile(`(?i)dashboard|portal|frontend|\bweb\b|\bui\b`)
	buildPrefixRe = regexp.MustCompile(`(?i)^build\s+`)
)

// HeuristicPlan is the offline template planner: a conservative, valid first draft.
func HeuristicPlan(goal string, facts RepoFacts) model.Plan {
	R := facts.Roots
	now := time.Now().UTC().Format(time.RFC3339)
	var feats []feature
	for _, f := range features {
		if f.kw.MatchString(goal) {
			feats = append(feats, f)
		}
	}
	if len(feats) == 0 {
		req := buildPrefixRe.ReplaceAllString(goal, "")
		req = strings.TrimSuffix(req, ".")
		if req != "" {
			req = strings.ToUpper(req[:1]) + req[1:]
		}
		feats = []feature{{
			key: "core", name: "Core feature", requirement: req,
			contracts: []featContract{
				{"CoreModel", "type", "{ id: string }", "Domain model for the core feature (refine in TASK-001).", false},
				{"GET /api/core", "http", "→ 200 CoreModel[]", "Core read endpoint.", true},
			},
			risks: []string{"Scope is unclear: refine requirements in TASK-001 first"}, ui: true,
		}}
	}
	multiTenant := multiTenantRe.MatchString(goal)
	mobile := mobileRe.MatchString(goal)
	hasAuth := false
	for _, f := range feats {
		if f.key == "auth" {
			hasAuth = true
		}
	}

	var tasks []*model.Task
	var contracts []model.Contract
	var components []model.Component
	var requirements []model.Requirement
	var decisions []model.Decision
	n := 0

	runTests := model.Criterion{Text: "Tests for this task pass"}
	if facts.TestCmd != "" {
		runTests = model.Criterion{Text: fmt.Sprintf("Test suite passes (%s)", facts.TestCmd), Check: &model.Check{Type: "command", Cmd: facts.TestCmd}}
	}
	mk := func(t model.Task) *model.Task {
		n++
		t.ID = fmt.Sprintf("TASK-%03d", n)
		if t.Effort == 0 {
			t.Effort = 2
		}
		t.CreatedAt = now
		t.Normalize()
		for _, c := range append(append([]string{}, t.Provides...), t.Consumes...) {
			t.ContractVersion[c] = 1
		}
		p := &t
		tasks = append(tasks, p)
		return p
	}
	relevant := func(key string) []string {
		var out []string
		for _, f := range facts.Files {
			if strings.Contains(strings.ToLower(f), key) && len(out) < 8 {
				out = append(out, f)
			}
		}
		return out
	}
	prefixed := func(prefix ...string) []string {
		var out []string
		for _, f := range facts.Files {
			for _, p := range prefix {
				if strings.HasPrefix(f, p+"/") && len(out) < 8 {
					out = append(out, f)
					break
				}
			}
		}
		return out
	}

	reqIDs := map[string]string{}
	var allReqs []string
	for i, f := range feats {
		id := fmt.Sprintf("REQ-%03d", i+1)
		reqIDs[f.key] = id
		allReqs = append(allReqs, id)
		layers := []string{"database", "backend"}
		if f.ui {
			layers = append(layers, "frontend")
		}
		layers = append(layers, "testing")
		requirements = append(requirements, model.Requirement{ID: id, Text: f.requirement, Layers: layers})
	}

	decisions = append(decisions, model.Decision{ID: "ADR-001", Title: "Contract-first parallel development", Status: "accepted", CreatedAt: now,
		Why: "Backend and UI tasks are decoupled by explicit contracts so humans and agents can work in parallel; changing a contract is a tracked event that propagates to consumers."})
	adr2 := model.Decision{ID: "ADR-002", CreatedAt: now}
	if len(facts.Frameworks) > 0 {
		adr2.Title, adr2.Status, adr2.Why = "Build on the existing stack ("+strings.Join(facts.Frameworks, ", ")+")", "accepted", "Detected from the repository; do not introduce parallel frameworks."
	} else {
		adr2.Title, adr2.Status, adr2.Why = "Stack to be chosen in the architecture task", "proposed", "No framework was detected in the repository."
	}
	decisions = append(decisions, adr2)
	if multiTenant {
		decisions = append(decisions, model.Decision{ID: "ADR-003", Title: "Tenant isolation via tenant_id on every row", Status: "proposed", CreatedAt: now,
			Why: "Simple, auditable isolation. Every query must be tenant-scoped."})
	}

	// Foundation
	components = append(components, model.Component{ID: "platform", Name: "Platform & architecture", Description: "Contracts, constitution and architecture docs.", Paths: []string{".wbi/**", "docs/**"}})
	var adrIDs []string
	for _, d := range decisions {
		adrIDs = append(adrIDs, d.ID)
	}
	arch := mk(model.Task{
		Title: "Architecture & contracts", Goal: "Review the generated plan, finalize contracts and the constitution so parallel work can start.", Layer: "architecture", Component: "platform",
		Requirements: allReqs, AllowedPaths: []string{".wbi/**", "docs/**"}, Effort: 1, Decisions: adrIDs,
		Acceptance: []model.Criterion{{Text: "Every contract in .wbi/contracts reviewed by its provider and consumers", Human: true}, {Text: "Constitution rules agreed by the team", Human: true}},
		Risks:      []string{"Skipping this review is how parallel agents diverge"},
	})
	components = append(components, model.Component{ID: "database", Name: "Database", Description: "Schema and migrations.", Paths: []string{R.DB + "/**", R.Migrations + "/**"}, DependsOn: []string{"platform"}})
	var names []string
	for _, f := range feats {
		names = append(names, f.name)
	}
	dbGoal := "Design and migrate the schema for: " + strings.Join(names, ", ")
	dbRisks := []string{"Migrations are hard to reverse once shared"}
	var dbDecisions []string
	if multiTenant {
		dbGoal += " (tenant-scoped)"
		dbRisks = append(dbRisks, "Missing tenant_id on a table leaks data across tenants")
		dbDecisions = []string{"ADR-003"}
	}
	db := mk(model.Task{
		Title: "Database schema & migrations", Goal: dbGoal + ".", Layer: "database", Component: "database", Requirements: allReqs, DependsOn: []string{arch.ID},
		AllowedPaths: []string{R.DB + "/**", R.Migrations + "/**"}, Effort: 3, Impact: []string{model.ImpactMigration},
		RelevantFiles: prefixed(R.DB, R.Migrations), Decisions: dbDecisions, Risks: dbRisks,
		Acceptance: []model.Criterion{{Text: "Schema covers every entity needed by the listed requirements"}, {Text: "Migration applies cleanly on an empty database and is reversible"}},
	})

	needsUI := uiGoalRe.MatchString(goal)
	for _, f := range feats {
		if f.ui {
			needsUI = true
		}
	}
	var shell *model.Task
	if needsUI {
		components = append(components, model.Component{ID: "web-shell", Name: "Web shell", Description: "App layout, routing, shared UI components.", Paths: []string{R.Web + "/shell/**", R.Web + "/components/**"}, DependsOn: []string{"platform"}})
		shell = mk(model.Task{
			Title: "Web app shell", Goal: "App layout, routing, design system and an authenticated-route wrapper that features plug into.", Layer: "frontend", Component: "web-shell",
			Requirements: allReqs, DependsOn: []string{arch.ID}, AllowedPaths: []string{R.Web + "/shell/**", R.Web + "/components/**"}, RestrictedPaths: []string{R.API + "/**", R.Migrations + "/**"}, Effort: 2,
			Acceptance: []model.Criterion{{Text: "Shell renders with navigation placeholders for each feature"}, runTests},
		})
	}

	var testTasks []*model.Task
	var authAPI *model.Task
	ordered := make([]feature, 0, len(feats))
	for _, f := range feats {
		if f.key == "auth" {
			ordered = append(ordered, f)
		}
	}
	for _, f := range feats {
		if f.key != "auth" {
			ordered = append(ordered, f)
		}
	}
	for _, f := range ordered {
		req := reqIDs[f.key]
		apiComp := f.key + "-api"
		apiPaths := []string{R.API + "/" + f.key + "/**"}
		compDeps := []string{"database"}
		if hasAuth && f.key != "auth" {
			compDeps = append(compDeps, "auth-api")
		}
		components = append(components, model.Component{ID: apiComp, Name: f.name + " API", Description: f.name + " backend.", Paths: apiPaths, DependsOn: compDeps})
		deps := []string{db.ID}
		restricted := []string{R.Migrations + "/**", R.Web + "/**"}
		var consumes []string
		if authAPI != nil && f.key != "auth" {
			deps = append(deps, authAPI.ID)
		}
		if hasAuth && f.key != "auth" {
			restricted = append(restricted, R.API+"/auth/**")
			consumes = []string{"Session"}
		}
		lower := strings.ToLower(f.name)
		var cnames []string
		for _, c := range f.contracts {
			cnames = append(cnames, c.name)
		}
		api := mk(model.Task{
			Title: f.name + " API", Goal: "Implement the " + lower + " backend and the contracts other tasks build on.", Layer: "backend", Component: apiComp, Requirements: []string{req},
			DependsOn: deps, AllowedPaths: apiPaths, RestrictedPaths: restricted, RelevantFiles: relevant(f.key), Consumes: consumes, Impact: f.impact, Risks: f.risks, Effort: 3, Decisions: []string{"ADR-001"},
			Acceptance: []model.Criterion{{Text: "Implements " + strings.Join(cnames, " and ") + " exactly as specified in .wbi/contracts"}, {Text: "Validation and error responses handled for every endpoint"}, runTests},
		})
		for _, c := range f.contracts {
			contracts = append(contracts, model.Contract{Name: c.name, Kind: c.kind, Version: 1, ProvidedBy: api.ID, Shape: c.shape, Description: c.desc, Public: c.public, History: []model.HistoryEntry{}})
			api.Provides = append(api.Provides, c.name)
			api.ContractVersion[c.name] = 1
		}
		if f.key == "auth" {
			authAPI = api
		}
		var ui *model.Task
		if f.ui {
			uiComp := f.key + "-ui"
			uiPaths := []string{R.Web + "/" + f.key + "/**"}
			cdeps := []string{apiComp}
			udeps := []string{api.ID}
			if shell != nil {
				cdeps = append(cdeps, "web-shell")
				udeps = append(udeps, shell.ID)
			}
			components = append(components, model.Component{ID: uiComp, Name: f.name + " UI", Description: f.name + " screens.", Paths: uiPaths, DependsOn: cdeps})
			uconsumes := append([]string{}, api.Provides...)
			if hasAuth && f.key != "auth" {
				uconsumes = append(uconsumes, "Session")
			}
			ui = mk(model.Task{
				Title: f.name + " UI", Goal: "Build the " + lower + " screens against the published contracts.", Layer: "frontend", Component: uiComp, Requirements: []string{req},
				DependsOn: udeps, AllowedPaths: uiPaths, RestrictedPaths: []string{R.API + "/**", R.Migrations + "/**"}, RelevantFiles: relevant(f.key), Consumes: uconsumes, Effort: 2,
				Risks:      []string{"UI redefining contract types instead of importing them"},
				Acceptance: []model.Criterion{{Text: "Consumes " + strings.Join(api.Provides, ", ") + " without redefining types"}, {Text: "Loading, empty and error states handled"}, runTests},
			})
		}
		tdeps := []string{api.ID}
		if ui != nil {
			tdeps = append(tdeps, ui.ID)
		}
		testTasks = append(testTasks, mk(model.Task{
			Title: f.name + " end-to-end tests", Goal: "Cover the critical " + lower + " path end to end.", Layer: "testing", Component: apiComp, Requirements: []string{req},
			DependsOn: tdeps, AllowedPaths: []string{R.Tests + "/" + f.key + "/**"}, RestrictedPaths: []string{R.API + "/**", R.Web + "/**"}, Consumes: api.Provides, Effort: 1,
			Acceptance: []model.Criterion{{Text: "Critical " + lower + " path covered end to end"}, runTests},
		}))
	}

	var mobileTask *model.Task
	if mobile {
		var apiComps, apiTaskIDs, apiProvides []string
		for _, f := range feats {
			apiComps = append(apiComps, f.key+"-api")
		}
		for _, t := range tasks {
			if t.Layer == "backend" {
				apiTaskIDs = append(apiTaskIDs, t.ID)
				apiProvides = append(apiProvides, t.Provides...)
			}
		}
		components = append(components, model.Component{ID: "mobile", Name: "Mobile client", Description: "Mobile app consuming the API.", Paths: []string{"mobile/**"}, DependsOn: apiComps})
		mobileTask = mk(model.Task{
			Title: "Mobile client", Goal: "Mobile app consuming the published API contracts.", Layer: "mobile", Component: "mobile", Requirements: allReqs,
			DependsOn: apiTaskIDs, AllowedPaths: []string{"mobile/**"}, RestrictedPaths: []string{R.API + "/**", R.Migrations + "/**"}, Consumes: apiProvides, Effort: 3,
			Risks:      []string{"Every API contract change affects the mobile client"},
			Acceptance: []model.Criterion{{Text: "Mobile app builds and exercises every consumed contract"}, runTests},
		})
	}
	var apiCompIDs []string
	for _, c := range components {
		if strings.HasSuffix(c.ID, "-api") {
			apiCompIDs = append(apiCompIDs, c.ID)
		}
	}
	components = append(components, model.Component{ID: "integration", Name: "Integration", Description: "Cross-feature verification.", Paths: []string{R.Tests + "/e2e/**"}, DependsOn: apiCompIDs})
	var finalDeps []string
	for _, t := range testTasks {
		finalDeps = append(finalDeps, t.ID)
	}
	if mobileTask != nil {
		finalDeps = append(finalDeps, mobileTask.ID)
	}
	mk(model.Task{
		Title: "Integration tests", Goal: "Verify all features work together in a clean environment.", Layer: "integration", Component: "integration", Requirements: allReqs,
		DependsOn: finalDeps, AllowedPaths: []string{R.Tests + "/e2e/**"}, RestrictedPaths: []string{R.API + "/**", R.Web + "/**"}, Effort: 2,
		Acceptance: []model.Criterion{{Text: "Full flow passes from a clean checkout"}, runTests},
	})

	for _, t := range tasks {
		if t.Layer == "backend" {
			decisions[0].Tasks = append(decisions[0].Tasks, t.ID)
		}
	}
	var rules []string
	rn := 6
	for _, f := range feats {
		if f.rule != nil {
			rules = append(rules, f.rule(R, rn))
			rn++
		}
	}
	if multiTenant {
		rules = append(rules, fmt.Sprintf("- **C%d** Every database query must be tenant-scoped (ADR-003).", rn))
	}

	plan := model.Plan{Components: components, Requirements: requirements, Contracts: contracts, Decisions: decisions, ConstitutionRules: rules}
	for _, t := range tasks {
		plan.Tasks = append(plan.Tasks, *t)
	}
	plan.Normalize(now)
	return plan
}

// ParsePlan accepts sparse hand/agent-written plan JSON and fills in defaults.
func ParsePlan(data []byte) (model.Plan, error) {
	var p model.Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return p, model.Errf("Plan JSON is invalid: %v", err)
	}
	if len(p.Tasks) == 0 {
		return p, model.Errf(`Plan JSON must contain a "tasks" array.`)
	}
	for i := range p.Tasks {
		if p.Tasks[i].ID == "" {
			p.Tasks[i].ID = fmt.Sprintf("TASK-%03d", i+1)
		}
	}
	p.Normalize(time.Now().UTC().Format(time.RFC3339))
	return p, nil
}

// AssertValid rejects plans with structural errors (cycles, unknown dependencies, bad contracts).
func AssertValid(p model.Plan) error {
	var errs []string
	for _, i := range graph.Validate(p.Tasks, p.Contracts) {
		if i.Severity == "error" {
			errs = append(errs, "  - "+i.Message)
		}
	}
	if len(errs) > 0 {
		return model.Errf("Invalid plan:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

// AgentPrompt is a self-contained prompt that makes any LLM agent author a plan as JSON.
func AgentPrompt(goal string, f RepoFacts) string {
	rootsJSON, _ := json.Marshal(f.Roots)
	sample := f.Files
	if len(sample) > 40 {
		sample = sample[:40]
	}
	orNone := func(s string) string {
		if s == "" {
			return "none detected"
		}
		return s
	}
	return fmt.Sprintf(`You are the planning agent for a software project coordinated by Who Broke It? (wbi).

PRODUCT GOAL
%s

REPOSITORY FACTS
- languages: %s
- frameworks: %s
- test command: %s
- suggested roots: %s
- existing files (sample): %s

Produce ONLY one JSON object (no prose) with this shape:
{
  "components": [{"id","name","description","paths":[glob],"dependsOn":[componentId]}],
  "requirements": [{"id":"REQ-001","text","layers":["database","backend","frontend","testing"]}],
  "contracts": [{"name","kind":"type|http|event|function","version":1,"providedBy":"TASK-00N","shape","description","public":bool}],
  "tasks": [{"id":"TASK-001","title","goal","layer","component","requirements":[REQ],"dependsOn":[TASK],
     "allowedPaths":[glob],"restrictedPaths":[glob],"relevantFiles":[path],"provides":[contractName],"consumes":[contractName],
     "acceptance":[{"text","check":{"type":"command","cmd":"..."}}],"risks":[string],"impact":["migration|security|public-api|infra"],"effort":1-5}],
  "decisions": [{"id":"ADR-001","title","why","status":"accepted|proposed","tasks":[]}],
  "constitutionRules": ["- **C6** rule text `+"`forbid: /regex/`"+`"]
}

Rules: tasks form a DAG (no cycles). A task that consumes a contract must depend on the task that provides it.
Allowed paths of tasks that can run in parallel must not overlap. Maximize parallelism: contract-first, so UI and API tasks can run side by side.
Mark migrations, security-sensitive and public API work with the matching "impact" so humans approve them.
`, goal, orNone(strings.Join(f.Languages, ", ")), orNone(strings.Join(f.Frameworks, ", ")), orNone(f.TestCmd), rootsJSON, orNone(strings.Join(sample, ", ")))
}

// RunAgentCmd pipes the prompt to a shell command (any agent CLI) and extracts the JSON object it prints.
func RunAgentCmd(cmd, prompt string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Stdin = strings.NewReader(prompt)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return nil, model.Errf("Agent command failed: %v\n%s", err, strings.TrimSpace(errb.String()))
	}
	s := out.String()
	a, b := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if a < 0 || b < a {
		return nil, model.Errf("Agent command did not return a JSON object.")
	}
	return []byte(s[a : b+1]), nil
}

func RenderProjectDoc(p model.Project, plan model.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n> %s\n\nGenerated by `wbi plan` on %s. The Engineering Graph lives in `.wbi/`; run `wbi status` for the live view.\n\n## Requirements\n\n", p.Name, p.Goal, p.CreatedAt[:10])
	for _, r := range plan.Requirements {
		fmt.Fprintf(&b, "- **%s** %s _(layers: %s)_\n", r.ID, r.Text, strings.Join(r.Layers, ", "))
	}
	b.WriteString("\n## Work breakdown\n\n")
	for _, t := range plan.Tasks {
		after := ""
		if len(t.DependsOn) > 0 {
			after = ", after " + strings.Join(t.DependsOn, ", ")
		}
		fmt.Fprintf(&b, "- **%s** %s _(%s%s)_\n", t.ID, t.Title, t.Layer, after)
	}
	return b.String()
}

var nonWord = regexp.MustCompile(`\W`)

func RenderArchitectureDoc(plan model.Plan) string {
	var b strings.Builder
	b.WriteString("# Architecture\n\n## Components\n\n```mermaid\ngraph TD\n")
	for _, c := range plan.Components {
		fmt.Fprintf(&b, "  %s[\"%s\"]\n", nonWord.ReplaceAllString(c.ID, "_"), c.Name)
	}
	for _, c := range plan.Components {
		for _, d := range c.DependsOn {
			fmt.Fprintf(&b, "  %s --> %s\n", nonWord.ReplaceAllString(c.ID, "_"), nonWord.ReplaceAllString(d, "_"))
		}
	}
	b.WriteString("```\n\n| Component | Paths |\n|---|---|\n")
	for _, c := range plan.Components {
		var ps []string
		for _, p := range c.Paths {
			ps = append(ps, "`"+p+"`")
		}
		fmt.Fprintf(&b, "| %s | %s |\n", c.Name, strings.Join(ps, ", "))
	}
	b.WriteString("\n## Contracts\n\n| Contract | Kind | Provided by | Shape |\n|---|---|---|---|\n")
	for _, c := range plan.Contracts {
		fmt.Fprintf(&b, "| `%s` | %s | %s | `%s` |\n", c.Name, c.Kind, c.ProvidedBy, strings.ReplaceAll(c.Shape, "|", "\\|"))
	}
	b.WriteString("\n## Decisions\n\n")
	for _, d := range plan.Decisions {
		fmt.Fprintf(&b, "- **%s** %s _(%s)_: %s\n", d.ID, d.Title, d.Status, d.Why)
	}
	return b.String()
}
