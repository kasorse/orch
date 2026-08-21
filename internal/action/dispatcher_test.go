package action

import (
	"context"
	"errors"
	"testing"

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
	err     error
	lastReq provider.Request
}

func (f *fakeClient) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	f.lastReq = req
	if f.err != nil {
		return provider.Result{}, f.err
	}
	return provider.Result{Output: f.output, ExitCode: 0}, nil
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
			Analyze:   config.ProviderModel{Provider: "claude", Model: "m"},
			Plan:      config.ProviderModel{Provider: "claude", Model: "m"},
			Implement: config.ProviderModel{Provider: "claude", Model: "m"},
			Review:    config.ProviderModel{Provider: "claude", Model: "m"},
			Answer:    config.ProviderModel{Provider: "claude", Model: "m"},
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

func TestExecuteAnswer_Success(t *testing.T) {
	client := &fakeClient{output: `{"answer":"42"}`}
	d := newDispatcher(client)

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "what is the answer"}}
	outcome, err := d.Execute(context.Background(), domain.ActionAnswer, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Outcome != workflow.OutcomeSuccess {
		t.Errorf("expected success, got %s", outcome.Outcome)
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
