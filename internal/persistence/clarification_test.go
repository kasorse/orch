package persistence

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

func validClarification() *domain.Clarification {
	return &domain.Clarification{
		Threshold: 0.8, Checks: []float64{0.42, 0.86}, Status: domain.ClarityClear,
		Rounds: []domain.ClarifyRound{{
			Kind: domain.ClarifyRoundClarify, Mode: domain.ClarifyInteractive,
			Points: []domain.ClarifyPoint{
				{Unclear: "format", Question: "Which format?", Answer: "CSV", By: domain.ClarifyByDocs, Source: "docs/export.md#Formats"},
				{Unclear: "users", Question: "Which users?", Answer: "admins only", By: domain.ClarifyByOperator},
				{Unclear: "locale", Question: "Which locale?"},
			},
		}},
	}
}

// run.json: task.clarification round-trips through the strict decoder, and a run without it loads.
func TestStore_TaskClarificationRoundTrips(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "add an export"})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRun(run.ID)
	if err != nil || loaded.Task.Clarification != nil {
		t.Fatalf("a run without a clarification: %v, %+v", err, loaded.Task.Clarification)
	}

	run.Task.Clarification = validClarification()
	if err := store.SaveRun(run); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.LoadRun(run.ID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if !reflect.DeepEqual(loaded.Task.Clarification, run.Task.Clarification) {
		t.Errorf("clarification = %+v, want %+v", loaded.Task.Clarification, run.Task.Clarification)
	}
}

func TestStore_RefusesAnInvalidTaskClarification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(c *domain.Clarification)
		want   string
	}{
		{"threshold zero", func(c *domain.Clarification) { c.Threshold = 0 }, "threshold"},
		{"check above one", func(c *domain.Clarification) { c.Checks[1] = 1.5 }, "checks[1]"},
		{"checks do not match the rounds", func(c *domain.Clarification) { c.Checks = c.Checks[:1] }, "one more check than rounds"},
		{"unknown status", func(c *domain.Clarification) { c.Status = "maybe" }, "status"},
		{"first round is an addition", func(c *domain.Clarification) {
			c.Rounds[0] = domain.ClarifyRound{Kind: domain.ClarifyRoundAddition, Text: "x"}
		}, "rounds[0]"},
		{"unknown mode", func(c *domain.Clarification) { c.Rounds[0].Mode = "chat" }, "mode"},
		{"unknown by", func(c *domain.Clarification) { c.Rounds[0].Points[0].By = "model" }, "by"},
		{"assumption in an interactive round", func(c *domain.Clarification) {
			c.Rounds[0].Points[1].By = domain.ClarifyByAssumption
		}, "assumption"},
		{"answer too long", func(c *domain.Clarification) {
			c.Rounds[0].Points[0].Answer = strings.Repeat("x", domain.MaxClarifyFieldBytes+1)
		}, "answer"},
		{"open point with an answer", func(c *domain.Clarification) { c.Rounds[0].Points[2].Answer = "x" }, "open point"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore(t.TempDir())
			run, err := store.NewRun(domain.Task{Description: "add an export"})
			if err != nil {
				t.Fatal(err)
			}
			c := validClarification()
			tc.mutate(c)
			run.Task.Clarification = c
			if err := store.SaveRun(run); err != nil {
				t.Fatal(err)
			}
			_, err = store.LoadRun(run.ID)
			var rerr *apperr.RunStateError
			if !errors.As(err, &rerr) || rerr.Kind != apperr.RunCorrupt {
				t.Fatalf("err = %v, want a corrupt run state error", err)
			}
			if !strings.Contains(err.Error(), "task.clarification") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name task.clarification and %q", err, tc.want)
			}
		})
	}
}

func TestStore_RefusesAnUnknownKeyInTaskClarification(t *testing.T) {
	body := `{"id":"` + clarityRunID + `","task":{"description":"x","clarification":{"threshold":0.8,"checks":[0.5],"status":"clear","rounds":[],"why":"x"},"created_at":"2026-01-01T00:00:00Z"},"status":"pending"}`
	_, err := decodeRun([]byte(body), clarityRunID)
	var rerr *apperr.RunStateError
	if !errors.As(err, &rerr) || rerr.Kind != apperr.RunCorrupt || !strings.Contains(err.Error(), "why") {
		t.Errorf("err = %v, want a corrupt run naming the unknown key", err)
	}
}

// decision.json: usage.clarify round-trips, and more than one call is refused.
func TestStore_DecisionClarifyUsage(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	trace := domain.DecisionTrace{
		Request: "fix", Source: domain.DecisionSourceJev, Provider: "jev",
		Usage: &domain.DecisionUsage{
			Clarify:              []domain.Usage{{InputTokens: 300, OutputTokens: 40}},
			TranslationClarified: &domain.Usage{InputTokens: 20, OutputTokens: 30},
		},
	}
	if err := store.SaveDecision(run.ID, trace); err != nil {
		t.Fatal(err)
	}
	got, _, err := store.LoadDecision(run.ID)
	if err != nil || !reflect.DeepEqual(got.Usage, trace.Usage) {
		t.Fatalf("usage = %+v, err %v; want %+v", got.Usage, err, trace.Usage)
	}

	for name, body := range map[string]string{
		"two calls":      `{"request":"x","usage":{"clarify":[{"input_tokens":1,"output_tokens":1},{"input_tokens":1,"output_tokens":1}]}}`,
		"negative usage": `{"request":"x","usage":{"clarify":[{"input_tokens":-1,"output_tokens":0}]}}`,
	} {
		if _, err := decodeDecision([]byte(body), clarityRunID); err == nil || !strings.Contains(err.Error(), "usage.clarify") {
			t.Errorf("%s: err = %v, want a refusal naming usage.clarify", name, err)
		}
	}
	body := `{"request":"x","usage":{"translation_clarified":{"input_tokens":-1,"output_tokens":0}}}`
	if _, err := decodeDecision([]byte(body), clarityRunID); err == nil || !strings.Contains(err.Error(), "usage.translation_clarified") {
		t.Errorf("err = %v, want a refusal naming usage.translation_clarified", err)
	}
}

func TestStore_SaveClarifyArtifact(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	rel, err := store.SaveClarifyArtifact(run.ID, 1, map[string]any{"points": []string{}}, "ACTION: clarify\n<<<TASK")
	if err != nil {
		t.Fatal(err)
	}
	if rel != filepath.Join("artifacts", "clarify-1.json") {
		t.Errorf("path = %q", rel)
	}
	data, err := os.ReadFile(filepath.Join(store.RunDir(run.ID), rel))
	if err != nil {
		t.Fatal(err)
	}
	// The prompt is the last field, written as it was sent (no HTML escaping).
	if !strings.Contains(string(data), `"prompt": "ACTION: clarify\n<<<TASK"`) {
		t.Errorf("artifact:\n%s", data)
	}
	if _, err := store.SaveClarifyArtifact("not-a-run", 1, map[string]any{}, ""); err == nil {
		t.Error("a path must not be built from something that is not a run id")
	}
}
