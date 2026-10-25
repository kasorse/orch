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
		{"negative active time", func(o *domain.Outcome) { o.ActiveMS = -1 }},
		{"more partial calls than calls", func(o *domain.Outcome) { o.Usage.PartialCalls = 2 }},
		{"negative diff counter", func(o *domain.Outcome) { o.Diff = &domain.DiffStat{Files: 1, Insertions: -1} }},
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

func TestValidateRun_StepCallAndDiff(t *testing.T) {
	exec := func(action domain.ActionType, call *domain.CallReport, diff *domain.StepDiff) *domain.Run {
		return &domain.Run{ID: "r", Status: domain.RunFailed, Executions: []domain.StepExecution{
			{Index: 0, Action: action, Status: domain.StepCompleted, Call: call, Diff: diff},
		}}
	}
	goodCall := &domain.CallReport{
		PromptBytes: 100, Truncated: []domain.PromptPart{domain.PromptTestOutput}, APIMS: 5,
		Models: []domain.ModelUse{{Model: "claude-sonnet-5-5", CostUSD: 0.1}},
		Tools:  map[string]int{"Read": 3, "mcp__graphify__query_graph": 1},
	}
	goodDiff := &domain.StepDiff{
		Step: domain.DiffStat{Files: 2, Insertions: 3, Deletions: 1, Binary: 1}, Run: &domain.DiffStat{Files: 2},
		NotReported: []string{"a/b.go"}, ReportedUnchanged: []string{"c.go"},
	}
	if err := validateRun(exec(domain.ActionImplement, goodCall, goodDiff), "r"); err != nil {
		t.Fatalf("a valid call report and diff: %v", err)
	}

	paths := make([]string, domain.MaxDiffPaths+1)
	for i := range paths {
		paths[i] = "f.go"
	}
	cases := []struct {
		name string
		run  *domain.Run
	}{
		{"negative prompt bytes", exec(domain.ActionInvestigate, &domain.CallReport{PromptBytes: -1}, nil)},
		{"unknown prompt part", exec(domain.ActionInvestigate, &domain.CallReport{Truncated: []domain.PromptPart{"task"}}, nil)},
		{"empty model name", exec(domain.ActionInvestigate, &domain.CallReport{Models: []domain.ModelUse{{}}}, nil)},
		{"control character in a tool name", exec(domain.ActionInvestigate, &domain.CallReport{Tools: map[string]int{"Read\x1b[2J": 1}}, nil)},
		{"negative tool count", exec(domain.ActionInvestigate, &domain.CallReport{Tools: map[string]int{"Read": -1}}, nil)},
		{"NaN model cost", exec(domain.ActionInvestigate, &domain.CallReport{Models: []domain.ModelUse{{Model: "m", CostUSD: math.NaN()}}}, nil)},
		{"diff on a review step", exec(domain.ActionReview, nil, &domain.StepDiff{})},
		{"more binary files than files", exec(domain.ActionImplement, nil, &domain.StepDiff{Step: domain.DiffStat{Files: 1, Binary: 2}})},
		{"path outside the repository", exec(domain.ActionImplement, nil, &domain.StepDiff{NotReported: []string{"../etc/passwd"}})},
		{"absolute path", exec(domain.ActionImplement, nil, &domain.StepDiff{ReportedUnchanged: []string{"/etc/passwd"}})},
		{"too many paths", exec(domain.ActionImplement, nil, &domain.StepDiff{NotReported: paths})},
	}
	for _, tc := range cases {
		if err := validateRun(tc.run, "r"); err == nil || !strings.Contains(err.Error(), "executions[0]") {
			t.Errorf("%s: validateRun() = %v, want an executions[0] error", tc.name, err)
		}
	}
}
