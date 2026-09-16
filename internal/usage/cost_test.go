package usage

import "testing"

func ptr(v int64) *int64 { return &v }

func TestCalculateSeparatesCacheAndReasoning(t *testing.T) {
	r := Report{InputTokens: ptr(1_000_000), CachedInputTokens: ptr(2_000_000), ReasoningTokens: ptr(500_000)}
	p := Price{Input: ptr(3), CachedInput: ptr(1), Reasoning: ptr(8)}
	got := Calculate(r, p)
	if !got.Known || got.Microusd != 9 {
		t.Fatalf("got %+v, want 9 micro-USD", got)
	}
}

func TestCalculateLeavesMissingValuesUnknown(t *testing.T) {
	got := Calculate(Report{TotalTokens: ptr(10)}, Price{Input: ptr(4)})
	if got.Known || got.Microusd != 0 {
		t.Fatalf("got %+v, want unknown zero", got)
	}
}
