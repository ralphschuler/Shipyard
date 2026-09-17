package memory

import (
	"encoding/json"
	"strings"
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

func TestRedactObjectFailsClosedForSensitiveKeys(t *testing.T) {
	got := string(RedactObject(json.RawMessage(`{"token":"secret","nested":{"password":123},"safe":"hello"}`)))
	if strings.Contains(got, "secret") || strings.Contains(got, "123") || !strings.Contains(got, `"token":"[REDACTED]"`) || !strings.Contains(got, `"safe":"hello"`) {
		t.Fatalf("sensitive JSON fields were not redacted: %s", got)
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

func TestDedupeKeyKeepsFactIdentityAcrossObjectChanges(t *testing.T) {
	if DedupeKey("name", "likes", json.RawMessage(`"tea"`)) != DedupeKey("name", "likes", json.RawMessage(`"coffee"`)) {
		t.Fatal("object changes must create a new version of the same fact")
	}
}

func TestFitNeverExceedsTokenBudget(t *testing.T) {
	items := []RetrievalItem{{Kind: "conversation", Text: "one two"}, {Kind: "fact", Text: "three four"}, {Kind: "fact", Text: "five"}}
	p := Fit(items, 4)
	if p.UsedTokens > 4 || len(p.Items) != 2 || !p.Truncated {
		t.Fatalf("bad pack: %+v", p)
	}
}

func TestFactVersionVisibleAtHonorsValidityWindow(t *testing.T) {
	point := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	from := point.Add(-time.Hour)
	until := point.Add(time.Hour)
	if !FactVersionVisibleAt(from, &until, point) {
		t.Fatal("version inside validity window must be visible")
	}
	if FactVersionVisibleAt(point.Add(time.Nanosecond), &until, point) || FactVersionVisibleAt(from, &point, point) {
		t.Fatal("validity window must be half-open")
	}
}
