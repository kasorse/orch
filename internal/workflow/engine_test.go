package workflow

import (
	"context"
	"errors"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// scriptedExecutor returns outcomes[action] in order, one per call, cycling through
// each action's own slice independently so retries (e.g. a second call to implement)
// get their own scripted outcome.
type scriptedExecutor struct {
	outcomes map[domain.ActionType][]StepOutcome
	errs     map[domain.ActionType][]error
	calls    []domain.ActionType
	callIdx  map[domain.ActionType]int
}

func newScriptedExecutor() *scriptedExecutor {
	return &scriptedExecutor{
		outcomes: make(map[domain.ActionType][]StepOutcome),
		errs:     make(map[domain.ActionType][]error),
		callIdx:  make(map[domain.ActionType]int),
	}
}

func (s *scriptedExecutor) script(action domain.ActionType, outcome StepOutcome, err error) {
	s.outcomes[action] = append(s.outcomes[action], outcome)
	s.errs[action] = append(s.errs[action], err)
}

func (s *scriptedExecutor) Execute(ctx context.Context, action domain.ActionType, run *domain.Run) (StepOutcome, error) {
	s.calls = append(s.calls, action)
	idx := s.callIdx[action]
	s.callIdx[action]++

	outcomes := s.outcomes[action]
	if idx >= len(outcomes) {
		return StepOutcome{Outcome: OutcomeSuccess}, nil
	}
	return outcomes[idx], s.errs[action][idx]
}

func newRun(wf domain.Workflow) *domain.Run {
	return &domain.Run{Workflow: &wf}
}

func TestEngine_SuccessfulForwardWorkflow(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionResearch}, {Action: domain.ActionImplement}, {Action: domain.ActionTest}, {Action: domain.ActionReview},
	}}
	exec := newScriptedExecutor()
	for _, a := range []domain.ActionType{domain.ActionResearch, domain.ActionImplement, domain.ActionTest, domain.ActionReview} {
		exec.script(a, StepOutcome{Outcome: OutcomeSuccess}, nil)
	}

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	err := engine.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != domain.RunCompleted {
		t.Errorf("expected completed, got %s", run.Status)
	}
	want := []domain.ActionType{domain.ActionResearch, domain.ActionImplement, domain.ActionTest, domain.ActionReview}
	assertActions(t, exec.calls, want)
}

func TestEngine_TestFailureLoopsToImplement(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionImplement}, {Action: domain.ActionTest}, {Action: domain.ActionReview},
	}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeFailure}, nil)
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionReview, StepOutcome{Outcome: OutcomeSuccess}, nil)

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != domain.RunCompleted {
		t.Errorf("expected completed, got %s", run.Status)
	}
	want := []domain.ActionType{
		domain.ActionImplement, domain.ActionTest,
		domain.ActionImplement, domain.ActionTest, domain.ActionReview,
	}
	assertActions(t, exec.calls, want)
	if run.FixIterations != 1 {
		t.Errorf("expected 1 fix iteration, got %d", run.FixIterations)
	}
}

func TestEngine_ReviewFailureLoopsToImplement(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionImplement}, {Action: domain.ActionReview},
	}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionReview, StepOutcome{Outcome: OutcomeFailure}, nil)
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionReview, StepOutcome{Outcome: OutcomeSuccess}, nil)

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionImplement, domain.ActionReview, domain.ActionImplement, domain.ActionReview}
	assertActions(t, exec.calls, want)
	if run.FixIterations != 1 {
		t.Errorf("expected 1 fix iteration, got %d", run.FixIterations)
	}
}

func TestEngine_ReviewFailureLoopsThroughTestThenReview(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionImplement}, {Action: domain.ActionTest}, {Action: domain.ActionReview},
	}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionReview, StepOutcome{Outcome: OutcomeFailure}, nil)
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionReview, StepOutcome{Outcome: OutcomeSuccess}, nil)

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{
		domain.ActionImplement, domain.ActionTest, domain.ActionReview,
		domain.ActionImplement, domain.ActionTest, domain.ActionReview,
	}
	assertActions(t, exec.calls, want)
}

