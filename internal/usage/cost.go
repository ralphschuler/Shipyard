package usage

import "math"

// Tokens use pointers so an adapter can distinguish an unavailable value from
// a measured zero. Costs are integer micro-USD throughout the calculation.
type Report struct {
	Provider, Model, ServiceTier string
	APICalls                     *int
	InputTokens, OutputTokens    *int64
	CachedInputTokens            *int64
	CacheWriteTokens             *int64
	ReasoningTokens              *int64
	TotalTokens                  *int64
	Status                       string // complete, incomplete, or unknown
}

type Price struct {
	Version                                           string
	Input, Output, CachedInput, CacheWrite, Reasoning *int64
}

type Result struct {
	Microusd int64
	Known    bool
}

func Calculate(r Report, p Price) Result {
	var total int64
	known := false
	add := func(tokens *int64, rate *int64) {
		if tokens == nil || rate == nil {
			return
		}
		known = true
		total += divRound((*tokens)*(*rate), 1_000_000)
	}
	add(r.InputTokens, p.Input)
	add(r.OutputTokens, p.Output)
	add(r.CachedInputTokens, p.CachedInput)
	add(r.CacheWriteTokens, p.CacheWrite)
	add(r.ReasoningTokens, p.Reasoning)
	return Result{Microusd: total, Known: known}
}

func divRound(value, divisor int64) int64 {
	if value >= 0 {
		return int64(math.Floor(float64(value)/float64(divisor) + 0.5))
	}
	return int64(math.Ceil(float64(value)/float64(divisor) - 0.5))
}
