package store

import (
	"strings"
	"taskboard/internal/domain"
	"testing"
)

func TestAutomationEventFingerprintIgnoresTransportAndObjectOrder(t *testing.T) {
	rule := domain.AutomationRule{ID: "rule-1", TargetColumnID: "column-qa"}
	one := domain.AutomationEvent{Type: "task.entered_column", TaskID: "task-1", Payload: []byte(`{"target_column_id":"column-qa","event_id":"old","items":[{"id":2},{"id":1}]}`)}
	two := domain.AutomationEvent{Type: one.Type, TaskID: one.TaskID, Payload: []byte(`{"items":[{"id":1},{"id":2}],"received_at":"later","target_column_id":"column-qa","delivery_id":"new"}`)}
	first, err := AutomationEventFingerprint(one, rule)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AutomationEventFingerprint(two, rule)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("semantic duplicate fingerprints differ: %s != %s", first, second)
	}
	if len(first) != 64 || strings.Trim(first, "0123456789abcdef") != "" {
		t.Fatalf("unexpected SHA-256 fingerprint: %q", first)
	}
}

func TestAutomationEventFingerprintIncludesRuleAndReturnGeneration(t *testing.T) {
	event := domain.AutomationEvent{Type: "task.entered_column", TaskID: "task-1", Payload: []byte(`{"qa_return":true,"return_generation":1}`)}
	first, err := AutomationEventFingerprint(event, domain.AutomationRule{ID: "rule-1", TargetColumnID: "column-a"})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []domain.AutomationRule{{ID: "rule-2", TargetColumnID: "column-a"}, {ID: "rule-1", TargetColumnID: "column-b"}} {
		other, err := AutomationEventFingerprint(event, rule)
		if err != nil {
			t.Fatal(err)
		}
		if first == other {
			t.Fatal("different rule/target must not share a fingerprint")
		}
	}
	changed := event
	changed.Payload = []byte(`{"qa_return":true,"return_generation":2}`)
	other, err := AutomationEventFingerprint(changed, domain.AutomationRule{ID: "rule-1", TargetColumnID: "column-a"})
	if err != nil {
		t.Fatal(err)
	}
	if first == other {
		t.Fatal("different return generations must not share a fingerprint")
	}
}

func TestAutomationEventFingerprintRejectsInvalidPayload(t *testing.T) {
	_, err := AutomationEventFingerprint(domain.AutomationEvent{Payload: []byte("not-json")}, domain.AutomationRule{})
	if err == nil {
		t.Fatal("invalid payload must fail fingerprinting")
	}
}