func TestEngine_MaxFixIterationsExceeded(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionImplement}, {Action: domain.ActionTest},
	}}
	exec := newScriptedExecutor()
	// implement always succeeds, test always fails - an infinite loop without the limit.
	for i := 0; i < 10; i++ {
		exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
		exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeFailure}, nil)
	}

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 100, MaxFixIterations: 2}}

	err := engine.Run(context.Background(), run)
	if err == nil {
		t.Fatal("expected error")
	}
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *apperr.LimitExceededError, got %T", err)
	}
	if run.Status != domain.RunStopped {
		t.Errorf("expected stopped, got %s", run.Status)
	}
	if run.FixIterations != 3 {
		t.Errorf("expected fix iterations to reach 3 (exceeding max of 2), got %d", run.FixIterations)
	}
	// implement+test run 3 times before the 4th fix iteration is refused (initial +
	// 2 allowed retries + the one that trips the limit before executing again).
	if len(exec.calls) != 6 {
		t.Errorf("expected exactly 6 calls (3 rounds of implement+test), got %d: %v", len(exec.calls), exec.calls)
	}
}

func TestEngine_MaxStepsExceeded(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionImplement}, {Action: domain.ActionTest},
	}}
	exec := newScriptedExecutor()
	for i := 0; i < 10; i++ {
		exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
		exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeFailure}, nil)
	}

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 4, MaxFixIterations: 100}}

	err := engine.Run(context.Background(), run)
	if err == nil {
		t.Fatal("expected error")
	}
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *apperr.LimitExceededError, got %T", err)
	}
	if run.Status != domain.RunStopped {
		t.Errorf("expected stopped, got %s", run.Status)
	}
	if len(exec.calls) != 4 {
		t.Errorf("expected exactly 4 calls before stopping, got %d: %v", len(exec.calls), exec.calls)
	}
}

// budgetExecutor scripts three steps (research, investigate, implement) that each report usage.
func budgetExecutor(usage domain.Usage) *scriptedExecutor {
	exec := newScriptedExecutor()
	for _, a := range []domain.ActionType{domain.ActionResearch, domain.ActionInvestigate, domain.ActionImplement} {
		u := usage
		exec.script(a, StepOutcome{Outcome: OutcomeSuccess, Usage: &u}, nil)
	}
	return exec
}

func budgetWorkflow() domain.Workflow {
	return domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionResearch}, {Action: domain.ActionInvestigate}, {Action: domain.ActionImplement},
	}}
}

func TestEngine_BudgetStopsBeforeTheNextStep(t *testing.T) {
	exec := budgetExecutor(domain.Usage{CostUSD: 3})
	run := newRun(budgetWorkflow())
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2, MaxCostUSD: 5}}

	err := engine.Run(context.Background(), run)

	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *apperr.LimitExceededError, got %T: %v", err, err)
	}
	if run.Status != domain.RunStopped {
		t.Errorf("expected stopped, got %s", run.Status)
	}
	if want := "budget exceeded: $6.00 of $5.00"; run.FailureReason != want {
		t.Errorf("FailureReason = %q, want %q", run.FailureReason, want)
	}
	// The check runs before a step, so the second step overshoots and the third never starts.
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionResearch, domain.ActionInvestigate})
}

func TestEngine_BudgetReachedStopsTheThirdStep(t *testing.T) {
	exec := budgetExecutor(domain.Usage{CostUSD: 2})
	run := newRun(budgetWorkflow())
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2, MaxCostUSD: 4}}

	err := engine.Run(context.Background(), run)

	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *apperr.LimitExceededError, got %T: %v", err, err)
	}
	if run.Status != domain.RunStopped {
		t.Errorf("expected stopped, got %s", run.Status)
	}
	if want := "budget exceeded: $4.00 of $4.00"; run.FailureReason != want {
		t.Errorf("FailureReason = %q, want %q", run.FailureReason, want)
	}
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionResearch, domain.ActionInvestigate})
}

