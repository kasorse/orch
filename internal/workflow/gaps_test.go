package workflow

import (
	"slices"
	"testing"

	"orchestrator/internal/domain"
)

func TestGaps(t *testing.T) {
	const (
		planNoAnalysis = "planning runs without analysis; the plan will rely on the task text alone"
		devNothing     = "development runs without a plan or analysis; it works from the task text alone"
		devNoPlan      = "development runs without a plan; it works from the task text and the analysis alone"
	)
	cases := []struct {
		name  string
		stage []domain.Stage
		want  []string
	}{
		{"research alone", []domain.Stage{domain.StageResearch}, nil},
		{"analysis alone", []domain.Stage{domain.StageAnalysis}, nil},
		{"planning alone", []domain.Stage{domain.StagePlanning}, []string{planNoAnalysis}},
		{"development alone", []domain.Stage{domain.StageDevelopment}, []string{devNothing}},
		{"testing alone", []domain.Stage{domain.StageTesting}, nil},
		{"review alone", []domain.Stage{domain.StageReview}, nil},
		{"testing+review", []domain.Stage{domain.StageReview, domain.StageTesting}, nil},
		{"planning+development", []domain.Stage{domain.StageDevelopment, domain.StagePlanning}, []string{planNoAnalysis}},
		{"analysis+development", []domain.Stage{domain.StageAnalysis, domain.StageDevelopment}, []string{devNoPlan}},
		{"full chain", domain.StageOrder(), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := domain.ManualDecision(tc.stage)
			if err != nil {
				t.Fatal(err)
			}
			wf, err := Build(d.Stages)
			if err != nil {
				t.Fatal(err)
			}
			got := Gaps(wf)
			if !slices.Equal(got, tc.want) {
				t.Errorf("Gaps = %q, want %q", got, tc.want)
			}
			if !slices.Equal(got, Gaps(wf)) {
				t.Error("not deterministic")
			}
		})
	}
}
