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

func TestCalculateRoundsEachTokenClassInMicroUSD(t *testing.T) {
	r := Report{
		InputTokens: ptr(500_000), OutputTokens: ptr(1_500_000),
		CachedInputTokens: ptr(500_000), CacheWriteTokens: ptr(1_500_000),
		ReasoningTokens: ptr(500_000),
	}
	p := Price{
		Input: ptr(1), Output: ptr(1), CachedInput: ptr(1),
		CacheWrite: ptr(1), Reasoning: ptr(1),
	}
	if got := Calculate(r, p); !got.Known || got.Microusd != 7 {
		t.Fatalf("got %+v, want 7 micro-USD after per-class rounding", got)
	}
}

func TestCalculatePreservesKnownZeroCost(t *testing.T) {
	zero := int64(0)
	got := Calculate(Report{InputTokens: ptr(1)}, Price{Input: &zero})
	if !got.Known || got.Microusd != 0 {
		t.Fatalf("got %+v, want known zero cost", got)
	}
}
