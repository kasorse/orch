package domain

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func boolPtr(b bool) *bool { return &b }

func TestComputeOutcome(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	exec := func(action ActionType, start, finish time.Duration, mut func(*StepExecution)) StepExecution {
		e := StepExecution{Action: action, Status: StepCompleted, StartedAt: t0.Add(start), FinishedAt: t0.Add(finish)}
		if mut != nil {
			mut(&e)
		}
		return e
	}
	usage := func(in int64, cost float64) *Usage {
		u := &Usage{InputTokens: in, OutputTokens: 2 * in, CostUSD: cost, Turns: 1}
		u.Limits = &RateLimits{FiveHour: &LimitWindow{Utilization: 0.5}}
		return u
	}
	withUsage := func(in int64, cost float64) func(*StepExecution) {
		return func(e *StepExecution) { e.Usage = usage(in, cost) }
	}
	green := func(e *StepExecution) { e.TestResult = &TestResult{ExitCode: 0, Success: true} }
	red := func(e *StepExecution) { e.TestResult = &TestResult{ExitCode: 1} }
	review := func(issues ...ReviewIssue) func(*StepExecution) {
		return func(e *StepExecution) { e.ReviewResult = &ReviewResult{Status: ReviewPass, Issues: issues} }
	}
	both := func(fs ...func(*StepExecution)) func(*StepExecution) {
		return func(e *StepExecution) {
			for _, f := range fs {
				f(e)
			}
		}
	}

	// The user's thinking time is in the trace but is not the decision's cost.
	trace := &DecisionTrace{Source: DecisionSourceLLM}
	trace.Latency = DecisionLatency{TranslationMS: 100, PreCheckMS: 200, ContextMS: 30, ClassificationMS: 400, ManualChoiceMS: 99999}
	trace.Usage = &DecisionUsage{Translation: usage(1, 0.01), ContextDepth: usage(2, 0.02), Classification: usage(3, 0.03)}

	cases := []struct {
		name  string
		run   Run
		trace *DecisionTrace
		want  Outcome
	}{
		{
			"completed with a green final test",
			Run{Status: RunCompleted, Executions: []StepExecution{
				exec(ActionImplement, 0, 2*time.Second, withUsage(10, 0.3)),
				exec(ActionTest, 2*time.Second, 3*time.Second, green),
				exec(ActionReview, 3*time.Second, 5*time.Second, both(review(), withUsage(20, 0.4))),
			}},
			trace,
			Outcome{Status: RunCompleted, TestsPassedFinal: boolPtr(true), ReviewVerdict: ReviewPass, DurationMS: 5000, Usage: OutcomeUsage{Usage: Usage{InputTokens: 36, OutputTokens: 72, CostUSD: 0.76, Turns: 5}, Calls: 5}, DecisionOverhead: DecisionOverhead{CostUSD: 0.06, MS: 730, Calls: 3}},
		},
		{
			"failed in test after two fix iterations",
			Run{Status: RunFailed, FailureReason: "tests still failing", FixIterations: 2, Executions: []StepExecution{
				exec(ActionImplement, 0, time.Second, nil),
				exec(ActionTest, time.Second, 2*time.Second, red),
				exec(ActionImplement, 2*time.Second, 3*time.Second, nil),
				exec(ActionTest, 3*time.Second, 4*time.Second, green),
				exec(ActionImplement, 4*time.Second, 5*time.Second, nil),
				exec(ActionTest, 5*time.Second, 6*time.Second, red),
			}},
			nil,
			Outcome{Status: RunFailed, FailureReason: "tests still failing", FixIterations: 2, TestsPassedFinal: boolPtr(false), DurationMS: 6000},
		},
		{
			"review fail without implement completes with the verdict",
			Run{Status: RunCompleted, Executions: []StepExecution{
				exec(ActionReview, 0, time.Second, review(ReviewIssue{Severity: SeverityHigh, File: "a.go", Description: "x"})),
			}},
			nil,
			Outcome{Status: RunCompleted, ReviewVerdict: ReviewFail, DurationMS: 1000},
		},
		{
			"the verdict is the computed one, not the model's own",
			Run{Status: RunCompleted, Executions: []StepExecution{
				exec(ActionReview, 0, time.Second, review(ReviewIssue{Severity: SeverityCritical})),
			}},
			nil,
			Outcome{Status: RunCompleted, ReviewVerdict: ReviewFail, DurationMS: 1000},
		},
		{
			"a last review without a result has no verdict",
			Run{Status: RunFailed, Executions: []StepExecution{
				exec(ActionReview, 0, time.Second, review(ReviewIssue{Severity: SeverityHigh, File: "a.go", Description: "x"})),
				exec(ActionImplement, time.Second, 2*time.Second, nil),
				exec(ActionReview, 2*time.Second, 3*time.Second, func(e *StepExecution) { e.Status = StepFailed }),
			}},
			nil,
			Outcome{Status: RunFailed, DurationMS: 3000},
		},
		{
			"stopped in plan mode",
			Run{Status: RunPending, Mode: ModePlan},
			nil,
			Outcome{Status: RunPending},
		},
		{
			"stopped by a limit",
			Run{Status: RunStopped, FailureReason: "maximum steps exceeded", Executions: []StepExecution{
				exec(ActionImplement, 0, time.Second, nil),
			}},
			nil,
			Outcome{Status: RunStopped, FailureReason: "maximum steps exceeded", DurationMS: 1000},
		},
		{
			"no test step",
			Run{Status: RunCompleted, Executions: []StepExecution{
				exec(ActionAnalyze, 0, time.Second, nil),
			}},
			nil,
			Outcome{Status: RunCompleted, DurationMS: 1000},
		},
		{
			"a test execution that failed without a result",
			Run{Status: RunFailed, Executions: []StepExecution{
				exec(ActionTest, 0, time.Second, green),
				exec(ActionTest, time.Second, 2*time.Second, func(e *StepExecution) { e.Status = StepFailed }),
			}},
			nil,
			Outcome{Status: RunFailed, DurationMS: 2000},
		},
		{
			"resumed run sums the usage of both attempts",
			Run{Status: RunCompleted, Resumes: []Resume{{FromStatus: RunFailed}}, Executions: []StepExecution{
				exec(ActionImplement, 0, time.Second, both(func(e *StepExecution) { e.Status = StepFailed }, withUsage(10, 0.07))),
				exec(ActionImplement, time.Hour, time.Hour+time.Second, both(func(e *StepExecution) { e.Attempt = 1 }, withUsage(10, 0.33))),
			}},
			nil,
			Outcome{Status: RunCompleted, DurationMS: (time.Hour + time.Second).Milliseconds(), Usage: OutcomeUsage{Usage: Usage{InputTokens: 20, OutputTokens: 40, CostUSD: 0.4, Turns: 2}, Calls: 2}},
		},
		{
			"a manual choice after a failed provider counts the provider's time, not the user's",
			Run{Status: RunCompleted},
			&DecisionTrace{Source: DecisionSourceUser, DecisionError: "provider unavailable", Latency: DecisionLatency{ManualChoiceMS: 5000, PreCheckMS: 40}},
			Outcome{Status: RunCompleted, DecisionOverhead: DecisionOverhead{MS: 40}},
		},
		{
			"a --stage trace has no overhead",
			Run{Status: RunCompleted},
			&DecisionTrace{Source: DecisionSourceManual, Latency: DecisionLatency{ClassificationMS: 300}},
			Outcome{Status: RunCompleted},
		},
		{
			"a trace that recorded no usage still has its time",
			Run{Status: RunCompleted},
			&DecisionTrace{Source: DecisionSourceJev, Latency: DecisionLatency{ClassificationMS: 300}},
			Outcome{Status: RunCompleted, DecisionOverhead: DecisionOverhead{MS: 300}},
		},
		{
			"a round that did not run is not a call",
			Run{Status: RunCompleted},
			&DecisionTrace{Source: DecisionSourceLLM, Latency: DecisionLatency{ClassificationMS: 300}, Usage: &DecisionUsage{Classification: usage(3, 0.03)}},
			Outcome{Status: RunCompleted, Usage: OutcomeUsage{Usage: Usage{InputTokens: 3, OutputTokens: 6, CostUSD: 0.03, Turns: 1}, Calls: 1}, DecisionOverhead: DecisionOverhead{CostUSD: 0.03, MS: 300, Calls: 1}},
		},
		{
			"no executions",
			Run{Status: RunFailed, FailureReason: "boom"},
			nil,
			Outcome{Status: RunFailed, FailureReason: "boom"},
		},
		{
			"a clock that went backwards gives no duration",
			Run{Status: RunCompleted, Executions: []StepExecution{
				exec(ActionAnalyze, time.Minute, 0, nil),
			}},
			nil,
			Outcome{Status: RunCompleted},
		},
		{
			"an execution without a finish time falls back to the last finished one",
			Run{Status: RunFailed, Executions: []StepExecution{
				exec(ActionAnalyze, 0, 2*time.Second, nil),
				{Action: ActionImplement, Status: StepFailed, StartedAt: t0.Add(2 * time.Second)},
			}},
			nil,
			Outcome{Status: RunFailed, DurationMS: 2000},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeOutcome(tc.run, tc.trace)
			if got.Usage.Limits != nil {
				t.Error("the rate-limit state must not be part of the outcome")
			}
			if (got.TestsPassedFinal == nil) != (tc.want.TestsPassedFinal == nil) ||
				got.TestsPassedFinal != nil && *got.TestsPassedFinal != *tc.want.TestsPassedFinal {
				t.Errorf("tests_passed_final = %v, want %v", got.TestsPassedFinal, tc.want.TestsPassedFinal)
			}
			if got.Status != tc.want.Status || got.FailureReason != tc.want.FailureReason ||
				got.FixIterations != tc.want.FixIterations || got.ReviewVerdict != tc.want.ReviewVerdict ||
				got.DurationMS != tc.want.DurationMS {
				t.Errorf("outcome = %+v, want %+v", got, tc.want)
			}
			if !usageNear(got.Usage, tc.want.Usage) {
				t.Errorf("usage = %+v, want %+v", got.Usage, tc.want.Usage)
			}
			g, w := got.DecisionOverhead, tc.want.DecisionOverhead
			if g.Calls != w.Calls || g.MS != w.MS || math.Abs(g.CostUSD-w.CostUSD) > 1e-9 {
				t.Errorf("decision_overhead = %+v, want %+v", g, w)
			}
		})
	}
}

