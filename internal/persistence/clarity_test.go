package persistence

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

const clarityRunID = "7c1d2e3f-4a5b-4c6d-8e7f-0a1b2c3d4e5f"

// decision.json: clarity and usage.clarity_recheck round-trip through the strict decoder.
func TestStore_DecisionClarityRoundTrips(t *testing.T) {
	base := t.TempDir()
	store := NewStore(base)
	run, err := store.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	trace := domain.DecisionTrace{
		Request: "fix", Source: domain.DecisionSourceJev, Provider: "jev",
		Clarity: &domain.ClarityTrace{Threshold: 0.8, Checks: []float64{0.42, 0.86}, Status: domain.ClarityClear, Reason: "p=0.86 >= 0.80"},
		Usage:   &domain.DecisionUsage{ClarityRecheck: []domain.Usage{{InputTokens: 30, OutputTokens: 1}}},
	}
	if err := store.SaveDecision(run.ID, trace); err != nil {
		t.Fatal(err)
	}
	got, _, err := store.LoadDecision(run.ID)
	if err != nil {
		t.Fatalf("LoadDecision: %v", err)
	}
	if !reflect.DeepEqual(got.Clarity, trace.Clarity) || !reflect.DeepEqual(got.Usage, trace.Usage) {
		t.Errorf("clarity = %+v, usage = %+v; want %+v, %+v", got.Clarity, got.Usage, trace.Clarity, trace.Usage)
	}
}

// A decision.json written before the clarity question has neither key and still loads.
func TestDecodeDecision_LoadsATraceWithoutClarity(t *testing.T) {
	body := `{"request":"fix","translation":{"detected":"en","cyrillic_share":0,"translated":false,"failed":false},` +
		`"source":"jev","provider":"jev","context_limited":false,` +
		`"complexity":{"primary":"choice","sources":{},"selected":"simple","agree":true},` +
		`"stages":[],"workflow":["implement"],"latency":{"translation_ms":0,"pre_check_ms":0,"context_ms":0,"classification_ms":0},` +
		`"usage":{"context_depth":{"input_tokens":1,"output_tokens":1}}}`
	trace, err := decodeDecision([]byte(body), clarityRunID)
	if err != nil {
		t.Fatalf("an old decision.json must load: %v", err)
	}
	if trace.Clarity != nil || trace.Usage == nil || len(trace.Usage.ClarityRecheck) != 0 {
		t.Errorf("clarity = %+v, usage = %+v; want neither", trace.Clarity, trace.Usage)
	}
}

func TestDecodeDecision_RefusesAnInvalidClarity(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"threshold zero", `{"request":"x","clarity":{"threshold":0,"checks":[0.5],"status":"clear"}}`, "threshold"},
		{"threshold above one", `{"request":"x","clarity":{"threshold":1.5,"checks":[0.5],"status":"clear"}}`, "threshold"},
		{"no checks", `{"request":"x","clarity":{"threshold":0.8,"checks":[],"status":"clear"}}`, "checks"},
		{"check above one", `{"request":"x","clarity":{"threshold":0.8,"checks":[1.2],"status":"clear"}}`, "checks[0]"},
		{"check below zero", `{"request":"x","clarity":{"threshold":0.8,"checks":[0.5,-0.1],"status":"clear"}}`, "checks[1]"},
		{"too many checks", `{"request":"x","clarity":{"threshold":0.8,"checks":[0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1,0.1],"status":"unclear"}}`, "checks"},
		{"unknown status", `{"request":"x","clarity":{"threshold":0.8,"checks":[0.5],"status":"maybe"}}`, "status"},
		{"unknown key", `{"request":"x","clarity":{"threshold":0.8,"checks":[0.5],"status":"clear","why":"x"}}`, "why"},
		{"negative recheck usage", `{"request":"x","usage":{"clarity_recheck":[{"input_tokens":-1,"output_tokens":0}]}}`, "usage.clarity_recheck[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeDecision([]byte(tc.body), clarityRunID)
			var rerr *apperr.RunStateError
			if !errors.As(err, &rerr) || rerr.Kind != apperr.RunCorrupt {
				t.Fatalf("err = %v, want a corrupt run state error", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}

	for _, status := range []string{"clear", "unclear", "disabled"} {
		body := `{"request":"x","clarity":{"threshold":0.8,"checks":[0.5],"status":"` + status + `"}}`
		if _, err := decodeDecision([]byte(body), clarityRunID); err != nil {
			t.Errorf("status %s: %v", status, err)
		}
	}
}
