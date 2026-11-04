package policy

import (
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

func TestClearEnough_IsInclusive(t *testing.T) {
	pol := domain.DecisionPolicy{ClarityThreshold: 0.80}
	for _, tt := range []struct {
		p    float64
		want bool
	}{
		{0.79, false},
		{0.80, true},
		{0.81, true},
		{0, false},
		{1, true},
	} {
		if got := ClearEnough(tt.p, pol); got != tt.want {
			t.Errorf("ClearEnough(%v, threshold 0.80) = %v, want %v", tt.p, got, tt.want)
		}
	}
}

func TestClarity_StatusOfTheLastCheck(t *testing.T) {
	pol := domain.DecisionPolicy{ClarityThreshold: 0.80}

	unclear := Clarity([]float64{0.42}, pol)
	if unclear.Status != domain.ClarityUnclear || unclear.Threshold != 0.80 || unclear.Reason != "p=0.42 < 0.80" {
		t.Errorf("Clarity(0.42) = %+v", unclear)
	}

	clear := Clarity([]float64{0.42, 0.86}, pol)
	if clear.Status != domain.ClarityClear || len(clear.Checks) != 2 || clear.Reason != "p=0.86 >= 0.80" {
		t.Errorf("Clarity(0.42, 0.86) = %+v", clear)
	}
	if err := clear.Validate(); err != nil {
		t.Errorf("a trace built by Clarity must validate: %v", err)
	}
}
