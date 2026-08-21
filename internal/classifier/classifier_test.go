package classifier

import (
	"context"
	"errors"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

type fakeClient struct {
	output string
	err    error
}

func (f *fakeClient) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	if f.err != nil {
		return provider.Result{}, f.err
	}
	return provider.Result{Output: f.output, ExitCode: 0}, nil
}

func TestClassify_Valid(t *testing.T) {
	client := &fakeClient{output: `{
		"type": "bugfix",
		"complexity": "medium",
		"requires_code_change": true,
		"requires_tests": true,
		"requires_review": true
	}`}
	c := New(client, "sonnet")

	got, err := c.Classify(context.Background(), "fix the bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Type != domain.TaskBugfix || got.Complexity != domain.ComplexityMedium {
		t.Errorf("unexpected classification: %+v", got)
	}
}

func TestClassify_ValidWithMarkdownFence(t *testing.T) {
	client := &fakeClient{output: "```json\n{\"type\": \"question\", \"complexity\": \"low\", \"requires_code_change\": false, \"requires_tests\": false, \"requires_review\": false}\n```"}
	c := New(client, "sonnet")

	got, err := c.Classify(context.Background(), "what does this do?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Type != domain.TaskQuestion {
		t.Errorf("unexpected classification: %+v", got)
	}
}

func TestClassify_MalformedJSON(t *testing.T) {
	client := &fakeClient{output: "{not valid json"}
	c := New(client, "sonnet")

	_, err := c.Classify(context.Background(), "task")
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
	c := New(client, "sonnet")

	_, err := c.Classify(context.Background(), "task")
	var classErr *apperr.ClassificationError
	if !errors.As(err, &classErr) {
		t.Fatalf("expected *apperr.ClassificationError, got %T", err)
	}
}

func TestClassify_InvalidComplexity(t *testing.T) {
	client := &fakeClient{output: `{"type": "bugfix", "complexity": "extreme"}`}
	c := New(client, "sonnet")

	_, err := c.Classify(context.Background(), "task")
	var classErr *apperr.ClassificationError
	if !errors.As(err, &classErr) {
		t.Fatalf("expected *apperr.ClassificationError, got %T", err)
	}
}

func TestClassify_NoJSONObject(t *testing.T) {
	client := &fakeClient{output: "I refuse to answer in JSON."}
	c := New(client, "sonnet")

	_, err := c.Classify(context.Background(), "task")
	var classErr *apperr.ClassificationError
	if !errors.As(err, &classErr) {
		t.Fatalf("expected *apperr.ClassificationError, got %T", err)
	}
}
