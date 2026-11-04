package workflow

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

var testLimits = domain.LimitsView{MaxSteps: 20, MaxFixIterations: 3}

var (
	mediumResult = &domain.ReviewResult{Status: domain.ReviewPass, Issues: []domain.ReviewIssue{{Severity: domain.SeverityMedium, File: "a.go", Description: "no test for the empty input"}}}
	lowResult    = &domain.ReviewResult{Status: domain.ReviewPass, Issues: []domain.ReviewIssue{{Severity: domain.SeverityLow, File: "a.go", Description: "naming"}}}
)

func mediumReview() domain.StepExecution {
	return domain.StepExecution{Action: domain.ActionReview, Status: domain.StepCompleted, ReviewResult: mediumResult}
}

func fixMediumRun(on bool, execs ...domain.StepExecution) *domain.Run {
	run := runWith(wfp(domain.ActionImplement, domain.ActionTest, domain.ActionReview), execs...)
	run.Options.ReviewFixMedium = on
	return run
}

func TestOutcomeOf_FixMedium(t *testing.T) {
	implTest := []domain.StepExecution{done(domain.ActionImplement), done(domain.ActionTest)}
	cases := []struct {
		name   string
		on     bool
		prior  []domain.StepExecution
		review domain.StepExecution
		limits domain.LimitsView
		want   Outcome
	}{
		{"option off", false, implTest, mediumReview(), testLimits, OutcomeSuccess},
		{"on with a medium issue", true, implTest, mediumReview(), testLimits, OutcomeFixable},
		{"on with a medium issue already used", true, append(slices.Clone(implTest), mediumReview(), done(domain.ActionImplement), done(domain.ActionTest)),
			mediumReview(), testLimits, OutcomeSuccess},
		{"on with a medium issue and no iterations left", true,
			append(slices.Clone(implTest), failedReview(), done(domain.ActionImplement), done(domain.ActionTest)),
			mediumReview(), domain.LimitsView{MaxSteps: 20, MaxFixIterations: 1}, OutcomeSuccess},
		{"on with low issues only", true, implTest,
			domain.StepExecution{Action: domain.ActionReview, Status: domain.StepCompleted, ReviewResult: lowResult}, testLimits, OutcomeSuccess},
		{"on with no issues", true, implTest, done(domain.ActionReview), testLimits, OutcomeSuccess},
		{"a failing review stays a failure", true, implTest,
			domain.StepExecution{Action: domain.ActionReview, Status: domain.StepCompleted, ReviewResult: &domain.ReviewResult{
				Status: domain.ReviewPass, Issues: []domain.ReviewIssue{{Severity: domain.SeverityMedium}, {Severity: domain.SeverityHigh}}}},
			testLimits, OutcomeFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := fixMediumRun(tc.on, append(slices.Clone(tc.prior), tc.review)...)
			last := run.Executions[len(run.Executions)-1]
			got, err := outcomeOf(last, run, tc.limits)
			if err != nil || got != tc.want {
				t.Errorf("outcomeOf = %v, %v; want %v", got, err, tc.want)
			}
		})
	}

	t.Run("without implement there is nothing to fix", func(t *testing.T) {
		run := runWith(wfp(domain.ActionReview), mediumReview())
		run.Options.ReviewFixMedium = true
		got, err := outcomeOf(run.Executions[0], run, testLimits)
		if err != nil || got != OutcomeSuccess {
			t.Errorf("outcomeOf = %v, %v; want success", got, err)
		}
	})
}

func TestNext_ReviewFixable(t *testing.T) {
	if next, _ := Next(StateReview, OutcomeFixable, wfOf(domain.ActionImplement, domain.ActionTest, domain.ActionReview)); next != StateImplement {
		t.Errorf("fixable review with implement -> %s, want implement", next)
	}
	if next, reason := Next(StateReview, OutcomeFixable, wfOf(domain.ActionReview)); next != StateDone || reason != "" {
		t.Errorf("fixable review without implement -> %s (%q), want done", next, reason)
	}
	if !isBackwardEntryToImplement(StateReview, StateImplement) {
		t.Error("review -> implement must count as a fix iteration")
	}
}

func TestCounters_FixableReviewIsAFixIteration(t *testing.T) {
	run := fixMediumRun(true, done(domain.ActionImplement), done(domain.ActionTest), mediumReview(),
		done(domain.ActionImplement), done(domain.ActionTest), mediumReview())
	total, fix, err := Counters(run, testLimits)
	if err != nil || total != 6 || fix != 1 {
		t.Errorf("Counters = %d, %d, %v; want 6, 1", total, fix, err)
	}
}