func TestEngine_BudgetNotReachedRunsAllSteps(t *testing.T) {
	exec := budgetExecutor(domain.Usage{CostUSD: 3})
	run := newRun(budgetWorkflow())
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2, MaxCostUSD: 7}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != domain.RunCompleted {
		t.Errorf("expected completed, got %s", run.Status)
	}
	if len(exec.calls) != 3 {
		t.Errorf("expected 3 calls, got %v", exec.calls)
	}
}

func TestEngine_NoBudgetNeverStops(t *testing.T) {
	exec := budgetExecutor(domain.Usage{CostUSD: 1000})
	run := newRun(budgetWorkflow())
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != domain.RunCompleted {
		t.Errorf("expected completed, got %s", run.Status)
	}
}

func TestEngine_BudgetSaysLowerBoundForPartialUsage(t *testing.T) {
	exec := newScriptedExecutor()
	exec.script(domain.ActionResearch, StepOutcome{Outcome: OutcomeSuccess, Usage: &domain.Usage{Partial: true}}, nil)
	exec.script(domain.ActionInvestigate, StepOutcome{Outcome: OutcomeSuccess, Usage: &domain.Usage{CostUSD: 3}}, nil)
	run := newRun(budgetWorkflow())
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2, MaxCostUSD: 3}}

	err := engine.Run(context.Background(), run)

	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *apperr.LimitExceededError, got %T: %v", err, err)
	}
	want := "budget exceeded: $3.00 of $3.00 (lower bound: 1 calls without a final report)"
	if run.FailureReason != want {
		t.Errorf("FailureReason = %q, want %q", run.FailureReason, want)
	}
}

func TestEngine_BudgetCountsAResumedRunsHistory(t *testing.T) {
	exec := newScriptedExecutor()
	run := newRun(budgetWorkflow())
	run.Executions = []domain.StepExecution{
		{Index: 0, Action: domain.ActionResearch, Status: domain.StepCompleted, Usage: &domain.Usage{CostUSD: 5}},
	}
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2, MaxCostUSD: 5}}

	err := engine.Run(context.Background(), run)

	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *apperr.LimitExceededError, got %T: %v", err, err)
	}
	if len(exec.calls) != 0 {
		t.Errorf("expected no step to run, got %v", exec.calls)
	}
	if run.Status != domain.RunStopped {
		t.Errorf("expected stopped, got %s", run.Status)
	}
}

// The clarify step and the clarity re-checks are paid from the same budget (specs/clarify): when
// they used it up, no step runs.
func TestEngine_BudgetCountsTheClarifyCalls(t *testing.T) {
	exec := budgetExecutor(domain.Usage{CostUSD: 1})
	run := newRun(budgetWorkflow())
	engine := &Engine{
		Executor:      exec,
		Limits:        domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2, MaxCostUSD: 2},
		DecisionCalls: []domain.Usage{{CostUSD: 1.5}, {CostUSD: 0.5}},
	}

	err := engine.Run(context.Background(), run)

	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *apperr.LimitExceededError, got %T: %v", err, err)
	}
	if want := "budget exceeded: $2.00 of $2.00"; run.FailureReason != want {
		t.Errorf("FailureReason = %q, want %q", run.FailureReason, want)
	}
	if len(exec.calls) != 0 {
		t.Errorf("expected no step to run, got %v", exec.calls)
	}

	// Under the budget, they still count: the first step overshoots it and the second never starts.
	exec = budgetExecutor(domain.Usage{CostUSD: 1})
	run = newRun(budgetWorkflow())
	engine = &Engine{
		Executor:      exec,
		Limits:        domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2, MaxCostUSD: 2},
		DecisionCalls: []domain.Usage{{CostUSD: 1.5}},
	}
	if err := engine.Run(context.Background(), run); !errors.As(err, &limitErr) {
		t.Fatalf("expected *apperr.LimitExceededError, got %T: %v", err, err)
	}
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionResearch})
}

