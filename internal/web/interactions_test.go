package web

import (
	"strings"
	"taskboard/internal/domain"
	"testing"
)

func TestNormalizeInteractionResponseMapsLegacyButtonToSchemaField(t *testing.T) {
	schema := []byte(`{"fields":[{"id":"database","type":"buttons","options":[{"value":"postgres","label":"Postgres"}]}]}`)
	answer := normalizeInteractionResponse(schema, "task-123", map[string][]string{"task-123": {"postgres"}})
	if got := answer["database"]; len(got) != 1 || got[0] != "postgres" {
		t.Fatalf("button answer = %#v, want schema field", answer)
	}
	if _, old := answer["task-123"]; old {
		t.Fatalf("legacy key was retained: %#v", answer)
	}
}

func TestNormalizeInteractionResponseDoesNotGuessMultipleButtonFields(t *testing.T) {
	schema := []byte(`{"fields":[{"id":"database","type":"buttons"},{"id":"region","type":"buttons"}]}`)
	answer := normalizeInteractionResponse(schema, "task-123", map[string][]string{"task-123": {"postgres"}})
	if got := answer["task-123"]; len(got) != 1 {
		t.Fatalf("ambiguous response was changed: %#v", answer)
	}
}

func TestDisplayCommentsCollapsesRepeatedAgentDecisionNotices(t *testing.T) {
	comments := []domain.Comment{
		{Author: "Agent", Body: "Agent benötigt eine Entscheidung: Datenbank wählen"},
		{Author: "Ralph", Body: "Postgres"},
		{Author: "Agent", Body: "Agent benötigt eine Entscheidung: Datenbank wählen"},
		{Author: "Agent", Body: "Agent benötigt eine Entscheidung: Runtime wählen"},
	}
	visible := displayComments(comments)
	if len(visible) != 3 {
		t.Fatalf("visible comments = %#v, want exactly one duplicate notice removed", visible)
	}
	if visible[0].Author != "Agent" || visible[1].Author != "Ralph" || visible[2].Body != "Agent benötigt eine Entscheidung: Runtime wählen" {
		t.Fatalf("unexpected retained order: %#v", visible)
	}
}

func TestDisplayCommentsKeepsOnlyLatestRecordedDecisionAnswer(t *testing.T) {
	comments := []domain.Comment{
		{Author: "Ralph", Body: "Antwort auf Agentenfrage „Datenbank wählen“: {\"database\":[\"sqlite\"]}"},
		{Author: "Ralph", Body: "Antwort auf Agentenfrage „Datenbank wählen“: {\"database\":[\"postgres\"]}"},
	}
	visible := displayComments(comments)
	if len(visible) != 1 || !strings.Contains(visible[0].Body, "postgres") {
		t.Fatalf("visible answers = %#v, want only latest canonical answer", visible)
	}
}
