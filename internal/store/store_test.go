package store

import (
	"reflect"
	"strings"
	"taskboard/internal/domain"
	"testing"
)

func TestAutomationRuleSelectCoversDomainShape(t *testing.T) {
	if got, want := len(automationRuleColumns), reflect.TypeOf(domain.AutomationRule{}).NumField(); got != want {
		t.Fatalf("automation rule select has %d columns, but domain has %d fields", got, want)
	}
}

func TestDatesRejectsEndBeforeStart(t *testing.T) {
	_, _, err := dates("2026-10-12", "2026-10-11")
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestPriorityValidation(t *testing.T) {
	if !validPriority("urgent") || validPriority("now") {
		t.Fatal("unexpected priority validation")
	}
}

func TestBoardTemplateCatalogueIsComplete(t *testing.T) {
	templates := BoardTemplates()
	if len(templates) != 10 {
		t.Fatalf("want 10 board templates, got %d", len(templates))
	}
	seen := map[string]bool{}
	for _, template := range templates {
		if template.ID == "" || template.Name == "" || template.Detail == "" || len(template.Columns) < 3 {
			t.Fatalf("incomplete template: %#v", template)
		}
		if seen[template.ID] {
			t.Fatalf("duplicate template ID: %s", template.ID)
		}
		seen[template.ID] = true
		if _, ok := workflowTemplates[template.ID]; !ok {
			t.Fatalf("catalogue template %s has no workflow", template.ID)
		}
	}
}

func TestPersonalDevelopmentTemplateDefinesRecoveryPaths(t *testing.T) {
	spec, ok := workflowTemplates["personal"]
	if !ok {
		t.Fatal("personal template missing")
	}
	columns := map[string]bool{}
	for _, column := range spec.columns {
		columns[column.name] = true
	}
	for _, required := range []string{"Inbox", "Backlog", "In Progress", "Review", "QA", "Blocked", "Done"} {
		if !columns[required] {
			t.Fatalf("personal template is missing %q", required)
		}
	}
	paths := map[string]bool{}
	for _, transition := range spec.transitions {
		paths[transition.from+"->"+transition.to] = true
	}
	for _, required := range []string{"Inbox->Backlog", "Review->In Progress", "Review->QA", "QA->Done", "QA->In Progress", "Blocked->In Progress", "Blocked->Backlog", "Blocked->QA", "Blocked->Done", "Done->Backlog"} {
		if !paths[required] {
			t.Fatalf("personal template is missing recovery path %q", required)
		}
	}
	for _, column := range spec.columns {
		if column.name == "Blocked" && columnTypeForTemplate("personal", column) != "needs_action" {
			t.Fatal("Blocked must be the needs_action column")
		}
	}
	labels := map[string]bool{}
	for _, label := range spec.labels {
		if label.name == "" || label.color == "" {
			t.Fatalf("personal template has incomplete label: %#v", label)
		}
		labels[label.name] = true
	}
	for _, required := range []string{"Frontend", "Backend", "Infrastruktur", "Sonstiges"} {
		if !labels[required] {
			t.Fatalf("personal template is missing label %q", required)
		}
	}
}

func TestCanonicalUUIDRejectsRepositoryURL(t *testing.T) {
	if canonicalUUID.MatchString("https://github.com/example/shipyard.git") {
		t.Fatal("repository URL must not be accepted as a project UUID")
	}
	if !canonicalUUID.MatchString("123e4567-e89b-12d3-a456-426614174000") {
		t.Fatal("canonical UUID example must be accepted")
	}
}

func TestNormalizeTargetIDsTrimsValidUUIDs(t *testing.T) {
	got, err := normalizeTargetIDs("project", []string{" 123e4567-e89b-12d3-a456-426614174000 "})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("normalized IDs = %#v", got)
	}
}

func TestNormalizeTargetIDsRejectsRepositoryURLWithFormatDiagnostic(t *testing.T) {
	_, err := normalizeTargetIDs("project", []string{"https://github.com/example/shipyard.git"})
	if err == nil {
		t.Fatal("expected repository URL to be rejected")
	}
	for _, expected := range []string{"ungültige project-ID", "kanonische UUID", "keine Repository-URL"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("error %q does not contain %q", err, expected)
		}
	}
}

func TestBoardInheritanceEligibleColumn(t *testing.T) {
	for _, name := range []string{"Backlog", "Entwicklung", "In Progress"} {
		if !boardInheritanceEligibleColumn(name) {
			t.Errorf("%q should allow board target inheritance", name)
		}
	}
	for _, name := range []string{"Inbox", "Review", "Erledigt", "QA"} {
		if boardInheritanceEligibleColumn(name) {
			t.Errorf("%q should not trigger board target inheritance", name)
		}
	}
}
