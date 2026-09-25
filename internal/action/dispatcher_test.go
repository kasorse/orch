package action

import (
	"context"
	"errors"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
	"orchestrator/internal/repository"
	"orchestrator/internal/testexec"
	"orchestrator/internal/workflow"
)

type fakeClient struct {
	output  string
	stderr  string
	err     error
	lastReq provider.Request
}

func (f *fakeClient) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	f.lastReq = req
	if f.err != nil {
		// Mirrors the real provider adapters: the process's stdout/stderr are still
		// returned alongside a failure, not discarded (this is the F3 fix under test).
		return provider.Result{Output: f.output, Stderr: f.stderr}, f.err
	}
	return provider.Result{Output: f.output, ExitCode: 0}, nil
}

// ctxCapturingClient blocks until the context it's given is done, then reports what
// killed it - the real provider adapters do the equivalent via exec.CommandContext,
// which this fake stands in for without needing a real subprocess.
type ctxCapturingClient struct {
	observedErr error
}

func (c *ctxCapturingClient) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	<-ctx.Done()
	c.observedErr = ctx.Err()
	return provider.Result{}, ctx.Err()
}

type savedArtifact struct {
	runID      string
	stepIndex  int
	actionType domain.ActionType
	artifact   any
}

type fakeArtifactWriter struct {
	calls []savedArtifact
	err   error
}

func (f *fakeArtifactWriter) SaveArtifact(runID string, stepIndex int, actionType domain.ActionType, artifact any) (string, error) {
	f.calls = append(f.calls, savedArtifact{runID, stepIndex, actionType, artifact})
	if f.err != nil {
		return "", f.err
	}
	return "artifacts/saved.json", nil
}

func testConfig() *config.Config {
	return &config.Config{
		Actions: config.ActionsConfig{
			Research:  config.ActionConfig{ProviderModel: config.ProviderModel{Provider: "claude", Model: "research-model"}},
			Analyze:   config.ActionConfig{ProviderModel: config.ProviderModel{Provider: "claude", Model: "m"}},
			Plan:      config.ActionConfig{ProviderModel: config.ProviderModel{Provider: "claude", Model: "m"}},
			Implement: config.ActionConfig{ProviderModel: config.ProviderModel{Provider: "claude", Model: "m"}},
			Review:    config.ActionConfig{ProviderModel: config.ProviderModel{Provider: "claude", Model: "m"}},
		},
		Test: config.TestConfig{Command: "go test ./..."},
	}
}

func newDispatcher(client provider.Client) *Dispatcher {
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	return New(testConfig(), registry, &repository.Repository{Dir: "/repo"}, nil)
}

