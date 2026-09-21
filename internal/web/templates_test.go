package web

import (
	"strings"
	"testing"
)

// Template parsing happens during application construction, not Go package
// initialization. Keep that runtime-only failure in the normal test gate so a
// malformed template can never reach a service restart unnoticed.
func TestEmbeddedTemplatesParse(t *testing.T) {
	if _, err := parseTemplates(); err != nil {
		t.Fatalf("embedded templates must parse: %v", err)
	}
}

func TestBoardFiltersContract(t *testing.T) {
	page, err := files.ReadFile("templates/board.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, required := range []string{"data-board-filter", "data-filter-search", "data-filter=\"column\"", "data-filter=\"priority\"", "data-filter=\"label\"", "data-filter-reset", "data-filter-count", "data-filter-empty"} {
		if !strings.Contains(html, required) {
			t.Fatalf("board filter markup is missing %q", required)
		}
	}
	card, err := files.ReadFile("templates/task-card.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"data-title", "data-description", "data-column", "data-priority", "data-labels"} {
		if !strings.Contains(string(card), required) {
			t.Fatalf("task filter data is missing %q", required)
		}
	}
	script, err := files.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"shipyard.board-filters.", "toLocaleLowerCase", "setTimeout(render,180)", "data-clear-filter"} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("board filter behavior is missing %q", required)
		}
	}
}

func TestSecretsTemplatePreservesExistingAgentAssignments(t *testing.T) {
	page, err := files.ReadFile("templates/secrets.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	if !strings.Contains(html, `name="agent_id" value="{{.ID}}"{{if hasAgent .ID $.AgentIDs}} checked{{end}}`) {
		t.Fatal("secrets template must preselect persisted agent assignments")
	}
}

func TestAgentsTemplateBindsModelEffortAndEscalationToTheAgent(t *testing.T) {
	page, err := files.ReadFile("templates/agents.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, required := range []string{
		`{{$agent := .}}`,
		`name="model"`,
		`{{if eq . $agent.Model}}`,
		`name="reasoning_effort"`,
		`{{if eq . $agent.ReasoningEffort}}`,
		`name="escalation_policy"`,
		`{{.EscalationPolicy}}`,
		`CreateModels`,
		`CreateEfforts`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("agents template is missing %q", required)
		}
	}
	if strings.Contains(html, `{{if eq . $.Model}}`) || strings.Contains(html, `{{if eq . $.ReasoningEffort}}`) {
		t.Fatal("agents template must not bind model/effort to the page root")
	}
}

func TestRunTemplateShowsEscalationSelection(t *testing.T) {
	page, err := files.ReadFile("templates/run.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, required := range []string{"Modell &amp; Eskalation", ".Selection.Model", ".Selection.Effort", ".Selection.Stage", ".Selection.PolicyVersion"} {
		if !strings.Contains(html, required) {
			t.Fatalf("run template is missing %q", required)
		}
	}
}

func TestAgentsTemplatePreservesSandboxProfileActiveState(t *testing.T) {
	page, err := files.ReadFile("templates/agents.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	if strings.Contains(html, `name="active" value="true" checked>`) {
		t.Fatal("sandbox profile active checkbox must not be statically checked")
	}
	if !strings.Contains(html, `name="active" value="true"{{if .Active}} checked{{end}}`) {
		t.Fatal("sandbox profile active checkbox must reflect persisted state")
	}
	if !strings.Contains(html, `name="adapter"`) || !strings.Contains(html, `{{range $.Providers}}`) {
		t.Fatal("agents template must let operators pick any registered provider")
	}
}

func TestProviderTemplateRequiresAgentContextForConnectionTests(t *testing.T) {
	page, err := files.ReadFile("templates/providers.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, required := range []string{
		"/settings/secrets",
		"Agent für Verbindungstest",
		"data-provider-agent-select",
		"required",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("provider template must contain %q", required)
		}
	}

	script, err := files.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)
	for _, required := range []string{
		"data-provider-agent-select",
		"localStorage.setItem('shipyard.provider-test-agent'",
		"new URLSearchParams({agent_id:agent})",
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("provider test script must contain %q", required)
		}
	}
}

func TestTaskTabsRemainAccessibleAndKeepChangesActionsInHeader(t *testing.T) {
	page, err := files.ReadFile("templates/task.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	backlink := strings.Index(html, `class="task-nav"`)
	tabs := strings.Index(html, `role="tablist"`)
	title := strings.Index(html, `class="task-hero"`)
	if backlink < 0 || tabs < 0 || title < 0 || !(backlink < tabs && tabs < title) {
		t.Fatalf("task header order must be board backlink, tabs, title: backlink=%d tabs=%d title=%d", backlink, tabs, title)
	}
	for _, required := range []string{
		`id="tab-conversation" role="tab"`, `id="tab-info" role="tab"`, `id="tab-changes" role="tab"`,
		`role="tabpanel" aria-labelledby="tab-conversation"`, `role="tabpanel" aria-labelledby="tab-info"`, `role="tabpanel" aria-labelledby="tab-changes"`,
		`action="/runs/{{.ID}}/apply"`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("task tabs contract is missing %q", required)
		}
	}
	script, err := files.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(script)
	for _, required := range []string{"history.pushState", "addEventListener('popstate'", "ArrowRight", "tabIndex=selected?0:-1", "panel.hidden"} {
		if !strings.Contains(js, required) {
			t.Fatalf("task tab behavior contract is missing %q", required)
		}
	}
}

