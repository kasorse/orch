package classifier

import (
	"context"
	"errors"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

type fakeClient struct {
	output string
	stderr string
	err    error
}

func (f *fakeClient) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	if f.err != nil {
		return provider.Result{Output: f.output, Stderr: f.stderr}, f.err
	}
	return provider.Result{Output: f.output, ExitCode: 0}, nil
}

type fakeLogger struct {
	calls []loggedCall
	err   error
}

type loggedCall struct {
	runID   string
	stream  string
	content string
}

func (f *fakeLogger) AppendClassifierLog(runID, stream, content string) error {
	f.calls = append(f.calls, loggedCall{runID, stream, content})
	return f.err
}

func (f *fakeLogger) ClassifierLogPath(runID, stream string) string {
	return "logs/classify." + stream + ".log"
}

type fakeObserver struct {
	started  []string
	finished []string
}

func (f *fakeObserver) ProviderCallStarted(providerID, model string) {
	f.started = append(f.started, providerID+"/"+model)
}

func (f *fakeObserver) ProviderCallHeartbeat(elapsed time.Duration) {}

func (f *fakeObserver) ProviderCallFinished(providerID, model string, duration time.Duration, err error) {
	f.finished = append(f.finished, providerID+"/"+model)
}

func TestClassify_Valid(t *testing.T) {
	client := &fakeClient{output: `{
		"type": "bugfix",
		"complexity": "medium",
		"requires_code_change": true,
		"requires_tests": true,
		"requires_review": true
	}`}
	c := New(client, "claude", "sonnet", nil)

	got, err := c.Classify(context.Background(), "run-1", "fix the bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Type != domain.TaskBugfix || got.Complexity != domain.LegacyComplexityMedium {
		t.Errorf("unexpected classification: %+v", got)
	}
}

func TestClassify_ValidWithMarkdownFence(t *testing.T) {
	client := &fakeClient{output: "```json\n{\"type\": \"question\", \"complexity\": \"low\", \"requires_code_change\": false, \"requires_tests\": false, \"requires_review\": false}\n```"}
	c := New(client, "claude", "sonnet", nil)

	got, err := c.Classify(context.Background(), "run-1", "what does this do?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Type != domain.TaskQuestion {
		t.Errorf("unexpected classification: %+v", got)
	}
}

func TestClassify_MalformedJSON(t *testing.T) {
	client := &fakeClient{output: "{not valid json"}
	c := New(client, "claude", "sonnet", nil)

	_, err := c.Classify(context.Background(), "run-1", "task")
	if err == nil {
		t.Fatal("expected error")
	}
	var classErr *apperr.ClassificationError
	if !errors.As(err, &classErr) {
		t.Fatalf("expected *apperr.ClassificationError, got %T", err)
	}
}

func TestClassify_InvalidType(t *testing.T) {
	client := &fakeClient{output: `{"type": "not-a-type", "complexity": "low"}`}
	c := New(client, "claude", "sonnet", nil)

	_, err := c.Classify(context.Background(), "run-1", "task")
	var classErr *apperr.ClassificationError
	if !errors.As(err, &classErr) {
		t.Fatalf("expected *apperr.ClassificationError, got %T", err)
	}
}

func TestClassify_InvalidComplexity(t *testing.T) {
	client := &fakeClient{output: `{"type": "bugfix", "complexity": "extreme"}`}
	c := New(client, "claude", "sonnet", nil)

	_, err := c.Classify(context.Background(), "run-1", "task")
	var classErr *apperr.ClassificationError
	if !errors.As(err, &classErr) {
		t.Fatalf("expected *apperr.ClassificationError, got %T", err)
	}
}

func TestClassify_NoJSONObject(t *testing.T) {
	client := &fakeClient{output: "I refuse to answer in JSON."}
	c := New(client, "claude", "sonnet", nil)

	_, err := c.Classify(context.Background(), "run-1", "task")
	var classErr *apperr.ClassificationError
	if !errors.As(err, &classErr) {
		t.Fatalf("expected *apperr.ClassificationError, got %T", err)
	}
}

// TestClassify_ProviderFailure_TagsActionAndLogsBothStreams is the classifier-side
// counterpart to action's F3 regression test: a failing classification call must be
// tagged Action == "classify" (so the rendered error is self-explanatory) and its raw
// stdout/stderr must reach the log file even though there is no workflow step to attach
// them to.
func TestClassify_ProviderFailure_TagsActionAndLogsBothStreams(t *testing.T) {
	client := &fakeClient{
		output: "backend error: rate limited",
		stderr: "connection reset",
		err:    &apperr.ProviderError{Provider: "claude", Model: "sonnet", ExitCode: 1},
	}
	logger := &fakeLogger{}
	c := New(client, "claude", "sonnet", logger)

	_, err := c.Classify(context.Background(), "run-1", "task")
	if err == nil {
		t.Fatal("expected error")
	}
	var providerErr *apperr.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if providerErr.Action != "classify" {
		t.Errorf("expected Action to be filled in as classify, got %q", providerErr.Action)
	}
	if providerErr.LogPath != "logs/classify.stdout.log" {
		t.Errorf("expected LogPath to point at the stdout log (since stdout was non-empty), got %q", providerErr.LogPath)
	}

	if len(logger.calls) != 2 {
		t.Fatalf("expected both streams logged, got %+v", logger.calls)
	}
	var gotStdout, gotStderr bool
	for _, call := range logger.calls {
		if call.runID != "run-1" {
			t.Errorf("expected runID to be passed through, got %q", call.runID)
		}
		switch call.stream {
		case "stdout":
			gotStdout = true
			if call.content != "backend error: rate limited" {
				t.Errorf("unexpected stdout log content: %q", call.content)
			}
		case "stderr":
			gotStderr = true
			if call.content != "connection reset" {
				t.Errorf("unexpected stderr log content: %q", call.content)
			}
		}
	}
	if !gotStdout || !gotStderr {
		t.Errorf("expected both stdout and stderr logged, got %+v", logger.calls)
	}
}

func TestClassify_Success_AlsoLogsStdout(t *testing.T) {
	client := &fakeClient{output: `{"type": "question", "complexity": "low", "requires_code_change": false, "requires_tests": false, "requires_review": false}`}
	logger := &fakeLogger{}
	c := New(client, "claude", "sonnet", logger)

	_, err := c.Classify(context.Background(), "run-1", "what does this do?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logger.calls) != 1 || logger.calls[0].stream != "stdout" {
		t.Fatalf("expected stdout logged on success, got %+v", logger.calls)
	}
}

func TestClassify_NotifiesObserver(t *testing.T) {
	client := &fakeClient{output: `{"type": "question", "complexity": "low", "requires_code_change": false, "requires_tests": false, "requires_review": false}`}
	observer := &fakeObserver{}
	c := New(client, "claude", "sonnet", nil)
	c.Observer = observer

	_, err := c.Classify(context.Background(), "run-1", "what does this do?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(observer.started) != 1 || observer.started[0] != "claude/sonnet" {
		t.Errorf("expected observer to be notified of call start, got %+v", observer.started)
	}
	if len(observer.finished) != 1 || observer.finished[0] != "claude/sonnet" {
		t.Errorf("expected observer to be notified of call finish, got %+v", observer.finished)
	}
}