func TestExecuteAnalyze_Success(t *testing.T) {
	client := &fakeClient{output: `{"summary":"s","root_cause":"rc","affected_files":["a.go"],"recommendation":"r","risks":[]}`}
	d := newDispatcher(client)
	writer := &fakeArtifactWriter{}
	d.Artifacts = writer

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix the bug"}}
	outcome, err := d.Execute(context.Background(), domain.ActionAnalyze, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Outcome != workflow.OutcomeSuccess {
		t.Errorf("expected success, got %s", outcome.Outcome)
	}
	if d.lastAnalysis == nil || d.lastAnalysis.Summary != "s" {
		t.Errorf("expected analysis to be stored, got %+v", d.lastAnalysis)
	}
	if len(writer.calls) != 1 {
		t.Fatalf("expected one artifact save, got %d", len(writer.calls))
	}
	if outcome.ArtifactPath == "" {
		t.Error("expected artifact path to be set")
	}
}

func TestExecuteAnalyze_MalformedJSON_FallsBackToRawText(t *testing.T) {
	client := &fakeClient{output: "not json at all"}
	d := newDispatcher(client)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix the bug"}}
	outcome, err := d.Execute(context.Background(), domain.ActionAnalyze, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Outcome != workflow.OutcomeSuccess {
		t.Errorf("expected success (lenient parsing), got %s", outcome.Outcome)
	}
	if d.lastAnalysis.Summary != "not json at all" {
		t.Errorf("expected raw text fallback, got %q", d.lastAnalysis.Summary)
	}
}

func TestExecutePlan_Success(t *testing.T) {
	client := &fakeClient{output: `{"summary":"s","steps":["step1"],"affected_files":[],"risks":[]}`}
	d := newDispatcher(client)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "add feature"}}
	outcome, err := d.Execute(context.Background(), domain.ActionPlan, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Outcome != workflow.OutcomeSuccess {
		t.Errorf("expected success, got %s", outcome.Outcome)
	}
	if d.lastPlan == nil || d.lastPlan.Summary != "s" {
		t.Errorf("expected plan to be stored, got %+v", d.lastPlan)
	}
}

func TestExecuteImplement_IncludesAnalysisAndPlanContext(t *testing.T) {
	client := &fakeClient{output: `{"summary":"done","changed_files":["a.go"],"tests_added":[]}`}
	d := newDispatcher(client)
	d.lastAnalysis = &domain.AnalysisArtifact{Summary: "analysis summary", RootCause: "rc"}
	d.lastPlan = &domain.PlanArtifact{Summary: "plan summary", Steps: []string{"step1"}}

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	outcome, err := d.Execute(context.Background(), domain.ActionImplement, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Outcome != workflow.OutcomeSuccess {
		t.Errorf("expected success, got %s", outcome.Outcome)
	}
	if !contains(client.lastReq.Prompt, "analysis summary") || !contains(client.lastReq.Prompt, "plan summary") {
		t.Errorf("expected prompt to include analysis and plan context, got: %s", client.lastReq.Prompt)
	}
	if d.lastImpl == nil || d.lastImpl.Summary != "done" {
		t.Errorf("expected implementation to be stored, got %+v", d.lastImpl)
	}
}

func TestExecuteImplement_IncludesTestFailureContext_AndClearsIt(t *testing.T) {
	client := &fakeClient{output: `{"summary":"fixed","changed_files":[],"tests_added":[]}`}
	d := newDispatcher(client)
	d.pendingTestFailure = &domain.TestResult{Command: "go test ./...", ExitCode: 1, Stdout: "FAIL: TestFoo"}

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	_, err := d.Execute(context.Background(), domain.ActionImplement, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(client.lastReq.Prompt, "FAIL: TestFoo") {
		t.Errorf("expected prompt to include failing test output, got: %s", client.lastReq.Prompt)
	}
	if d.pendingTestFailure != nil {
		t.Error("expected pending test failure to be cleared after being consumed")
	}
}

func TestExecuteImplement_IncludesReviewIssuesContext_AndClearsIt(t *testing.T) {
	client := &fakeClient{output: `{"summary":"fixed","changed_files":[],"tests_added":[]}`}
	d := newDispatcher(client)
	d.pendingReviewIssues = []domain.ReviewIssue{{Severity: domain.SeverityHigh, File: "a.go", Description: "still broken"}}

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	_, err := d.Execute(context.Background(), domain.ActionImplement, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(client.lastReq.Prompt, "still broken") {
		t.Errorf("expected prompt to include review issues, got: %s", client.lastReq.Prompt)
	}
	if d.pendingReviewIssues != nil {
		t.Error("expected pending review issues to be cleared after being consumed")
	}
}

func TestExecuteTest_Success(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "PASS", ExitCode: 0}},
	}
	d := New(testConfig(), provider.NewRegistry(), &repository.Repository{Dir: "/repo"}, testexec.New(exec))

	run := &domain.Run{ID: "run-1"}
	outcome, err := d.Execute(context.Background(), domain.ActionTest, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Outcome != workflow.OutcomeSuccess {
		t.Errorf("expected success, got %s", outcome.Outcome)
	}
	if outcome.TestResult == nil || !outcome.TestResult.Success {
		t.Errorf("expected successful test result, got %+v", outcome.TestResult)
	}
	if d.pendingTestFailure != nil {
		t.Error("expected no pending test failure on success")
	}
}

func TestExecuteTest_Failure(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "", Stderr: "FAIL", ExitCode: 1}},
	}
	d := New(testConfig(), provider.NewRegistry(), &repository.Repository{Dir: "/repo"}, testexec.New(exec))

	run := &domain.Run{ID: "run-1"}
	outcome, err := d.Execute(context.Background(), domain.ActionTest, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Outcome != workflow.OutcomeFailure {
		t.Errorf("expected failure, got %s", outcome.Outcome)
	}
	if d.pendingTestFailure == nil {
		t.Error("expected pending test failure to be set")
	}
}

