package domain

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestUsage_Validate(t *testing.T) {
	cases := []struct {
		name string
		u    Usage
		ok   bool
	}{
		{"zero", Usage{}, true},
		{"typical", Usage{InputTokens: 10, OutputTokens: 20, CacheReadTokens: 300, CacheCreationTokens: 4, CostUSD: 0.42, Turns: 3}, true},
		{"negative input", Usage{InputTokens: -1}, false},
		{"negative output", Usage{OutputTokens: -1}, false},
		{"negative cache read", Usage{CacheReadTokens: -1}, false},
		{"negative cache creation", Usage{CacheCreationTokens: -1}, false},
		{"negative turns", Usage{Turns: -1}, false},
		{"negative cost", Usage{CostUSD: -0.01}, false},
		{"NaN cost", Usage{CostUSD: math.NaN()}, false},
		{"infinite cost", Usage{CostUSD: math.Inf(1)}, false},
	}
	for _, tc := range cases {
		if err := tc.u.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

// The account's rate limits are shown, never recorded: they are not the call's cost.
func TestUsage_JSONOmitsLimits(t *testing.T) {
	u := Usage{InputTokens: 1, OutputTokens: 2, CostUSD: 0.5,
		Limits: &RateLimits{FiveHour: &LimitWindow{Utilization: 0.4, ResetsAt: time.Now()}}}
	data, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != `{"input_tokens":1,"output_tokens":2,"cost_usd":0.5}` {
		t.Errorf("json = %s", got)
	}
	var back Usage
	if err := json.Unmarshal(data, &back); err != nil || back.Limits != nil || back.CostUSD != 0.5 {
		t.Errorf("round trip = %+v, %v", back, err)
	}
}

func TestDecisionUsage_Empty(t *testing.T) {
	if !(DecisionUsage{}).Empty() {
		t.Error("zero DecisionUsage must be empty")
	}
	if (DecisionUsage{ContextDepth: &Usage{}}).Empty() {
		t.Error("a recorded round is not empty")
	}
	data, _ := json.Marshal(DecisionTrace{})
	if strings.Contains(string(data), `"usage"`) {
		t.Errorf("a trace without usage must not write the key: %s", data)
	}
}
