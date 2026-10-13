package persistence

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

func TestLoadDecision_Models(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	trace := domain.DecisionTrace{Request: "fix", Source: domain.DecisionSourceJev, Provider: "jev",
		Models: map[string]domain.ModelRoute{
			"implement": {Provider: "claude", Model: "haiku", Effort: "low", Source: "by_complexity.simple"},
			"review":    {Provider: "claude", Model: "sonnet", Source: "base"},
		}}
	if err := store.SaveDecision(run.ID, trace); err != nil {
		t.Fatal(err)
	}
	got, _, err := store.LoadDecision(run.ID)
	if err != nil || !reflect.DeepEqual(got.Models, trace.Models) {
		t.Errorf("LoadDecision models = %+v, %v; want %+v", got.Models, err, trace.Models)
	}

	// A decision.json written before the field loads, with no models.
	old := filepath.Join(store.runDir(run.ID), "decision.json")
	if err := os.WriteFile(old, []byte(`{"request":"fix"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _, err := store.LoadDecision(run.ID); err != nil || got.Models != nil {
		t.Errorf("an old decision.json = %+v, %v; want no models", got.Models, err)
	}

	for name, bad := range map[string]string{
		"unknown source": `{"request":"fix","models":{"implement":{"provider":"claude","model":"m","source":"by_complexity.trivial"}}}`,
		"unknown action": `{"request":"fix","models":{"deploy":{"provider":"claude","model":"m","source":"base"}}}`,
		"unknown field":  `{"request":"fix","models":{"implement":{"provider":"claude","model":"m","source":"base","cost":1}}}`,
	} {
		if err := os.WriteFile(old, []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.LoadDecision(run.ID); err == nil || !strings.Contains(err.Error(), "decision.json") {
			t.Errorf("%s: a bad models entry must be refused, got %v", name, err)
		}
	}
}

func TestLoadDecision_Explored(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	p, th := 0.47, 0.5
	trace := domain.DecisionTrace{Request: "fix", Source: domain.DecisionSourceJev, Provider: "jev",
		Stages: []domain.StageOutcome{{Stage: domain.StageTesting, Probability: &p, Threshold: &th, Enabled: true, Explored: true,
			Reason: "explored (P 0.47, threshold 0.50)"}}}
	if err := store.SaveDecision(run.ID, trace); err != nil {
		t.Fatal(err)
	}
	if got, _, err := store.LoadDecision(run.ID); err != nil || len(got.Stages) != 1 || !got.Stages[0].Explored {
		t.Errorf("an enabled explored stage = %+v, %v; want it to load", got.Stages, err)
	}

	path := filepath.Join(store.runDir(run.ID), "decision.json")
	if err := os.WriteFile(path, []byte(`{"request":"fix","stages":[{"stage":"testing","forced":false,"enabled":true,"reason":"p"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _, err := store.LoadDecision(run.ID); err != nil || got.Stages[0].Explored {
		t.Errorf("a trace without explored = %+v, %v; want it to load, not explored", got.Stages, err)
	}

	for name, stage := range map[string]string{
		"disabled": `{"stage":"testing","forced":false,"enabled":false,"explored":true,"reason":"explored"}`,
		"forced":   `{"stage":"testing","forced":true,"enabled":true,"explored":true,"reason":"explored"}`,
	} {
		if err := os.WriteFile(path, []byte(`{"request":"fix","stages":[`+stage+`]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := store.LoadDecision(run.ID)
		var rse *apperr.RunStateError
		if !errors.As(err, &rse) || rse.Kind != apperr.RunCorrupt {
			t.Errorf("explored on a %s stage must be RunCorrupt, got %v", name, err)
		}
	}
}

func TestLoadRun_ExploredOnADisabledStageIsCorrupt(t *testing.T) {
	store := NewStore(t.TempDir())
	run := fullRun(t, store)
	run.Decision.Stages.Stages[0].Enabled = false
	run.Decision.Stages.Stages[0].Explored = true
	if err := store.SaveRun(run); err != nil {
		t.Fatal(err)
	}
	_, err := store.LoadRun(run.ID)
	var rse *apperr.RunStateError
	if !errors.As(err, &rse) || rse.Kind != apperr.RunCorrupt {
		t.Errorf("run.json with explored on a disabled stage must be RunCorrupt, got %v", err)
	}
}

func TestLoadDecision_PolicyHash(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	hash := domain.PolicyHash(domain.DecisionPolicy{ProjectContextThreshold: 0.5})
	trace := domain.DecisionTrace{Request: "fix", Source: domain.DecisionSourceJev, Provider: "jev", PolicyHash: hash}
	if err := store.SaveDecision(run.ID, trace); err != nil {
		t.Fatal(err)
	}
	if got, _, err := store.LoadDecision(run.ID); err != nil || got.PolicyHash != hash {
		t.Errorf("LoadDecision policy_hash = %q, %v; want %q", got.PolicyHash, err, hash)
	}

	path := filepath.Join(store.runDir(run.ID), "decision.json")
	if err := os.WriteFile(path, []byte(`{"request":"fix"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _, err := store.LoadDecision(run.ID); err != nil || got.PolicyHash != "" {
		t.Errorf("a decision.json without policy_hash = %q, %v; want it to load", got.PolicyHash, err)
	}

	for name, bad := range map[string]string{
		"uppercase": strings.ToUpper(hash),
		"short":     hash[:63],
		"long":      hash + "0",
		"not hex":   strings.Repeat("g", 64),
	} {
		if err := os.WriteFile(path, []byte(`{"request":"fix","policy_hash":"`+bad+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := store.LoadDecision(run.ID)
		var rse *apperr.RunStateError
		if !errors.As(err, &rse) || rse.Kind != apperr.RunCorrupt {
			t.Errorf("%s: a bad policy_hash must be RunCorrupt, got %v", name, err)
		}
	}
}
