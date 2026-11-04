package workflow

import (
	"slices"
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

func TestGaps(t *testing.T) {
	const devNoPlan = "development runs without an investigation; it works from the task text alone"
	cases := []struct {
		name  string
		stage []domain.Stage
		want  []string
	}{
		{"research alone", []domain.Stage{domain.StageResearch}, nil},
		{"investigation alone", []domain.Stage{domain.StageInvestigation}, nil},
		{"development alone", []domain.Stage{domain.StageDevelopment}, []string{devNoPlan}},
		{"testing alone", []domain.Stage{domain.StageTesting}, nil},
		{"review alone", []domain.Stage{domain.StageReview}, nil},
		{"testing+review", []domain.Stage{domain.StageReview, domain.StageTesting}, nil},
		{"investigation+development", []domain.Stage{domain.StageDevelopment, domain.StageInvestigation}, nil},
		{"research+development", []domain.Stage{domain.StageResearch, domain.StageDevelopment}, []string{devNoPlan}},
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