func TestEngine_HardErrorEndsRunImmediately(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionImplement}, {Action: domain.ActionReview},
	}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionReview, StepOutcome{}, &apperr.WorkflowError{Msg: "malformed review output"})

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	err := engine.Run(context.Background(), run)
	if err == nil {
		t.Fatal("expected error")
	}
	if run.Status != domain.RunFailed {
		t.Errorf("expected failed, got %s", run.Status)
	}
	if len(exec.calls) != 2 {
		t.Errorf("expected no further calls after the hard error, got %d: %v", len(exec.calls), exec.calls)
	}
}

// fakeGate returns decisions[action] in order, one per call, defaulting to
// GateProceed once its scripted decisions for an action are exhausted.
type fakeGate struct {
	decisions map[domain.ActionType][]GateDecision
	callIdx   map[domain.ActionType]int
	calls     []domain.ActionType
}

func newFakeGate() *fakeGate {
	return &fakeGate{decisions: make(map[domain.ActionType][]GateDecision), callIdx: make(map[domain.ActionType]int)}
}

func (g *fakeGate) script(action domain.ActionType, decision GateDecision) {
	g.decisions[action] = append(g.decisions[action], decision)
}

func (g *fakeGate) BeforeStep(ctx context.Context, run *domain.Run, index int, action domain.ActionType) (GateDecision, error) {
	g.calls = append(g.calls, action)
	idx := g.callIdx[action]
	g.callIdx[action]++

	decisions := g.decisions[action]
	if idx >= len(decisions) {
		return GateProceed, nil
	}
	return decisions[idx], nil
}

func TestEngine_NilGateBehavesLikeToday(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeSuccess}, nil)

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != domain.RunCompleted {
		t.Errorf("expected completed, got %s", run.Status)
	}
}

func TestEngine_GateProceedIsNoOpPassthrough(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeSuccess}, nil)

	gate := newFakeGate()
	gate.script(domain.ActionImplement, GateProceed)
	gate.script(domain.ActionTest, GateProceed)

	run := newRun(wf)
	engine := &Engine{Executor: exec, Gate: gate, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != domain.RunCompleted {
		t.Errorf("expected completed, got %s", run.Status)
	}
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionImplement, domain.ActionTest})
	assertActions(t, gate.calls, []domain.ActionType{domain.ActionImplement, domain.ActionTest})
}

func TestEngine_GateAbortStopsRun(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)

	gate := newFakeGate()
	gate.script(domain.ActionImplement, GateProceed)
	gate.script(domain.ActionTest, GateAbort)

	run := newRun(wf)
	engine := &Engine{Executor: exec, Gate: gate, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	err := engine.Run(context.Background(), run)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected error to satisfy errors.Is(err, context.Canceled), got %v", err)
	}
	if run.Status != domain.RunStopped {
		t.Errorf("expected stopped, got %s", run.Status)
	}
	// the aborted step must never reach the executor.
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionImplement})
}

func assertActions(t *testing.T, got, want []domain.ActionType) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// --- TASKS.md T05: generalized transitions ---------------------------------------

func TestEngine_ResearchThenInvestigateCompletes(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionResearch, Stage: domain.StageResearch},
		{Action: domain.ActionInvestigate, Stage: domain.StageInvestigation},
	}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionResearch, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionInvestigate, StepOutcome{Outcome: OutcomeSuccess}, nil)

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != domain.RunCompleted {
		t.Errorf("expected completed, got %s", run.Status)
	}
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionResearch, domain.ActionInvestigate})
}

// TestEngine_StandaloneTestingFailureFailsRun is TASKS.md В5: a workflow with no
// implement has nothing to retry into, so a failing test ends the run rather than
// trying to execute an implement step that was never part of the workflow.
func TestEngine_StandaloneTestingFailureFailsRun(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionTest, Stage: domain.StageTesting}}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeFailure}, nil)

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	err := engine.Run(context.Background(), run)
	if err == nil {
		t.Fatal("expected error")
	}
	if run.Status != domain.RunFailed {
		t.Errorf("expected failed, got %s", run.Status)
	}
	if run.FailureReason != "tests failed" {
		t.Errorf(`FailureReason = %q, want "tests failed"`, run.FailureReason)
	}
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionTest})
}

