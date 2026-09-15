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

func newRun(c domain.Classification) *domain.Run {
	wf, err := workflow.Build(c)
	if err != nil {
		panic(err)
	}
	return &domain.Run{Classification: &c, Workflow: &wf}
}

func TestReviewAndSelectMode_ImmediateApproveWithPinnedMode(t *testing.T) {
	run := newRun(domain.Classification{Type: domain.TaskBugfix, Complexity: domain.ComplexityLow, RequiresTests: true, RequiresReview: true})
	p := &fakePrompter{answers: []string{"a"}}
	var out bytes.Buffer

	mode, approved, err := ReviewAndSelectMode(p, &out, run, domain.ModeManual)
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
	run := newRun(domain.Classification{Type: domain.TaskBugfix, Complexity: domain.ComplexityLow, RequiresTests: true, RequiresReview: true})
	p := &fakePrompter{answers: []string{"a", "2"}}
	var out bytes.Buffer

	mode, approved, err := ReviewAndSelectMode(p, &out, run, "")
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

func TestReviewAndSelectMode_EditFieldsThenApprove(t *testing.T) {
	run := newRun(domain.Classification{Type: domain.TaskBugfix, Complexity: domain.ComplexityLow, RequiresTests: true, RequiresReview: true})
	p := &fakePrompter{answers: []string{
		"e",
		"t", "feature",
		"c", "high",
		"1", "n", // requires_tests -> false
		"2", "y", // requires_review -> true
		"d",
		"a",
		"4", // auto
	}}
	var out bytes.Buffer

	mode, approved, err := ReviewAndSelectMode(p, &out, run, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved {
		t.Fatal("expected approved")
	}
	if mode != domain.ModeAuto {
		t.Errorf("expected auto, got %s", mode)
	}

	if run.OriginalClassification == nil {
		t.Fatal("expected OriginalClassification to be preserved after an edit")
	}
	if run.OriginalClassification.Type != domain.TaskBugfix || run.OriginalClassification.Complexity != domain.ComplexityLow {
		t.Errorf("OriginalClassification not preserved correctly: %+v", run.OriginalClassification)
	}

	if run.Classification.Type != domain.TaskFeature {
		t.Errorf("expected edited type feature, got %s", run.Classification.Type)
	}
	if run.Classification.Complexity != domain.ComplexityHigh {
		t.Errorf("expected edited complexity high, got %s", run.Classification.Complexity)
	}
	if run.Classification.RequiresTests {
		t.Error("expected requires_tests to be false after edit")
	}
	if !run.Classification.RequiresReview {
		t.Error("expected requires_review to be true after edit")
	}

	// feature -> plan, implement, +review (tests disabled).
	wantActions := []domain.ActionType{domain.ActionPlan, domain.ActionImplement, domain.ActionReview}
	if len(run.Workflow.Steps) != len(wantActions) {
		t.Fatalf("workflow not rebuilt as expected: %+v", run.Workflow.Steps)
	}
	for i, a := range wantActions {
		if run.Workflow.Steps[i].Action != a {
			t.Errorf("step %d: got %s, want %s", i, run.Workflow.Steps[i].Action, a)
		}
	}
}

func TestReviewAndSelectMode_RejectsInvalidEditThenSucceeds(t *testing.T) {
	run := newRun(domain.Classification{Type: domain.TaskBugfix, Complexity: domain.ComplexityLow, RequiresTests: true, RequiresReview: true})
	p := &fakePrompter{answers: []string{
		"e",
		"c", "not-a-real-complexity", // rejected by Validate(), classification untouched
		"d",
		"a",
		"1", // plan
	}}
	var out bytes.Buffer

	mode, approved, err := ReviewAndSelectMode(p, &out, run, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved || mode != domain.ModePlan {
		t.Fatalf("expected approved plan mode, got approved=%v mode=%s", approved, mode)
	}
	if run.OriginalClassification != nil {
		t.Error("expected no OriginalClassification since the only edit attempt was invalid")
	}
	if run.Classification.Complexity != domain.ComplexityLow {
		t.Errorf("expected complexity unchanged, got %s", run.Classification.Complexity)
	}
}

func TestReviewAndSelectMode_Cancel(t *testing.T) {
	run := newRun(domain.Classification{Type: domain.TaskBugfix, Complexity: domain.ComplexityLow, RequiresTests: true, RequiresReview: true})
	p := &fakePrompter{answers: []string{"c"}}
	var out bytes.Buffer

	_, approved, err := ReviewAndSelectMode(p, &out, run, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approved {
		t.Error("expected cancel to report approved=false")
	}
}

func TestTerminalGate_DeclineThenRetry(t *testing.T) {
	run := newRun(domain.Classification{Type: domain.TaskBugfix, Complexity: domain.ComplexityLow, RequiresTests: true, RequiresReview: true})
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

func TestTerminalGate_DeclineEditThenRetryProceeds(t *testing.T) {
	run := newRun(domain.Classification{Type: domain.TaskBugfix, Complexity: domain.ComplexityLow, RequiresTests: true, RequiresReview: true})
	p := &fakePrompter{answers: []string{"n", "e", "1", "n", "d", "r"}}
	var out bytes.Buffer
	gate := NewTerminalGate(p, &out)

	decision, err := gate.BeforeStep(context.Background(), run, 0, domain.ActionTest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != workflow.GateProceed {
		t.Errorf("expected proceed, got %s", decision)
	}
	if run.Classification.RequiresTests {
		t.Error("expected requires_tests edited to false")
	}
	if !strings.Contains(out.String(), "Workflow (remaining)") {
		t.Errorf("expected remaining-workflow line after edit, got: %s", out.String())
	}
}

func TestTerminalGate_DeclineThenAbort(t *testing.T) {
	run := newRun(domain.Classification{Type: domain.TaskBugfix, Complexity: domain.ComplexityLow, RequiresTests: true, RequiresReview: true})
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
	run := newRun(domain.Classification{Type: domain.TaskBugfix, Complexity: domain.ComplexityLow, RequiresTests: true, RequiresReview: true})
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
