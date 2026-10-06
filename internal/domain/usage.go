package domain

import (
	"fmt"
	"math"
	"time"
)

// Usage is what one provider call cost, as far as the provider CLI reports it. It is
// displayed and recorded (run.json, decision.json - specs/benchmark/SPEC.md §11.1);
// nothing decides anything from these numbers. A provider that reports nothing leaves the
// call's Usage nil.
type Usage struct {
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64   `json:"cache_creation_tokens,omitempty"`
	CostUSD             float64 `json:"cost_usd,omitempty"`
	Turns               int     `json:"turns,omitempty"`
	// Limits is the account's rate-limit state after the call, when reported. It is the
	// account's state, not the call's cost, so it is shown but never recorded.
	Limits *RateLimits `json:"-"`
}

// Validate checks a recorded usage read back from disk: no counter is negative and the
// cost is a finite number.
func (u Usage) Validate() error {
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.CacheReadTokens < 0 || u.CacheCreationTokens < 0 || u.Turns < 0 {
		return fmt.Errorf("negative counter")
	}
	if u.CostUSD < 0 || math.IsNaN(u.CostUSD) || math.IsInf(u.CostUSD, 0) {
		return fmt.Errorf("cost_usd is %v", u.CostUSD)
	}
	return nil
}

// PromptTokens is every input token, cached or not.
func (u Usage) PromptTokens() int64 {
	return u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
}

// Add accumulates o into u; the latest non-nil Limits wins.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheCreationTokens += o.CacheCreationTokens
	u.CostUSD += o.CostUSD
	u.Turns += o.Turns
	if o.Limits != nil {
		u.Limits = o.Limits
	}
}

// RateLimits are the subscription windows: utilization 0..1 and when each resets.
type RateLimits struct {
	FiveHour *LimitWindow
	Weekly   *LimitWindow
}

type LimitWindow struct {
	Utilization float64
	ResetsAt    time.Time
}