func TestTelemetryBreakdownIsVisibleInClassicViews(t *testing.T) {
	run, err := files.ReadFile("templates/run.html")
	if err != nil {
		t.Fatal(err)
	}
	runHTML := string(run)
	for _, required := range []string{"Token-Breakdown", ".Usage.TotalTokens", ".Usage.CachedInputTokens", ".Usage.ReasoningTokens"} {
		if !strings.Contains(runHTML, required) {
			t.Fatalf("classic run detail is missing %q", required)
		}
	}
	dashboard, err := files.ReadFile("templates/dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	dashboardHTML := string(dashboard)
	for _, required := range []string{"Tokenumfang", ".Dashboard.UsageTokenBreakdown.TotalTokens", ".Dashboard.UsageTokenBreakdown.CachedInputTokens"} {
		if !strings.Contains(dashboardHTML, required) {
			t.Fatalf("classic dashboard is missing %q", required)
		}
	}
}

func TestRunTemplateShowsGitIntegrationMetadata(t *testing.T) {
	page, err := files.ReadFile("templates/run.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, required := range []string{".Delivery.IntegrationBranch", ".Delivery.IntegrationBaseSHA", ".Delivery.IntegrationHeadSHA", ".Delivery.IntegrationStatus", ".Delivery.PRURL", ".Delivery.PRNumber"} {
		if !strings.Contains(html, required) {
			t.Fatalf("run template is missing integration metadata %q", required)
		}
	}
	section := strings.Index(html, `<h2>Git-Integration</h2>`)
	closingHTML := strings.Index(html, `</html>`)
	if section < 0 || closingHTML < 0 || section > closingHTML {
		t.Fatalf("git integration metadata must be inside the HTML document")
	}
}

// Navigation is progressively enhanced by app.js so every legacy, server
// rendered view receives the same complete route set. This contract prevents
// a new page from silently dropping Runs, Audit or Settings again when an old
// inline sidebar is copied into a template.
func TestCanonicalNavigationIsPresentOnEveryApplicationPage(t *testing.T) {
	script, err := files.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read navigation script: %v", err)
	}
	for _, route := range []string{"'/'", "'/projects'", "'/boards'", "'/agents'", "'/automations'", "'/skills'", "'/runs'", "'/audit'", "'/settings/providers'"} {
		if !strings.Contains(string(script), route) {
			t.Fatalf("canonical navigation is missing route %s", route)
		}
	}
	for _, name := range []string{
		"account.html", "agent-policy.html", "agents.html", "appearance.html", "audit.html", "automations.html", "board.html", "boards.html", "dashboard.html", "integrations.html", "projects.html", "providers.html", "run.html", "runs.html", "schedules.html", "skills.html", "task.html", "webhooks.html", "workflow.html",
	} {
		page, readErr := files.ReadFile("templates/" + name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if !strings.Contains(string(page), "/static/app.js") {
			t.Fatalf("%s must load the canonical navigation script", name)
		}
	}
}

// The compact rail and the mobile drawer have regressed several times because
// they are shared by every page but implemented in one stylesheet. Keep the
// non-negotiable layout and theme rules in the normal Go test gate: a compact
// rail has no trailing border, its menu can scroll on a short viewport, and
// the explicit as well as OS-driven dark modes provide the same palette.
func TestNavigationAndThemeStyleContract(t *testing.T) {
	styles, err := files.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("read application styles: %v", err)
	}
	css := string(styles)
	for _, required := range []string{
		".app-sidebar{position:fixed",
		".app-sidebar{position:fixed;inset:0 auto 0 0;z-index:30;width:var(--nav-w);background:var(--nav);color:var(--nav-text);border-right:0",
		".sidebar-expanded .app-sidebar{width:var(--rail-w);border-right:1px solid var(--nav-line)}",
		".app-sidebar nav{display:flex;flex:1;min-height:0;flex-direction:column;gap:.18rem;overflow-y:auto",
		"html[data-theme=\"dark\"]{color-scheme:dark;--ink:",
		"@media(prefers-color-scheme:dark){html:not([data-theme=\"light\"]){color-scheme:dark;--ink:",
		"--canvas:#10191f",
		"--surface:#17242d",
		"--accent:#55c3d3",
	} {
		if !strings.Contains(css, required) {
			t.Fatalf("application style contract is missing %q", required)
		}
	}
}

// The skills page intentionally has one focused action: discover a skill,
// inspect its publisher, then install it. Source administration and catalog
// refresh controls made this workflow difficult to scan and must not return.
func TestSkillsDiscoveryAndDesignSystemContract(t *testing.T) {
	page, err := files.ReadFile("templates/skills.html")
	if err != nil {
		t.Fatalf("read skills page: %v", err)
	}
	html := string(page)
	for _, required := range []string{
		"skills.sh-Katalog", "action=\"/skills\"", "action=\"/skills/skills-sh/install\"",
		"Details und Prüfungen auf skills.sh", "Installieren", "Für Agents verfügbar",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("skills page is missing %q", required)
		}
	}
	for _, removed := range []string{"Quelle hinzufügen", "Katalog aktualisieren", "Quellen verwalten"} {
		if strings.Contains(html, removed) {
			t.Fatalf("legacy source control must not be visible: %q", removed)
		}
	}
	styles, err := files.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("read application styles: %v", err)
	}
	css := string(styles)
	for _, required := range []string{
		"--space-1:", "--surface:", "--accent:", "--danger:",
		"button:focus-visible", ".skills-discovery", ".skill-workbench",
	} {
		if !strings.Contains(css, required) {
			t.Fatalf("design system contract is missing %q", required)
		}
	}
}
