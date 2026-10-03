package domain

import (
	"slices"
	"strings"
	"testing"
)

func TestStageOrder(t *testing.T) {
	want := []Stage{StageResearch, StageAnalysis, StagePlanning, StageDevelopment, StageTesting, StageReview}
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
		StageResearch:    ActionResearch,
		StageAnalysis:    ActionAnalyze,
		StagePlanning:    ActionPlan,
		StageDevelopment: ActionImplement,
		StageTesting:     ActionTest,
		StageReview:      ActionReview,
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
		"research": StageResearch, "analysis": StageAnalysis, "analyze": StageAnalysis,
		"planning": StagePlanning, "plan": StagePlanning, "development": StageDevelopment,
		"implement": StageDevelopment, "testing": StageTesting, "test": StageTesting,
		"review": StageReview, "  ReVieW ": StageReview, "TEST": StageTesting,
	}
	for in, want := range ok {
		if got, err := ParseStageFlag(in); err != nil || got != want {
			t.Errorf("ParseStageFlag(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	_, err := ParseStageFlag("reveiw")
	if err == nil || !strings.Contains(err.Error(), `unknown stage "reveiw"`) ||
		!strings.Contains(err.Error(), "research, analysis, planning, development, testing, review") ||
		!strings.Contains(err.Error(), "aliases: analyze, plan, implement, test") {
		t.Errorf("unknown-name error = %v", err)
	}
	if _, err := ParseStage("analyze"); err == nil {
		t.Error("ParseStage must stay strict")
	}
}
