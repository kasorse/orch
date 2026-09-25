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

func TestBuildLegacy_Question(t *testing.T) {
	w, err := BuildLegacy(domain.Classification{Type: domain.TaskQuestion, Complexity: domain.LegacyComplexityLow})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionAnswer}
	if !reflect.DeepEqual(actions(w), want) {
		t.Errorf("got %v, want %v", actions(w), want)
	}
}

func TestBuildLegacy_StandaloneTest(t *testing.T) {
	w, err := BuildLegacy(domain.Classification{Type: domain.TaskTest, Complexity: domain.LegacyComplexityLow})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionTest}
	if !reflect.DeepEqual(actions(w), want) {
		t.Errorf("got %v, want %v", actions(w), want)
	}
}

func TestBuildLegacy_StandaloneReview(t *testing.T) {
	w, err := BuildLegacy(domain.Classification{Type: domain.TaskReview, Complexity: domain.LegacyComplexityLow})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionReview}
	if !reflect.DeepEqual(actions(w), want) {
		t.Errorf("got %v, want %v", actions(w), want)
	}
}

func TestBuildLegacy_BugfixLow_NoAnalyze(t *testing.T) {
	w, err := BuildLegacy(domain.Classification{
		Type: domain.TaskBugfix, Complexity: domain.LegacyComplexityLow,
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

func TestBuildLegacy_BugfixMediumHigh_WithAnalyze(t *testing.T) {
	for _, complexity := range []domain.LegacyComplexity{domain.LegacyComplexityMedium, domain.LegacyComplexityHigh} {
		w, err := BuildLegacy(domain.Classification{
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

func TestBuildLegacy_Feature(t *testing.T) {
	w, err := BuildLegacy(domain.Classification{
		Type: domain.TaskFeature, Complexity: domain.LegacyComplexityHigh,
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

func TestBuildLegacy_Refactor(t *testing.T) {
	w, err := BuildLegacy(domain.Classification{
		Type: domain.TaskRefactor, Complexity: domain.LegacyComplexityMedium,
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

func TestBuildLegacy_RequiresFlagsPruneTrailingSteps(t *testing.T) {
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
			w, err := BuildLegacy(domain.Classification{
				Type: domain.TaskBugfix, Complexity: domain.LegacyComplexityLow,
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

func TestBuildLegacy_SameClassificationAlwaysProducesSameWorkflow(t *testing.T) {
	c := domain.Classification{Type: domain.TaskFeature, Complexity: domain.LegacyComplexityHigh, RequiresTests: true, RequiresReview: true}
	w1, _ := BuildLegacy(c)
	w2, _ := BuildLegacy(c)
	if !reflect.DeepEqual(w1, w2) {
		t.Errorf("expected deterministic output, got %v and %v", w1, w2)
	}
}

func TestBuildLegacy_UnsupportedType(t *testing.T) {
	_, err := BuildLegacy(domain.Classification{Type: "invalid"})
	if err == nil {
		t.Fatal("expected error for unsupported task type")
	}
}
