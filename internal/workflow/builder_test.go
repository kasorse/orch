package workflow

import (
	"testing"

	"orchestrator/internal/domain"
)

func TestManualDecision_BuildsAndVerifies(t *testing.T) {
	for _, st := range domain.StageOrder() {
		d, err := domain.ManualDecision([]domain.Stage{st})
		if err != nil {
			t.Fatal(err)
		}
		wf, err := Build(d.Stages)
		if err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		if err := Verify(&domain.Run{Decision: &d, Workflow: &wf}); err != nil {
			t.Errorf("%s: Verify: %v", st, err)
		}
	}
	d, _ := domain.ManualDecision(domain.StageOrder())
	wf, err := Build(d.Stages)
	if err != nil || len(wf.Steps) != 6 {
		t.Errorf("all six: %v, %+v", err, wf)
	}
}
