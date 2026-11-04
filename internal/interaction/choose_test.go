package interaction

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

var jevDown = &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionUnavailable, StatusCode: 529, Attempts: 2}

func choosePolicy() domain.DecisionPolicy {
	return domain.DecisionPolicy{ForceStages: map[domain.Complexity][]domain.Stage{
		domain.ComplexityComplex: {domain.StageInvestigation, domain.StageReview},
	}}
}

func runChooser(t *testing.T, answers ...string) (ManualChoice, error, string, *fakePrompter) {
	t.Helper()
	p := &fakePrompter{answers: answers}
	var out bytes.Buffer
	choice, err := NewChooser(p, &out, true).Choose(context.Background(), jevDown, nil, choosePolicy())
	return choice, err, out.String(), p
}

func TestChoose_ValidChoice(t *testing.T) {
	choice, err, out, _ := runChooser(t, "2", "3, 4 review")
	if err != nil {
		t.Fatal(err)
	}
	want := ManualChoice{
		Complexity: domain.ComplexityMedium,
		Stages:     []domain.Stage{domain.StageDevelopment, domain.StageTesting, domain.StageReview},
	}
	if !reflect.DeepEqual(choice, want) {
		t.Errorf("choice = %+v, want %+v", choice, want)
	}
	if !strings.Contains(out, "Decision provider failed: decision provider jev failed: unavailable") || !strings.Contains(out, "HTTP 529") {
		t.Errorf("reason not shown:\n%s", out)
	}
}

func TestChoose_InvalidInputAsksAgain(t *testing.T) {
	choice, err, out, p := runChooser(t, "4", "hard", "complex", "9", "deploy", "3")
	if err != nil {
		t.Fatal(err)
	}
	if choice.Complexity != domain.ComplexityComplex || !reflect.DeepEqual(choice.Stages, []domain.Stage{domain.StageDevelopment}) {
		t.Errorf("choice = %+v", choice)
	}
	if p.idx != 6 {
		t.Errorf("asked %d times, want 6", p.idx)
	}
	for _, want := range []string{"Please enter a number from 1 to 3.", "No stage 9", `Unknown stage "deploy"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestChoose_ForcedStagesMarkedAndIncluded(t *testing.T) {
	// Enter with nothing picked: the forced stages alone are a valid selection.
	choice, err, out, _ := runChooser(t, "3", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(choice.Stages) != 0 || len(choice.Excluded) != 0 || choice.Complexity != domain.ComplexityComplex {
		t.Errorf("choice = %+v", choice)
	}
	if !strings.Contains(out, "[2] investigation  * forced for complexity=complex") || strings.Contains(out, "[1] research  *") {
		t.Errorf("forced stages not marked:\n%s", out)
	}
}

func TestChoose_ExcludingForcedNeedsConfirmation(t *testing.T) {
	// -5 (review) confirmed, -2 (investigation) declined.
	choice, err, out, _ := runChooser(t, "3", "3 -5 -2", "y", "n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(choice.Excluded, []domain.Stage{domain.StageReview}) {
		t.Errorf("Excluded = %v, want only the confirmed review", choice.Excluded)
	}
	if !strings.Contains(out, "Warning: review is forced for complexity=complex by policy.stages.force.") {
		t.Errorf("no warning:\n%s", out)
	}
}

func TestChoose_EmptySelectionRejected(t *testing.T) {
	// Medium has no forced stages, so an empty answer selects nothing.
	choice, err, out, _ := runChooser(t, "1", "", "  , ", "2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "Select at least one stage.") != 2 || !reflect.DeepEqual(choice.Stages, []domain.Stage{domain.StageInvestigation}) {
		t.Errorf("choice = %+v\n%s", choice, out)
	}
}

func TestChoose_AllForcedExcludedIsEmpty(t *testing.T) {
	_, err, out, _ := runChooser(t, "3", "-2 -5", "y", "y", "4")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Select at least one stage.") {
		t.Errorf("excluding every forced stage with nothing picked must be rejected:\n%s", out)
	}
}

func TestChoose_EOFCancels(t *testing.T) {
	_, err, _, _ := runChooser(t, "2")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want a cancellation", err)
	}
}

func TestChoose_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &fakePrompter{answers: []string{"2", "4"}}
	_, err := NewChooser(p, &bytes.Buffer{}, true).Choose(ctx, jevDown, nil, choosePolicy())
	if !errors.Is(err, context.Canceled) || p.idx != 0 {
		t.Errorf("err = %v, asked %d times; want cancellation before any question", err, p.idx)
	}
}

func TestChoose_NonInteractiveFailsWithoutReading(t *testing.T) {
	p := &fakePrompter{answers: []string{"2", "4"}}
	var out bytes.Buffer
	_, err := NewChooser(p, &out, false).Choose(context.Background(), jevDown, nil, choosePolicy())
	if p.idx != 0 || out.Len() != 0 {
		t.Errorf("stdin read %d times, output %q; want neither", p.idx, out.String())
	}
	var derr *apperr.DecisionError
	if !errors.As(err, &derr) || derr != jevDown {
		t.Errorf("err = %v, want the provider failure wrapped", err)
	}
	if !strings.Contains(err.Error(), "without --mode auto") {
		t.Errorf("no rerun instructions: %v", err)
	}
}

func TestChoose_ShowsProjectContext(t *testing.T) {
	project := &domain.ProjectContext{
		Language: "go", ProjectType: "go-module",
		TestInfo:      domain.TestInfo{Exists: true, UnitTests: true},
		Git:           domain.GitContext{Branch: "main"},
		RelevantFiles: []domain.RelevantFile{{Path: "internal/product/repository.go"}},
	}
	var out bytes.Buffer
	_, err := NewChooser(&fakePrompter{answers: []string{"1", "3"}}, &out, true).
		Choose(context.Background(), jevDown, project, choosePolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Project context: go (go-module), unit tests, branch main", "internal/product/repository.go"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
