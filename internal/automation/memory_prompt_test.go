package automation

import (
	"strings"
	"taskboard/internal/memory"
	"testing"
	"time"
)

func TestFormatMemoryContextSeparatesKindsAndIncludesProvenance(t *testing.T) {
	pack := memory.ContextPack{
		ID: "retrieval-1", TokenBudget: 20, UsedTokens: 8,
		Items: []memory.RetrievalItem{
			{Kind: "conversation", ID: "c-1", Text: "The API is version two.", Source: memory.Provenance{MessageID: "m-1", RunID: "r-1", TaskID: "t-1", AgentID: "a-1", OccurredAt: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}},
			{Kind: "fact", ID: "f-1", Text: "service owner team-a", Confidence: .9, Source: memory.Provenance{MessageID: "m-2", RunID: "r-2", TaskID: "t-1", AgentID: "a-1", OccurredAt: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)}},
		},
	}
	got := formatMemoryContext(pack)
	for _, want := range []string{"MEMORY CONTEXT", "CONVERSATIONS", "CONFIRMED FACTS", "c-1", "f-1", "m-1", "r-2", "8/20"} {
		if !strings.Contains(got, want) {
			t.Fatalf("memory context missing %q: %s", want, got)
		}
	}
	if strings.Index(got, "CONVERSATIONS") > strings.Index(got, "CONFIRMED FACTS") {
		t.Fatal("memory sections are not deterministic")
	}
}

func TestFormatMemoryContextDoesNotExposeSecrets(t *testing.T) {
	pack := memory.ContextPack{TokenBudget: 5, UsedTokens: 2, Items: []memory.RetrievalItem{{Kind: "fact", ID: "f-1", Text: "token=super-secret"}}}
	got := formatMemoryContext(pack)
	if strings.Contains(got, "super-secret") || !strings.Contains(got, memory.Redacted) {
		t.Fatalf("memory secret was exposed: %s", got)
	}
}