// TestEngine_StandaloneReviewFailureStillCompletes is TASKS.md В5's asymmetric half:
// a workflow with no implement completes even when review reports a failing verdict
// - reviewing a diff and reporting it has problems fulfils a review-only task, it
// does not fail the run. The verdict itself is carried on the step's ReviewResult.
func TestEngine_StandaloneReviewFailureStillCompletes(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionReview, Stage: domain.StageReview}}}
	exec := newScriptedExecutor()
	failing := &domain.ReviewResult{Status: domain.ReviewFail}
	exec.script(domain.ActionReview, StepOutcome{Outcome: OutcomeFailure, ReviewResult: failing}, nil)

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != domain.RunCompleted {
		t.Errorf("expected completed, got %s", run.Status)
	}
	if run.FailureReason != "" {
		t.Errorf("expected no failure reason on a completed run, got %q", run.FailureReason)
	}
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionReview})
	if got := run.Executions[len(run.Executions)-1].ReviewResult; got == nil || got.Status != domain.ReviewFail {
		t.Errorf("expected the failing verdict to still be recorded, got %+v", got)
	}
}

// TestEngine_ReviewFailureCascadesFromFullWorkflow re-covers
// TestEngine_ReviewFailureLoopsThroughTestThenReview's scenario explicitly as a T05
// acceptance case: with implement present, a failing review still loops all the way
// back through implement -> test -> review, purely as a consequence of forward
// progress following wf.Steps' order - no special-casing needed.
func TestEngine_ReviewFailureCascadesFromFullWorkflow(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionImplement, Stage: domain.StageDevelopment},
		{Action: domain.ActionTest, Stage: domain.StageTesting},
		{Action: domain.ActionReview, Stage: domain.StageReview},
	}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionReview, StepOutcome{Outcome: OutcomeFailure}, nil)
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionReview, StepOutcome{Outcome: OutcomeSuccess}, nil)

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != domain.RunCompleted {
		t.Errorf("expected completed, got %s", run.Status)
	}
	want := []domain.ActionType{
		domain.ActionImplement, domain.ActionTest, domain.ActionReview,
		domain.ActionImplement, domain.ActionTest, domain.ActionReview,
	}
	assertActions(t, exec.calls, want)
	if run.FixIterations != 1 {
		t.Errorf("expected 1 fix iteration, got %d", run.FixIterations)
	}
}

// TestEngine_TestingOnlyWorkflowCompletesOnSuccess checks the success half of a
// no-implement testing-only workflow, complementing
// TestEngine_StandaloneTestingFailureFailsRun's failure half.
func TestEngine_TestingOnlyWorkflowCompletesOnSuccess(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionTest, Stage: domain.StageTesting}}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionTest, StepOutcome{Outcome: OutcomeSuccess}, nil)

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != domain.RunCompleted {
		t.Errorf("expected completed, got %s", run.Status)
	}
}

// TestEngine_DevelopmentWithoutTestOrReviewCompletesAfterImplement checks a
// development-only workflow (investigation enabled it, but neither testing nor
// review cleared their thresholds): implement's success has nowhere forward to go but
// Done, purely from wf.Steps' order, without any RequiresTest/RequiresReview check.
func TestEngine_DevelopmentWithoutTestOrReviewCompletesAfterImplement(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionInvestigate, Stage: domain.StageInvestigation},
		{Action: domain.ActionImplement, Stage: domain.StageDevelopment},
	}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionInvestigate, StepOutcome{Outcome: OutcomeSuccess}, nil)
	exec.script(domain.ActionImplement, StepOutcome{Outcome: OutcomeSuccess}, nil)

	run := newRun(wf)
	engine := &Engine{Executor: exec, Limits: domain.LimitsView{MaxSteps: 12, MaxFixIterations: 2}}

	if err := engine.Run(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Status != domain.RunCompleted {
		t.Errorf("expected completed, got %s", run.Status)
	}
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionInvestigate, domain.ActionImplement})
}
