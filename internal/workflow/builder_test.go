package workflow

import (
	"reflect"
	"testing"

	"orchestrator/internal/domain"
)

func actions(w domain.Workflow) []domain.ActionType {
	out := make([]domain.ActionType, len(w.Steps))
	for i, s := range w.Steps {
		out[i] = s.Action
	}
	return out
}

func TestBuild_Question(t *testing.T) {
	w, err := Build(domain.Classification{Type: domain.TaskQuestion, Complexity: domain.ComplexityLow})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionAnswer}
	if !reflect.DeepEqual(actions(w), want) {
		t.Errorf("got %v, want %v", actions(w), want)
	}
}

func TestBuild_StandaloneTest(t *testing.T) {
	w, err := Build(domain.Classification{Type: domain.TaskTest, Complexity: domain.ComplexityLow})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionTest}
	if !reflect.DeepEqual(actions(w), want) {
		t.Errorf("got %v, want %v", actions(w), want)
	}
}

func TestBuild_StandaloneReview(t *testing.T) {
	w, err := Build(domain.Classification{Type: domain.TaskReview, Complexity: domain.ComplexityLow})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionReview}
	if !reflect.DeepEqual(actions(w), want) {
		t.Errorf("got %v, want %v", actions(w), want)
	}
}

func TestBuild_BugfixLow_NoAnalyze(t *testing.T) {
	w, err := Build(domain.Classification{
		Type: domain.TaskBugfix, Complexity: domain.ComplexityLow,
		RequiresTests: true, RequiresReview: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionImplement, domain.ActionTest, domain.ActionReview}
	if !reflect.DeepEqual(actions(w), want) {
		t.Errorf("got %v, want %v", actions(w), want)
	}
}

func TestBuild_BugfixMediumHigh_WithAnalyze(t *testing.T) {
	for _, complexity := range []domain.Complexity{domain.ComplexityMedium, domain.ComplexityHigh} {
		w, err := Build(domain.Classification{
			Type: domain.TaskBugfix, Complexity: complexity,
			RequiresTests: true, RequiresReview: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []domain.ActionType{domain.ActionAnalyze, domain.ActionImplement, domain.ActionTest, domain.ActionReview}
		if !reflect.DeepEqual(actions(w), want) {
			t.Errorf("complexity %s: got %v, want %v", complexity, actions(w), want)
		}
	}
}

func TestBuild_Feature(t *testing.T) {
	w, err := Build(domain.Classification{
		Type: domain.TaskFeature, Complexity: domain.ComplexityHigh,
		RequiresTests: true, RequiresReview: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionPlan, domain.ActionImplement, domain.ActionTest, domain.ActionReview}
	if !reflect.DeepEqual(actions(w), want) {
		t.Errorf("got %v, want %v", actions(w), want)
	}
}

func TestBuild_Refactor(t *testing.T) {
	w, err := Build(domain.Classification{
		Type: domain.TaskRefactor, Complexity: domain.ComplexityMedium,
		RequiresTests: true, RequiresReview: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionAnalyze, domain.ActionImplement, domain.ActionTest, domain.ActionReview}
	if !reflect.DeepEqual(actions(w), want) {
		t.Errorf("got %v, want %v", actions(w), want)
	}
}

func TestBuild_RequiresFlagsPruneTrailingSteps(t *testing.T) {
	cases := []struct {
		name           string
		requiresTests  bool
		requiresReview bool
		want           []domain.ActionType
	}{
		{"neither", false, false, []domain.ActionType{domain.ActionImplement}},
		{"testsOnly", true, false, []domain.ActionType{domain.ActionImplement, domain.ActionTest}},
		{"reviewOnly", false, true, []domain.ActionType{domain.ActionImplement, domain.ActionReview}},
		{"both", true, true, []domain.ActionType{domain.ActionImplement, domain.ActionTest, domain.ActionReview}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := Build(domain.Classification{
				Type: domain.TaskBugfix, Complexity: domain.ComplexityLow,
				RequiresTests: tc.requiresTests, RequiresReview: tc.requiresReview,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(actions(w), tc.want) {
				t.Errorf("got %v, want %v", actions(w), tc.want)
			}
		})
	}
}

func TestBuild_SameClassificationAlwaysProducesSameWorkflow(t *testing.T) {
	c := domain.Classification{Type: domain.TaskFeature, Complexity: domain.ComplexityHigh, RequiresTests: true, RequiresReview: true}
	w1, _ := Build(c)
	w2, _ := Build(c)
	if !reflect.DeepEqual(w1, w2) {
		t.Errorf("expected deterministic output, got %v and %v", w1, w2)
	}
}

func TestBuild_UnsupportedType(t *testing.T) {
	_, err := Build(domain.Classification{Type: "invalid"})
	if err == nil {
		t.Fatal("expected error for unsupported task type")
	}
}