func reviewRepo(t *testing.T, diffOutput, statusOutput string) *repository.Repository {
	t.Helper()
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{
			{Stdout: diffOutput, ExitCode: 0},
			{Stdout: statusOutput, ExitCode: 0},
		},
	}
	return &repository.Repository{Dir: "/repo", Executor: exec}
}

func TestExecuteReview_Pass(t *testing.T) {
	client := &fakeClient{output: `{"status":"pass","summary":"looks good","issues":[]}`}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	d := New(testConfig(), registry, reviewRepo(t, "diff content", ""), nil)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	outcome, err := d.Execute(context.Background(), domain.ActionReview, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Outcome != workflow.OutcomeSuccess {
		t.Errorf("expected success, got %s", outcome.Outcome)
	}
	if !contains(client.lastReq.Prompt, "diff content") {
		t.Errorf("expected prompt to include git diff, got: %s", client.lastReq.Prompt)
	}
}

func TestExecuteReview_FailOnHighSeverity(t *testing.T) {
	client := &fakeClient{output: `{"status":"fail","summary":"bad","issues":[{"severity":"high","file":"a.go","description":"bug"}]}`}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	d := New(testConfig(), registry, reviewRepo(t, "diff content", ""), nil)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	outcome, err := d.Execute(context.Background(), domain.ActionReview, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Outcome != workflow.OutcomeFailure {
		t.Errorf("expected failure, got %s", outcome.Outcome)
	}
	if len(d.pendingReviewIssues) != 1 {
		t.Errorf("expected review issues to be stored, got %+v", d.pendingReviewIssues)
	}
}

func TestExecuteReview_MalformedOutput_HardError(t *testing.T) {
	client := &fakeClient{output: "not json"}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	d := New(testConfig(), registry, reviewRepo(t, "diff content", ""), nil)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	_, err := d.Execute(context.Background(), domain.ActionReview, run)
	if err == nil {
		t.Fatal("expected error")
	}
	var workflowErr *apperr.WorkflowError
	if !errors.As(err, &workflowErr) {
		t.Fatalf("expected *apperr.WorkflowError, got %T", err)
	}
}

func TestExecute_ProviderError_ActionFieldFilledIn(t *testing.T) {
	client := &fakeClient{err: &apperr.ProviderError{Provider: "claude", Model: "m", ExitCode: 1, Stderr: "boom"}}
	d := newDispatcher(client)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	_, err := d.Execute(context.Background(), domain.ActionAnalyze, run)
	if err == nil {
		t.Fatal("expected error")
	}
	var providerErr *apperr.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if providerErr.Action != "analyze" {
		t.Errorf("expected Action to be filled in as analyze, got %q", providerErr.Action)
	}
}

// TestExecuteReview_ActionTimeout_FiresIndependentlyOfParentContext is the direct
// regression test for the reported bug: previously review shared one deadline with
// every earlier step, so a slow-but-legitimate review call could be killed mid-work
// purely because analyze/implement/test had already spent most of the run-wide
// execution.timeout. actions.review.timeout now gives review its own, dedicated
// deadline, independent of whether the parent context has a deadline at all.
func TestExecuteReview_ActionTimeout_FiresIndependentlyOfParentContext(t *testing.T) {
	client := &ctxCapturingClient{}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	cfg := testConfig()
	cfg.Actions.Review.Timeout = 10 * time.Millisecond
	d := New(cfg, registry, reviewRepo(t, "diff content", ""), nil)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	// context.Background() has no deadline of its own - if the action's own timeout
	// did not fire, this call would block forever and the test would time out instead
	// of failing cleanly.
	_, err := d.Execute(context.Background(), domain.ActionReview, run)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(client.observedErr, context.DeadlineExceeded) {
		t.Errorf("expected the action's own context to hit actions.review.timeout, got %v", client.observedErr)
	}
}

// TestExecute_ActionTimeout_NeverExceedsParentDeadline confirms the nested-context
// invariant (MVP.md §25): an action's own timeout can only tighten the bound inherited
// from ctx, never extend the run past it, so a generous per-action override does not
// let review outlive whatever remains of execution.timeout.
func TestExecute_ActionTimeout_NeverExceedsParentDeadline(t *testing.T) {
	client := &ctxCapturingClient{}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	cfg := testConfig()
	cfg.Actions.Review.Timeout = time.Hour // far longer than the parent's deadline below

	parentCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	d := New(cfg, registry, reviewRepo(t, "diff content", ""), nil)
	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	_, err := d.Execute(parentCtx, domain.ActionReview, run)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(client.observedErr, context.DeadlineExceeded) {
		t.Errorf("expected the parent's deadline to still fire despite the 1h override, got %v", client.observedErr)
	}
}

func TestExecute_ProviderError_ConfiguredTimeoutFilledInFromActionOverride(t *testing.T) {
	client := &fakeClient{err: &apperr.ProviderError{Provider: "claude", Model: "m", Kind: apperr.FailureTimeout}}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	cfg := testConfig()
	cfg.Actions.Review.Timeout = 5 * time.Minute
	d := New(cfg, registry, reviewRepo(t, "diff content", ""), nil)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	_, err := d.Execute(context.Background(), domain.ActionReview, run)
	if err == nil {
		t.Fatal("expected error")
	}
	var providerErr *apperr.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if providerErr.ConfiguredTimeout != 5*time.Minute {
		t.Errorf("expected ConfiguredTimeout to be filled in from actions.review.timeout, got %v", providerErr.ConfiguredTimeout)
	}
}

// TestExecute_ProviderError_ConfiguredTimeoutUnsetWhenParentDeadlineIsTighter guards
// against a real bug: when the run-wide execution.timeout budget left in ctx is tighter
// than the action's own override, that leftover run-wide budget is what actually fires
// (context.WithTimeout can only tighten the parent deadline, never extend past it - see
// TestExecute_ActionTimeout_NeverExceedsParentDeadline) - so the resulting error must
// blame execution.timeout, not an override that never had the chance to fire on its own.
func TestExecute_ProviderError_ConfiguredTimeoutUnsetWhenParentDeadlineIsTighter(t *testing.T) {
	client := &fakeClient{err: &apperr.ProviderError{Provider: "claude", Model: "m", Kind: apperr.FailureTimeout}}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	cfg := testConfig()
	cfg.Actions.Review.Timeout = time.Hour // far longer than the parent's deadline below

	parentCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	d := New(cfg, registry, reviewRepo(t, "diff content", ""), nil)
	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	_, err := d.Execute(parentCtx, domain.ActionReview, run)
	if err == nil {
		t.Fatal("expected error")
	}
	var providerErr *apperr.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if providerErr.ConfiguredTimeout != 0 {
		t.Errorf("expected ConfiguredTimeout to stay zero when the parent deadline is the binding constraint, got %v", providerErr.ConfiguredTimeout)
	}
}

func TestExecute_ProviderError_ConfiguredTimeoutUnsetWithoutActionOverride(t *testing.T) {
	client := &fakeClient{err: &apperr.ProviderError{Provider: "claude", Model: "m", Kind: apperr.FailureTimeout}}
	d := newDispatcher(client)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	_, err := d.Execute(context.Background(), domain.ActionAnalyze, run)
	if err == nil {
		t.Fatal("expected error")
	}
	var providerErr *apperr.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if providerErr.ConfiguredTimeout != 0 {
		t.Errorf("expected ConfiguredTimeout to stay zero with no actions.analyze.timeout set, got %v", providerErr.ConfiguredTimeout)
	}
}

type fakeLogPathBuilder struct{}

func (fakeLogPathBuilder) LogPath(runID string, stepIndex int, actionType domain.ActionType, stream string) string {
	return runID + "/logs/" + stream
}

func TestExecute_ProviderError_LogPathFilledIn(t *testing.T) {
	client := &fakeClient{
		output: "backend error",
		err:    &apperr.ProviderError{Provider: "claude", Model: "m", ExitCode: 1},
	}
	d := newDispatcher(client)
	d.Logs = fakeLogPathBuilder{}

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	_, err := d.Execute(context.Background(), domain.ActionAnalyze, run)
	if err == nil {
		t.Fatal("expected error")
	}
	var providerErr *apperr.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if providerErr.LogPath != "run-1/logs/stdout" {
		t.Errorf("expected LogPath to point at the stdout log (since stdout was non-empty), got %q", providerErr.LogPath)
	}
}

// TestExecuteAnalyze_ProviderFailure_PopulatesRawOutput is the direct regression test for
// the reported bug (F3): a failing provider call must not be discarded as an empty
// StepOutcome{} - the CLI's stdout (where claude --print writes backend/API errors) has to
// survive into the outcome so the engine's stepFinished wiring can log it.
func TestExecuteAnalyze_ProviderFailure_PopulatesRawOutput(t *testing.T) {
	client := &fakeClient{
		output: "backend error: rate limited",
		err:    &apperr.ProviderError{Provider: "claude", Model: "m", ExitCode: 1},
	}
	d := newDispatcher(client)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	outcome, err := d.Execute(context.Background(), domain.ActionAnalyze, run)
	if err == nil {
		t.Fatal("expected error")
	}
	if outcome.RawStdout != "backend error: rate limited" {
		t.Errorf("expected RawStdout to carry the provider's stdout, got %q", outcome.RawStdout)
	}
	if outcome.Provider == nil || outcome.Provider.Provider != "claude" || outcome.Provider.Model != "m" {
		t.Errorf("expected Provider to be set on failure outcome, got %+v", outcome.Provider)
	}
}

func TestExecuteImplement_ProviderFailure_PopulatesRawOutput(t *testing.T) {
	client := &fakeClient{
		stderr: "connection reset",
		err:    &apperr.ProviderError{Provider: "claude", Model: "m", ExitCode: 1},
	}
	d := newDispatcher(client)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	outcome, err := d.Execute(context.Background(), domain.ActionImplement, run)
	if err == nil {
		t.Fatal("expected error")
	}
	if outcome.RawStderr != "connection reset" {
		t.Errorf("expected RawStderr to carry the provider's stderr, got %q", outcome.RawStderr)
	}
	if outcome.Provider == nil {
		t.Error("expected Provider to be set on failure outcome")
	}
}

func TestExecuteReview_ProviderFailure_PopulatesRawOutput(t *testing.T) {
	client := &fakeClient{
		output: "boom on review",
		err:    &apperr.ProviderError{Provider: "claude", Model: "m", ExitCode: 1},
	}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	d := New(testConfig(), registry, reviewRepo(t, "diff content", ""), nil)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	outcome, err := d.Execute(context.Background(), domain.ActionReview, run)
	if err == nil {
		t.Fatal("expected error")
	}
	if outcome.RawStdout != "boom on review" {
		t.Errorf("expected RawStdout to carry the provider's stdout, got %q", outcome.RawStdout)
	}
	if outcome.Provider == nil {
		t.Error("expected Provider to be set on failure outcome")
	}
}

// TestExecuteReview_DiffFailure_NotAProviderCall confirms the deliberate asymmetry: a
// repository.Diff error happens before any provider call, so there is no ref/result to
// build a failureOutcome from - it stays the zero-value StepOutcome{}, matching
// testaction.go's pattern for non-provider errors.
func TestExecuteReview_DiffFailure_NotAProviderCall(t *testing.T) {
	client := &fakeClient{}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, client)
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{ExitCode: 1, Stderr: "not a git repo"}},
	}
	repo := &repository.Repository{Dir: "/repo", Executor: exec}
	d := New(testConfig(), registry, repo, nil)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	outcome, err := d.Execute(context.Background(), domain.ActionReview, run)
	if err == nil {
		t.Fatal("expected error")
	}
	if outcome.Provider != nil || outcome.RawStdout != "" || outcome.RawStderr != "" {
		t.Errorf("expected zero-value outcome for a non-provider error, got %+v", outcome)
	}
}

func TestExecute_UnknownAction(t *testing.T) {
	d := newDispatcher(&fakeClient{})
	run := &domain.Run{ID: "run-1"}
	_, err := d.Execute(context.Background(), domain.ActionType("bogus"), run)
	if err == nil {
		t.Fatal("expected error")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
