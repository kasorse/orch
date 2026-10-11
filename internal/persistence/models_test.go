package persistence

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
