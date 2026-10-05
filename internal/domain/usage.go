package domain

import "time"

// Usage is what one provider call cost, as far as the provider CLI reports it. Only
// display uses it: nothing decides anything from these numbers. A provider that reports
// nothing leaves the call's Usage nil.
type Usage struct {
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	CostUSD             float64
	Turns               int
	// Limits is the account's rate-limit state after the call, when reported.
	Limits *RateLimits
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
