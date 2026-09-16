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
