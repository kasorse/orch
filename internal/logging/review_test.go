package logging

import (
	"strings"
	"testing"

	"orchestrator/internal/domain"
	"orchestrator/internal/tui"
)

func reviewFixture(askMode bool) *reviewState {
	p := func(f float64) *float64 { return &f }
	d := domain.Decision{Complexity: domain.ComplexityOutcome{Selected: "simple"}, Stages: domain.StageSelection{Stages: []domain.StageOutcome{
		{Stage: "research", Probability: p(0.03), Threshold: p(0.7)},
		{Stage: "analysis", Probability: p(0.15), Threshold: p(0.6)},
		{Stage: "planning", Probability: p(0.08), Threshold: p(0.65)},
		{Stage: "development", Probability: p(0.98), Threshold: p(0.6), Enabled: true},
		{Stage: "testing", Probability: p(0.85), Threshold: p(0.6), Enabled: true},
		{Stage: "review", Probability: p(0.3), Threshold: p(0.7)},
	}}}
	r := &ConsoleReporter{}
	return r.newReviewState(ReviewInput{Decision: d, AskMode: askMode, DefaultMode: domain.ModeAcceptEdits, Editable: true,
		Policy: domain.DecisionPolicy{ForceStages: map[domain.Complexity][]domain.Stage{"complex": {"analysis", "planning", "review"}}}})
}

func press(t *testing.T, st *reviewState, keys ...tui.Event) (bool, ReviewResult) {
	t.Helper()
	for i, k := range keys {
		done, res, err := st.handle(k)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			if i != len(keys)-1 {
				t.Fatalf("form closed early at key %d", i)
			}
			return true, res
		}
	}
	return false, ReviewResult{}
}

var (
	kTab   = tui.Event{Key: tui.KeyTab}
	kRight = tui.Event{Key: tui.KeyRight}
	kEnter = tui.Event{Key: tui.KeyEnter}
	kEsc   = tui.Event{Key: tui.KeyEsc}
	kSpace = tui.Event{Key: tui.KeySpace}
	kDown  = tui.Event{Key: tui.KeyDown}
)

func digit(n rune) tui.Event { return tui.Event{Key: tui.KeyRune, Rune: n} }

func TestReview_EnterApprovesUnchanged(t *testing.T) {
	st := reviewFixture(true)
	done, res := press(t, st, kEnter)
	if !done || !res.Approved || res.Decision.Edit != nil || res.Mode != domain.ModeAcceptEdits {
		t.Fatalf("got %+v", res)
	}
}

func TestReview_ToggleStageAndPickMode(t *testing.T) {
	st := reviewFixture(true)
	// 6 toggles review on; Tab to complexity, Tab to mode, Right twice: auto.
	done, res := press(t, st, digit('6'), kTab, kTab, kRight, kRight, kEnter)
	if !done || !res.Approved || res.Mode != domain.ModeAuto {
		t.Fatalf("got %+v", res)
	}
	if got := res.Decision.Stages.EnabledStages(); len(got) != 3 || got[2] != "review" {
		t.Errorf("enabled = %v", got)
	}
	if e := res.Decision.Edit; e == nil || e.DecidedComplexity != "simple" || len(e.DecidedStages) != 2 {
		t.Errorf("edit = %+v", e)
	}
}

func TestReview_ComplexityTurnsOnForcedStages(t *testing.T) {
	st := reviewFixture(false)
	done, res := press(t, st, kTab, kRight, kRight, kEnter)
	if !done || !res.Approved {
		t.Fatal("not approved")
	}
	if res.Decision.Complexity.Selected != "complex" {
		t.Fatalf("complexity = %s", res.Decision.Complexity.Selected)
	}
	got := strings.Join(stageNames(res.Decision.Stages.EnabledStages()), ",")
	if got != "analysis,planning,development,testing,review" {
		t.Errorf("enabled = %s", got)
	}
	for _, so := range res.Decision.Stages.Stages {
		if (so.Stage == "analysis" || so.Stage == "review") && !so.Forced {
			t.Errorf("%s must be marked forced", so.Stage)
		}
	}
}

func TestReview_UnforcingWarnsAndRecordsOverride(t *testing.T) {
	st := reviewFixture(false)
	press(t, st, kTab, kRight, kRight) // complex
	press(t, st, digit('2'))           // analysis off
	if !strings.Contains(st.message, "forced for complexity=complex") {
		t.Errorf("expected a warning, got %q", st.message)
	}
	_, res := press(t, st, kEnter)
	for _, so := range res.Decision.Stages.Stages {
		if so.Stage == "analysis" && (so.Enabled || !so.Forced || !strings.Contains(so.Reason, "excluded by user")) {
			t.Errorf("analysis = %+v", so)
		}
	}
}

