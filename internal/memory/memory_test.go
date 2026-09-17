package memory

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRedactSecretsBeforePersistenceAndOutput(t *testing.T) {
	input := "Authorization: Bearer abc123 password=hunter2 sk_live_abcdefghijklmnop"
	got := Redact(input)
	if got == input || got != "Authorization: [REDACTED] [REDACTED] [REDACTED]" {
		t.Fatalf("unexpected redaction: %q", got)
	}
}

func TestScopeAndProvenanceAreCompleteAndBound(t *testing.T) {
	s := Scope{"tenant", "user", "project", "task", "agent"}
	if !s.Valid() {
		t.Fatal("complete scope rejected")
	}
	p := Provenance{MessageID: "message", TaskID: "task", RunID: "run", AgentID: "agent", OccurredAt: time.Now()}
	if !p.Valid(s) {
		t.Fatal("matching provenance rejected")
	}
	p.TaskID = "other"
	if p.Valid(s) {
		t.Fatal("cross-task provenance accepted")
	}
}

func TestDedupeKeyCanonicalizesObject(t *testing.T) {
	a, _ := NormalizeObject(json.RawMessage(`{"b":2,"a":1}`))
	b, _ := NormalizeObject(json.RawMessage(`{"a":1,"b":2}`))
	if DedupeKey(" Name ", "PREDICATE", a) != DedupeKey("name", "predicate", b) {
		t.Fatal("equivalent JSON must deduplicate")
	}
}

func TestFitNeverExceedsTokenBudget(t *testing.T) {
	items := []RetrievalItem{{Kind: "conversation", Text: "one two"}, {Kind: "fact", Text: "three four"}, {Kind: "fact", Text: "five"}}
	p := Fit(items, 4)
	if p.UsedTokens > 4 || len(p.Items) != 2 || !p.Truncated {
		t.Fatalf("bad pack: %+v", p)
	}
}
