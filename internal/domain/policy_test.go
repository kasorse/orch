package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestManualDecision(t *testing.T) {
	a, err := ManualDecision([]Stage{StageReview, StageTesting, StageTesting})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ManualDecision([]Stage{StageTesting, StageReview})
	if !reflect.DeepEqual(a, b) {
		t.Error("order or duplicates changed the decision")
	}
	if a.Source != DecisionSourceManual || a.ContextDepth.Selected != ContextDepthNone || a.Complexity.Selected != "" {
		t.Errorf("decision = %+v", a)
	}
	if got := a.Stages.EnabledStages(); !reflect.DeepEqual(got, []Stage{StageTesting, StageReview}) {
		t.Errorf("enabled = %v", got)
	}
	for i, o := range a.Stages.Stages {
		if o.Stage != StageOrder()[i] || o.Probability != nil || o.Threshold != nil || o.Forced {
			t.Errorf("outcome %d = %+v", i, o)
		}
		want := "not selected"
		if o.Enabled {
			want = "selected with --stage"
		}
		if o.Reason != want {
			t.Errorf("reason %d = %q", i, o.Reason)
		}
	}
	raw, _ := json.Marshal(a)
	var back Decision
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(a, back) {
		t.Errorf("round trip: %v\n%+v\n%+v", err, a, back)
	}
	if _, err := ManualDecision(nil); err == nil {
		t.Error("empty list must error")
	}
	if _, err := ManualDecision([]Stage{"bogus"}); err == nil {
		t.Error("unknown stage must error")
	}
}
