package persistence

import (
	"math"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func TestValidateRun_Outcome(t *testing.T) {
	run := func(o *domain.Outcome) *domain.Run {
		return &domain.Run{ID: "r", Status: domain.RunCompleted, Outcome: o}
	}
	var good domain.Outcome
	good.Status, good.ReviewVerdict, good.DurationMS = domain.RunCompleted, domain.ReviewPass, 5
	good.Usage = domain.OutcomeUsage{Usage: domain.Usage{InputTokens: 1, CostUSD: 0.1}, Calls: 1}
	good.DecisionOverhead = domain.DecisionOverhead{CostUSD: 0.1, MS: 2, Calls: 1}
	if err := validateRun(run(&good), "r"); err != nil {
		t.Fatalf("a valid outcome: %v", err)
	}
	if err := validateRun(run(nil), "r"); err != nil {
		t.Fatalf("a run without an outcome (an old or unfinished run): %v", err)
	}

	// A resume drops the previous attempt's outcome before it saves the run as running, so
	// an outcome of another status is a corrupt run.json.
	stale := good
	stale.Status = domain.RunFailed
	if err := validateRun(&domain.Run{ID: "r", Status: domain.RunRunning, Outcome: &stale}, "r"); err == nil || !strings.Contains(err.Error(), "outcome") {
		t.Errorf("a previous attempt's outcome on a running run: validateRun() = %v, want an outcome error", err)
	}

	cases := []struct {
		name   string
		mutate func(o *domain.Outcome)
	}{
		{"status is not terminal", func(o *domain.Outcome) { o.Status = domain.RunRunning }},
		{"status is not the run's", func(o *domain.Outcome) { o.Status = domain.RunFailed }},
		{"negative fix iterations", func(o *domain.Outcome) { o.FixIterations = -1 }},
		{"negative duration", func(o *domain.Outcome) { o.DurationMS = -1 }},
		{"unknown review verdict", func(o *domain.Outcome) { o.ReviewVerdict = "maybe" }},
		{"negative calls", func(o *domain.Outcome) { o.Usage.Calls = -1 }},
		{"negative usage counter", func(o *domain.Outcome) { o.Usage.OutputTokens = -1 }},
		{"NaN usage cost", func(o *domain.Outcome) { o.Usage.CostUSD = math.NaN() }},
		{"negative overhead ms", func(o *domain.Outcome) { o.DecisionOverhead.MS = -1 }},
		{"negative overhead calls", func(o *domain.Outcome) { o.DecisionOverhead.Calls = -1 }},
		{"negative overhead cost", func(o *domain.Outcome) { o.DecisionOverhead.CostUSD = -0.1 }},
		{"infinite overhead cost", func(o *domain.Outcome) { o.DecisionOverhead.CostUSD = math.Inf(1) }},
	}
	for _, tc := range cases {
		o := good
		tc.mutate(&o)
		if err := validateRun(run(&o), "r"); err == nil || !strings.Contains(err.Error(), "outcome") {
			t.Errorf("%s: validateRun() = %v, want an outcome error", tc.name, err)
		}
	}
}
