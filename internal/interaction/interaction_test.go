package interaction

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// fakePrompter returns queued answers in order, one per Ask call, and errors once
// they're exhausted (simulating stdin closing / EOF mid-flow).
type fakePrompter struct {
	answers []string
	idx     int
}

func (f *fakePrompter) Ask(prompt string) (string, error) {
	if f.idx >= len(f.answers) {
		return "", errors.New("fakePrompter: no more scripted answers")
	}
	a := f.answers[f.idx]
	f.idx++
	return a, nil
}

func newRun() *domain.Run {
	return &domain.Run{Workflow: &domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionImplement, Stage: domain.StageDevelopment},
		{Action: domain.ActionTest, Stage: domain.StageTesting},
	}}}
}

func TestReviewAndSelectMode_ImmediateApproveWithPinnedMode(t *testing.T) {
	p := &fakePrompter{answers: []string{"a"}}
	var out bytes.Buffer

	mode, approved, err := ReviewAndSelectMode(p, &out, domain.ModeManual)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved {
		t.Fatal("expected approved")
	}
	if mode != domain.ModeManual {
		t.Errorf("expected pinned mode to win, got %s", mode)
	}
	if p.idx != 1 {
		t.Errorf("expected the mode-select prompt to be skipped when pinned, consumed %d answers", p.idx)
	}
}

func TestReviewAndSelectMode_ApproveThenInteractiveModeSelect(t *testing.T) {
	p := &fakePrompter{answers: []string{"a", "2"}}
	var out bytes.Buffer

	mode, approved, err := ReviewAndSelectMode(p, &out, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved {
		t.Fatal("expected approved")
	}
	if mode != domain.ModeManual {
		t.Errorf("expected manual (option 2), got %s", mode)
	}
}

func TestReviewAndSelectMode_Cancel(t *testing.T) {
	p := &fakePrompter{answers: []string{"c"}}
	var out bytes.Buffer

	_, approved, err := ReviewAndSelectMode(p, &out, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approved {
		t.Error("expected cancel to report approved=false")
	}
}

func TestTerminalGate_DeclineThenRetry(t *testing.T) {
	run := newRun()
	p := &fakePrompter{answers: []string{"n", "r"}}
	var out bytes.Buffer
	gate := NewTerminalGate(p, &out)

	decision, err := gate.BeforeStep(context.Background(), run, 0, domain.ActionTest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != workflow.GateProceed {
		t.Errorf("expected proceed, got %s", decision)
	}
}

func TestTerminalGate_DeclineThenAbort(t *testing.T) {
	run := newRun()
	p := &fakePrompter{answers: []string{"n", "a"}}
	var out bytes.Buffer
	gate := NewTerminalGate(p, &out)

	decision, err := gate.BeforeStep(context.Background(), run, 0, domain.ActionTest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != workflow.GateAbort {
		t.Errorf("expected abort, got %s", decision)
	}
}

func TestTerminalGate_AllRemainingSkipsFuturePrompts(t *testing.T) {
	run := newRun()
	p := &fakePrompter{answers: []string{"A"}}
	var out bytes.Buffer
	gate := NewTerminalGate(p, &out)

	if _, err := gate.BeforeStep(context.Background(), run, 0, domain.ActionImplement); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// no further scripted answers queued - if the gate prompts again this will error.
	decision, err := gate.BeforeStep(context.Background(), run, 1, domain.ActionTest)
	if err != nil {
		t.Fatalf("expected passthrough to skip prompting, got error: %v", err)
	}
	if decision != workflow.GateProceed {
		t.Errorf("expected proceed, got %s", decision)
	}
}

func TestTerminalPrompter_EOFGivesActionableGuidance(t *testing.T) {
	p := NewTerminalPrompter(strings.NewReader(""), &bytes.Buffer{})
	_, err := p.Ask("Approve? ")
	if err == nil {
		t.Fatal("expected error on EOF")
	}
	if !strings.Contains(err.Error(), "--mode auto") {
		t.Errorf("expected guidance toward --mode auto, got: %v", err)
	}
}
