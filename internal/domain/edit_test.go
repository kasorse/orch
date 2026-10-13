package domain

import "testing"

func TestWithEdit_NoChangeKeepsDecision(t *testing.T) {
	d := Decision{Complexity: ComplexityOutcome{Selected: ComplexitySimple}, Stages: StageSelection{Stages: []StageOutcome{
		{Stage: StageDevelopment, Enabled: true}, {Stage: StageReview}}}}
	if got := d.WithEdit("", []Stage{StageDevelopment}, nil); got.Edit != nil {
		t.Errorf("no change must not record an edit: %+v", got.Edit)
	}
}

func TestWithEdit_RecordsBeforeAndReasons(t *testing.T) {
	d := Decision{Complexity: ComplexityOutcome{Selected: ComplexitySimple}, Stages: StageSelection{Stages: []StageOutcome{
		{Stage: StageAnalysis}, {Stage: StageDevelopment, Enabled: true}, {Stage: StageReview}}}}
	got := d.WithEdit(ComplexityComplex, []Stage{StageDevelopment, StageReview}, []Stage{StageAnalysis, StageReview})
	if got.Edit == nil || got.Edit.DecidedComplexity != ComplexitySimple || len(got.Edit.DecidedStages) != 1 {
		t.Fatalf("edit = %+v", got.Edit)
	}
	if got.Complexity.Selected != ComplexityComplex {
		t.Errorf("complexity = %s", got.Complexity.Selected)
	}
	want := map[Stage]string{StageAnalysis: "forced: complexity=complex, excluded by user", StageReview: "selected by user"}
	for _, so := range got.Stages.Stages {
		if r, ok := want[so.Stage]; ok && so.Reason != r {
			t.Errorf("%s reason = %q, want %q", so.Stage, so.Reason, r)
		}
	}
	if d.Stages.Stages[2].Enabled {
		t.Error("WithEdit must not modify the original decision")
	}
}

func TestWithEdit_ExploredOnlyWhileTheStageStaysOn(t *testing.T) {
	explored := func(st Stage) StageOutcome {
		return StageOutcome{Stage: st, Enabled: true, Explored: true, Reason: "explored (P 0.47, threshold 0.50)"}
	}
	d := Decision{Complexity: ComplexityOutcome{Selected: ComplexitySimple}, Stages: StageSelection{Stages: []StageOutcome{
		{Stage: StageDevelopment, Enabled: true}, explored(StageTesting), explored(StageReview)}}}

	// Review is dropped; testing stays and keeps its mark and reason.
	got := d.WithEdit("", []Stage{StageDevelopment, StageTesting}, nil)
	byStage := map[Stage]StageOutcome{}
	for _, so := range got.Stages.Stages {
		byStage[so.Stage] = so
	}
	if so := byStage[StageReview]; so.Explored || so.Enabled || so.Reason != "deselected by user" {
		t.Errorf("deselected explored stage = %+v, want off, not explored, deselected by user", so)
	}
	if so := byStage[StageTesting]; !so.Explored || so.Reason != "explored (P 0.47, threshold 0.50)" {
		t.Errorf("kept explored stage = %+v, want it still explored with its reason", so)
	}

	// Forcing an explored stage makes it a forced one (a complexity change makes it an edit).
	got = d.WithEdit(ComplexityComplex, []Stage{StageDevelopment, StageTesting, StageReview}, []Stage{StageTesting})
	for _, so := range got.Stages.Stages {
		if so.Stage == StageTesting && (so.Explored || !so.Forced) {
			t.Errorf("forced stage = %+v, want forced and not explored", so)
		}
	}
	if !d.Stages.Stages[1].Explored {
		t.Error("WithEdit must not modify the original decision")
	}
}