func usageNear(a, b OutcomeUsage) bool {
	return a.Calls == b.Calls && a.InputTokens == b.InputTokens && a.OutputTokens == b.OutputTokens &&
		a.CacheReadTokens == b.CacheReadTokens && a.CacheCreationTokens == b.CacheCreationTokens &&
		a.Turns == b.Turns && math.Abs(a.CostUSD-b.CostUSD) < 1e-9
}

// The outcome is derived: computing it neither changes the run nor the trace.
func TestComputeOutcome_DoesNotMutate(t *testing.T) {
	u := &Usage{InputTokens: 5, CostUSD: 0.1}
	run := Run{Status: RunCompleted, Executions: []StepExecution{{Action: ActionAnalyze, Usage: u}}}
	tr := &DecisionTrace{Usage: &DecisionUsage{Classification: &Usage{InputTokens: 7}}}
	ComputeOutcome(run, tr)
	ComputeOutcome(run, tr)
	if u.InputTokens != 5 || u.CostUSD != 0.1 || tr.Usage.Classification.InputTokens != 7 {
		t.Errorf("inputs changed: %+v %+v", u, tr.Usage.Classification)
	}
}

func TestOutcome_JSON(t *testing.T) {
	var o Outcome
	o.Status, o.TestsPassedFinal, o.ReviewVerdict, o.DurationMS = RunCompleted, boolPtr(true), ReviewPass, 12
	o.Usage = OutcomeUsage{Usage: Usage{InputTokens: 1, OutputTokens: 2, CostUSD: 0.5}, Calls: 3}
	o.DecisionOverhead = DecisionOverhead{CostUSD: 0.1, MS: 4, Calls: 1}
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"status":"completed","fix_iterations":0,"tests_passed_final":true,"review_verdict":"pass","duration_ms":12,` +
		`"usage":{"input_tokens":1,"output_tokens":2,"cost_usd":0.5,"calls":3},` +
		`"decision_overhead":{"cost_usd":0.1,"ms":4,"calls":1}}`
	if string(data) != want {
		t.Errorf("json = %s\nwant   %s", data, want)
	}
	var back Outcome
	if err := json.Unmarshal(data, &back); err != nil || back.Usage.Calls != 3 || back.Usage.CostUSD != 0.5 {
		t.Errorf("round trip = %+v, %v", back, err)
	}
}

func TestOutcomeUsage_Validate(t *testing.T) {
	if err := (OutcomeUsage{Calls: -1}).Validate(); err == nil {
		t.Error("negative calls must be rejected")
	}
	if err := (OutcomeUsage{Usage: Usage{InputTokens: -1}}).Validate(); err == nil {
		t.Error("a negative counter must be rejected")
	}
	if err := (OutcomeUsage{Usage: Usage{InputTokens: 1}, Calls: 1}).Validate(); err != nil {
		t.Errorf("Validate() = %v", err)
	}
}
