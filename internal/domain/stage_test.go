package domain

import (
	"slices"
	"strings"
	"testing"
)

func TestStageOrder(t *testing.T) {
	want := []Stage{StageResearch, StageInvestigation, StageDevelopment, StageTesting, StageReview}
	if got := StageOrder(); !slices.Equal(got, want) {
		t.Fatalf("StageOrder() = %v, want %v", got, want)
	}

	// The returned slice is a copy: mutating it must not reorder the stages.
	got := StageOrder()
	got[0] = StageReview
	if StageOrder()[0] != StageResearch {
		t.Fatal("mutating StageOrder()'s result changed the stage order")
	}
}

func TestStageAction(t *testing.T) {
	want := map[Stage]ActionType{
		StageResearch:      ActionResearch,
		StageInvestigation: ActionInvestigate,
		StageDevelopment:   ActionImplement,
		StageTesting:       ActionTest,
		StageReview:        ActionReview,
	}
	for _, st := range StageOrder() {
		got, ok := st.Action()
		if !ok {
			t.Errorf("%s.Action() reported unknown stage", st)
			continue
		}
		if got != want[st] {
			t.Errorf("%s.Action() = %s, want %s", st, got, want[st])
		}
	}
	if len(want) != len(StageOrder()) {
		t.Fatalf("test covers %d stages, StageOrder has %d", len(want), len(StageOrder()))
	}

	if _, ok := Stage("deploy").Action(); ok {
		t.Error("unknown stage reported an action")
	}
}

func TestParseStage(t *testing.T) {
	for _, st := range StageOrder() {
		got, err := ParseStage(string(st))
		if err != nil || got != st {
			t.Errorf("ParseStage(%q) = %q, %v", st, got, err)
		}
	}
	for _, in := range []string{"", "Research", "develop", "answer"} {
		if _, err := ParseStage(in); err == nil {
			t.Errorf("ParseStage(%q) expected error, got nil", in)
		}
	}
}

func TestParseStageFlag(t *testing.T) {
	ok := map[string]Stage{
		"research":      StageResearch,
		"investigation": StageInvestigation,
		"investigate":   StageInvestigation,
		"development":   StageDevelopment,
		"implement":     StageDevelopment,
		"testing":       StageTesting,
		"test":          StageTesting,
		"review":        StageReview,
		"  ReVieW ":     StageReview,
		"TEST":          StageTesting,
	}
	for in, want := range ok {
		if got, err := ParseStageFlag(in); err != nil || got != want {
			t.Errorf("ParseStageFlag(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	_, err := ParseStageFlag("reveiw")
	if err == nil || !strings.Contains(err.Error(), `unknown stage "reveiw"`) ||
		!strings.Contains(err.Error(), "research, investigation, development, testing, review") ||
		!strings.Contains(err.Error(), "aliases: investigate, implement, test") {
		t.Errorf("unknown-name error = %v", err)
	}
	if _, err := ParseStage("analyze"); err == nil {
		t.Error("ParseStage must stay strict")
	}
}

// analysis, analyze, planning and plan are refused by name, with the spec that removed or
// renamed them (specs/clarify).
func TestParseStageFlag_RefusesRemovedStages(t *testing.T) {
	for _, in := range []string{"analysis", "analyze", " Analyze ", "planning", "plan", " PLAN "} {
		_, err := ParseStageFlag(in)
		if err == nil || !strings.Contains(err.Error(), "specs/clarify") || strings.Contains(err.Error(), "unknown stage") {
			t.Errorf("ParseStageFlag(%q) error = %v, want a refusal naming specs/clarify", in, err)
		}
	}
}

func TestAskedStages(t *testing.T) {
	want := []Stage{StageResearch, StageDevelopment}
	if got := AskedStages(); !slices.Equal(got, want) {
		t.Fatalf("AskedStages() = %v, want %v", got, want)
	}
}
