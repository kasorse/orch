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
	// InputTokens is uncached input. CacheReadTokens and CacheCreationTokens are
	// separate parts of the provider's total input and are included in PromptTokens.
	InputTokens int64 `json:"input_tokens"`
	// OutputTokens is visible output. ReasoningTokens is the hidden reasoning part
	// of the provider's total output.
	OutputTokens        int64   `json:"output_tokens"`
	ReasoningTokens     int64   `json:"reasoning_tokens,omitempty"`
	CacheReadTokens     int64   `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64   `json:"cache_creation_tokens,omitempty"`
	CostUSD             float64 `json:"cost_usd,omitempty"`
	Turns               int     `json:"turns,omitempty"`
	// Partial is true when the call ended (a timeout, a crash) before the CLI's final
	// report: the counters are summed from the messages it streamed until then. The
	// input and cache counters are each message's own; OutputTokens is a lower bound,
	// since the stream reports it as a message starts; CostUSD is unknown and left 0.
	Partial bool `json:"partial,omitempty"`
	// Limits is the account's rate-limit state after the call, when reported. It is the
	// account's state, not the call's cost, so it is shown but never recorded.
	Limits *RateLimits `json:"-"`
	// APIKeySource is the credential source the claude CLI reported for the call
	// (specs/claude-auth/SPEC.md §10); empty for other providers and for a CLI that reports none.
	// Never summed by Add.
	APIKeySource string `json:"api_key_source,omitempty"`
}

// Validate checks a recorded usage read back from disk: no counter is negative and the
// cost is a finite number.
func (u Usage) Validate() error {
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.ReasoningTokens < 0 || u.CacheReadTokens < 0 || u.CacheCreationTokens < 0 || u.Turns < 0 {
		return fmt.Errorf("negative counter")
	}
	if u.CostUSD < 0 || math.IsNaN(u.CostUSD) || math.IsInf(u.CostUSD, 0) {
		return fmt.Errorf("cost_usd is %v", u.CostUSD)
	}
	if u.APIKeySource != "" {
		if err := reportName(u.APIKeySource); err != nil {
			return fmt.Errorf("api_key_source: %w", err)
		}
	}
	return nil
}

// PromptTokens is every input token, cached or not.
func (u Usage) PromptTokens() int64 {
	return u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
}

// Add accumulates o into u; the latest non-nil Limits wins. Partial is the caller's to
// keep track of: a sum is not one call.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.ReasoningTokens += o.ReasoningTokens
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
