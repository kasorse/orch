package interaction

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/workflow"
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

	mode, approved, err := ReviewAndSelectMode(p, &out, domain.ModeManual, "")
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

	mode, approved, err := ReviewAndSelectMode(p, &out, "", "")
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

func TestReviewAndSelectMode_DefaultMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answers []string
		pinned  domain.RunMode
		want    domain.RunMode
	}{
		{"empty answer takes the default", []string{"a", ""}, "", domain.ModeAuto},
		{"an explicit choice wins", []string{"a", "2"}, "", domain.ModeManual},
		{"--mode wins", []string{"a"}, domain.ModeAcceptEdits, domain.ModeAcceptEdits},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePrompter{answers: tc.answers}
			var out bytes.Buffer
			mode, approved, err := ReviewAndSelectMode(p, &out, tc.pinned, domain.ModeAuto)
			if err != nil || !approved || mode != tc.want {
				t.Fatalf("ReviewAndSelectMode = %s, %v, %v; want %s", mode, approved, err, tc.want)
			}
			if p.idx != len(tc.answers) {
				t.Errorf("consumed %d answers, want %d", p.idx, len(tc.answers))
			}
		})
	}
}

func TestReviewAndSelectMode_EmptyAnswerWithoutDefaultAsksAgain(t *testing.T) {
	p := &fakePrompter{answers: []string{"a", "", "3"}}
	var out bytes.Buffer
	mode, _, err := ReviewAndSelectMode(p, &out, "", "")
	if err != nil || mode != domain.ModeAcceptEdits {
		t.Fatalf("ReviewAndSelectMode = %s, %v; want accept-edits", mode, err)
	}
	if !strings.Contains(out.String(), "unrecognized option") {
		t.Errorf("an empty answer without a default must be rejected, output:\n%s", out.String())
	}
}

func TestReviewAndSelectMode_Cancel(t *testing.T) {
	p := &fakePrompter{answers: []string{"c"}}
	var out bytes.Buffer

	_, approved, err := ReviewAndSelectMode(p, &out, "", "")
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

func TestTerminalGate_ShowsChangesAfterImplement(t *testing.T) {
	var out bytes.Buffer
	g := NewTerminalGate(&fakePrompter{answers: []string{"y", "y"}}, &out)
	calls := 0
	g.Changes = func(ctx context.Context) (string, error) {
		calls++
		return " M fixture.go", nil
	}

	afterInvestigate := &domain.Run{Executions: []domain.StepExecution{{Action: domain.ActionInvestigate, Status: domain.StepCompleted}}}
	if _, err := g.BeforeStep(context.Background(), afterInvestigate, 1, domain.ActionImplement); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || strings.Contains(out.String(), "Changes") {
		t.Errorf("no summary expected after investigate, got %q", out.String())
	}

	afterImplement := &domain.Run{Executions: []domain.StepExecution{{Action: domain.ActionImplement, Status: domain.StepCompleted}}}
	if _, err := g.BeforeStep(context.Background(), afterImplement, 2, domain.ActionTest); err != nil {
		t.Fatal(err)
	}
	want := "Changes in the working tree:\n M fixture.go\nProtected paths: unchanged\n"
	if !strings.Contains(out.String(), want) {
		t.Errorf("expected %q in %q", want, out.String())
	}
}

func TestTerminalGate_NamesTheStepItAsksAbout(t *testing.T) {
	var out bytes.Buffer
	g := NewTerminalGate(&fakePrompter{answers: []string{"y"}}, &out)

	if _, err := g.BeforeStep(context.Background(), newRun(), 1, domain.ActionTest); err != nil {
		t.Fatal(err)
	}
	if want := "\nNext step: [2] TEST\n"; !strings.Contains(out.String(), want) {
		t.Errorf("expected %q in %q", want, out.String())
	}
}