func TestReview_EmptySelectionIsRefused(t *testing.T) {
	st := reviewFixture(false)
	done, _ := press(t, st, digit('4'), digit('5'), kEnter)
	if done || st.message != "select at least one stage" {
		t.Errorf("done=%v message=%q", done, st.message)
	}
}

func TestReview_EscAndCancelButton(t *testing.T) {
	if done, res := press(t, reviewFixture(true), kEsc); !done || res.Approved {
		t.Error("Esc must cancel")
	}
	// Tab to the buttons (stages → complexity → mode → buttons), Right to Cancel, Enter.
	if done, res := press(t, reviewFixture(true), kTab, kTab, kTab, kRight, kEnter); !done || res.Approved {
		t.Error("the Cancel button must cancel")
	}
	// Letters do nothing: no accidental cancel or approve.
	st := reviewFixture(true)
	if done, _ := press(t, st, tui.Event{Key: tui.KeyRune, Rune: 'c'}, tui.Event{Key: tui.KeyRune, Rune: 'a'}); done {
		t.Error("letters must not close the form")
	}
}

func TestReview_ArrowNavigationTogglesWithSpace(t *testing.T) {
	st := reviewFixture(false)
	_, res := press(t, st, kDown, kSpace, kEnter) // analysis on
	if got := strings.Join(stageNames(res.Decision.Stages.EnabledStages()), ","); got != "analysis,development,testing" {
		t.Errorf("enabled = %s", got)
	}
}

func TestReview_RenderFitsAndShowsPreview(t *testing.T) {
	st := reviewFixture(true)
	st.in.Build = func(sel domain.StageSelection) ([]domain.ActionType, error) {
		var out []domain.ActionType
		for _, s := range sel.EnabledStages() {
			a, _ := s.Action()
			out = append(out, a)
		}
		return out, nil
	}
	press(t, st, digit('6'))
	lines := st.render(style{on: true})
	plain := ansiRE.ReplaceAllString(strings.Join(lines, "\n"), "")
	for _, want := range []string{"[✓] review", "changed", "‹simple›", "‹accept-edits›", "workflow  implement → test → review", "[ Run ]", "[ Cancel ]"} {
		if !strings.Contains(plain, want) {
			t.Errorf("expected %q in:\n%s", want, plain)
		}
	}
	for _, l := range lines {
		if visibleWidth(l) > termWidth()-1 {
			t.Errorf("line wider than the terminal: %q", l)
		}
	}
}

// An explored stage is marked on the review screen like a forced one, and deselecting it says
// so; the edit then records it as no longer explored.
func TestReview_ExploredStageIsMarked(t *testing.T) {
	d := domain.Decision{Complexity: domain.ComplexityOutcome{Selected: "simple"}, Stages: domain.StageSelection{Stages: []domain.StageOutcome{
		{Stage: "development", Probability: ptr(0.98), Threshold: ptr(0.6), Enabled: true},
		{Stage: "review", Probability: ptr(0.67), Threshold: ptr(0.7), Enabled: true, Explored: true, Reason: "explored (P 0.67, threshold 0.70)"},
	}}}
	st := (&ConsoleReporter{}).newReviewState(ReviewInput{Decision: d, DefaultMode: domain.ModeAcceptEdits, Editable: true})
	show := func() string { return ansiRE.ReplaceAllString(strings.Join(st.render(style{on: true}), "\n"), "") }

	if got := show(); !strings.Contains(got, "explored") || strings.Contains(got, "explored · off") || strings.Contains(got, "changed") {
		t.Errorf("an explored stage that is on should read explored:\n%s", got)
	}
	press(t, st, digit('2'))
	if got := show(); !strings.Contains(got, "explored · off") {
		t.Errorf("a deselected explored stage should read explored · off:\n%s", got)
	}
	_, res := press(t, st, kEnter)
	for _, so := range res.Decision.Stages.Stages {
		if so.Stage == "review" && (so.Enabled || so.Explored) {
			t.Errorf("review = %+v, want off and no longer explored", so)
		}
	}
}

func stageNames(s []domain.Stage) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = string(x)
	}
	return out
}
