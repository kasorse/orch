package domain

import "fmt"

// Outcome is how a run ended and what it cost, derived from the run's own record and
// recorded in run.json (specs/benchmark/SPEC.md §11.2). Nothing decides anything from it:
// it exists so a report reads one place instead of re-deriving it from the executions.
type Outcome struct {
	Status        RunStatus `json:"status"`
	FailureReason string    `json:"failure_reason,omitempty"`
	FixIterations int       `json:"fix_iterations"`
	// TestsPassedFinal is the last test execution's result; nil without a test step or
	// before one ran.
	TestsPassedFinal *bool `json:"tests_passed_final,omitempty"`
	// ReviewVerdict is the last review's authoritative verdict (ReviewResult.Decide);
	// empty without a review or when the last review gave no result.
	ReviewVerdict ReviewStatus `json:"review_verdict,omitempty"`
	// DurationMS runs from the first execution's start to the last one's finish; 0 without
	// executions.
	DurationMS int64 `json:"duration_ms"`
	// Usage is summed over the executions and the decision calls.
	Usage            OutcomeUsage     `json:"usage"`
	DecisionOverhead DecisionOverhead `json:"decision_overhead"`
}

// OutcomeUsage is the usage summed over every provider call of a run, and how many calls
// reported one.
type OutcomeUsage struct {
	Usage
	Calls int `json:"calls"`
}

// Validate checks a recorded outcome read back from disk.
func (u OutcomeUsage) Validate() error {
	if u.Calls < 0 {
		return fmt.Errorf("negative counter")
	}
	return u.Usage.Validate()
}

// DecisionOverhead is what deciding the workflow cost: the cost and call count of the
// decision calls that reported usage, and the time the provider-bound decision steps took
// (never the user's own thinking time). It is zero for a --stage run, which asked nothing.
type DecisionOverhead struct {
	CostUSD float64 `json:"cost_usd"`
	MS      int64   `json:"ms"`
	Calls   int     `json:"calls"`
}

// ComputeOutcome derives run's outcome. It is pure and total: any run gives an answer, so
// the report can compute it for old runs and for runs that died without a terminal status.
// trace is the run's decision.json, nil when there is none.
func ComputeOutcome(run Run, trace *DecisionTrace) Outcome {
	out := Outcome{
		Status:        run.Status,
		FailureReason: run.FailureReason,
		FixIterations: run.FixIterations,
	}

	for _, exec := range run.Executions {
		switch exec.Action {
		case ActionTest:
			out.TestsPassedFinal = nil
			if exec.TestResult != nil {
				passed := exec.TestResult.Success
				out.TestsPassedFinal = &passed
			}
		case ActionReview:
			out.ReviewVerdict = ""
			if exec.ReviewResult != nil {
				out.ReviewVerdict = exec.ReviewResult.Decide()
			}
		}
		if exec.Usage != nil {
			out.Usage.Usage.Add(*exec.Usage)
			out.Usage.Calls++
		}
	}
	out.DurationMS = executionsDurationMS(run.Executions)

	if trace != nil && trace.Source != DecisionSourceManual {
		if trace.Usage != nil {
			for _, u := range []*Usage{trace.Usage.Translation, trace.Usage.ContextDepth, trace.Usage.Classification} {
				if u == nil {
					continue
				}
				out.Usage.Usage.Add(*u)
				out.Usage.Calls++
				out.DecisionOverhead.CostUSD += u.CostUSD
				out.DecisionOverhead.Calls++
			}
		}
		// The time is in the trace even when no round reported usage (a trace written
		// before usage was recorded, a provider that failed before answering).
		l := trace.Latency
		out.DecisionOverhead.MS = l.TranslationMS + l.PreCheckMS + l.ContextMS + l.ClassificationMS
	}
	// The account's rate-limit state is not a cost, and is never recorded.
	out.Usage.Limits = nil
	return out
}

// executionsDurationMS is the first execution's start to the last finished execution's
// finish; 0 without executions, without a finish time, or when the clock went backwards.
func executionsDurationMS(execs []StepExecution) int64 {
	if len(execs) == 0 || execs[0].StartedAt.IsZero() {
		return 0
	}
	start := execs[0].StartedAt
	for i := len(execs) - 1; i >= 0; i-- {
		if end := execs[i].FinishedAt; !end.IsZero() {
			if end.Before(start) {
				return 0
			}
			return end.Sub(start).Milliseconds()
		}
	}
	return 0
}