func TestEntry_FixMedium(t *testing.T) {
	cases := []struct {
		name string
		run  *domain.Run
		want State
	}{
		{"after the first review", fixMediumRun(true, done(domain.ActionImplement), done(domain.ActionTest), mediumReview()), StateImplement},
		// The option comes from the run: the limits are all a resume takes from the config now.
		{"the run's option is off", fixMediumRun(false, done(domain.ActionImplement), done(domain.ActionTest), mediumReview()), StateDone},
		{"after the second review", fixMediumRun(true, done(domain.ActionImplement), done(domain.ActionTest), mediumReview(),
			done(domain.ActionImplement), done(domain.ActionTest), mediumReview()), StateDone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := Entry(tc.run, testLimits)
			if err != nil || got != tc.want {
				t.Errorf("Entry = %s, %v; want %s", got, err, tc.want)
			}
		})
	}
}

// review(pass + medium) -> implement -> test -> review(pass + medium) -> done, one fix iteration.
func TestEngine_FixMedium(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}, {Action: domain.ActionReview}}}
	exec := newScriptedExecutor()
	for i := 0; i < 2; i++ {
		exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
		exec.script(domain.ActionTest, passingTests(), nil)
		exec.script(domain.ActionReview, StepOutcome{Outcome: OutcomeSuccess, ReviewResult: mediumResult}, nil)
	}
	run := newRun(wf)
	run.Options.ReviewFixMedium = true
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	want := []domain.ActionType{
		domain.ActionImplement, domain.ActionTest, domain.ActionReview,
		domain.ActionImplement, domain.ActionTest, domain.ActionReview,
	}
	assertActions(t, exec.calls, want)
	if run.Status != domain.RunCompleted || run.FixIterations != 1 {
		t.Errorf("status %s, fix iterations %d; want completed, 1", run.Status, run.FixIterations)
	}
}

// finishedObserver records the outcome each step is reported with.
type finishedObserver struct{ outcomes []Outcome }

func (o *finishedObserver) StepStarted(*domain.Run, int, domain.ActionType) {}
func (o *finishedObserver) StepFinished(_ *domain.Run, _ int, _ domain.StepExecution, outcome StepOutcome) {
	o.outcomes = append(o.outcomes, outcome.Outcome)
}
func (o *finishedObserver) RunFinished(*domain.Run) {}

// The observer is told the outcome the run goes on with, so a fixable review is not reported
// as a plain pass followed by an unexplained second implement.
func TestEngine_FixMediumObserverSeesTheDerivedOutcome(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}, {Action: domain.ActionReview}}}
	exec := newScriptedExecutor()
	for i := 0; i < 2; i++ {
		exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
		exec.script(domain.ActionTest, passingTests(), nil)
		exec.script(domain.ActionReview, StepOutcome{Outcome: OutcomeSuccess, ReviewResult: mediumResult}, nil)
	}
	run := newRun(wf)
	run.Options.ReviewFixMedium = true
	obs := &finishedObserver{}
	engine := &Engine{Executor: exec, Observer: obs, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	want := []Outcome{OutcomeSuccess, OutcomeSuccess, OutcomeFixable, OutcomeSuccess, OutcomeSuccess, OutcomeSuccess}
	if !slices.Equal(obs.outcomes, want) {
		t.Errorf("reported outcomes %v, want %v", obs.outcomes, want)
	}
}

// A history outcomeOf cannot read ends the run instead of being continued on a guess, as it
// ends a resume: Entry and Counters treat the same error as fatal.
func TestEngine_UnreadableHistoryEndsTheRun(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}, {Action: domain.ActionReview}}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeSuccess}, nil) // completed, but no test result
	exec.script(domain.ActionReview, StepOutcome{Outcome: OutcomeSuccess, ReviewResult: mediumResult}, nil)
	run := newRun(wf)
	run.Options.ReviewFixMedium = true
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	err := engine.Run(context.Background(), run)
	if err == nil || !strings.Contains(err.Error(), "cannot derive where to continue") {
		t.Fatalf("err = %v, want one saying where to continue cannot be derived", err)
	}
	if run.Status != domain.RunFailed || !strings.Contains(run.FailureReason, "no test result") {
		t.Errorf("status %s, reason %q; want failed, naming the missing test result", run.Status, run.FailureReason)
	}
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionImplement, domain.ActionTest, domain.ActionReview})
}

func TestEngine_FixMediumOffIgnoresMediumIssues(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}, {Action: domain.ActionReview}}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionTest, passingTests(), nil)
	exec.script(domain.ActionReview, StepOutcome{Outcome: OutcomeSuccess, ReviewResult: mediumResult}, nil)
	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionImplement, domain.ActionTest, domain.ActionReview})
	if run.FixIterations != 0 {
		t.Errorf("fix iterations %d, want 0", run.FixIterations)
	}
}
