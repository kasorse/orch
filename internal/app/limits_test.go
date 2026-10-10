package app

import (
	"testing"

	"orchestrator/internal/config"
)

func TestLimitsView_StopWithoutProgress(t *testing.T) {
	yes, no := true, false
	for name, tc := range map[string]struct {
		set  *bool
		want bool
	}{
		"absent defaults to on": {nil, true},
		"true":                  {&yes, true},
		"false":                 {&no, false},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := config.Config{Limits: config.LimitsConfig{MaxSteps: 12, MaxFixIterations: 2, StopWithoutProgress: tc.set}}
			view := limitsView(cfg)
			if view.StopWithoutProgress != tc.want || view.MaxSteps != 12 || view.MaxFixIterations != 2 {
				t.Errorf("limitsView = %+v, want StopWithoutProgress %v", view, tc.want)
			}
		})
	}
}
