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
