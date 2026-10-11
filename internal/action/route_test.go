package action

import (
	"context"
	"testing"

	"orchestrator/internal/config"
	"orchestrator/internal/domain"
)

func routedDispatcher(client *fakeClient) *Dispatcher {
	d := newDispatcher(client)
	d.Config.Actions.Analyze.Effort = "medium"
	d.Config.Actions.Analyze.ByComplexity = map[domain.Complexity]config.ModelEffort{
		domain.ComplexitySimple: {Model: "haiku", Effort: "low"},
	}
	return d
}

func runDecided(source domain.DecisionSource, c domain.Complexity) *domain.Run {
	return &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix the bug"},
		Decision: &domain.Decision{Source: source, Complexity: domain.ComplexityOutcome{Selected: c}}}
}

func TestExecute_RoutesByDecidedComplexity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		run        *domain.Run
		model      string
		effort     string
		wantSource string
	}{
		{"simple", runDecided(domain.DecisionSourceJev, domain.ComplexitySimple), "haiku", "low", "by_complexity.simple"},
		{"level without an entry", runDecided(domain.DecisionSourceJev, domain.ComplexityComplex), "m", "medium", "base"},
		{"manual", runDecided(domain.DecisionSourceManual, ""), "m", "medium", "base"},
		{"no decision", &domain.Run{ID: "run-1"}, "m", "medium", "base"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeClient{output: `{"summary":"s"}`}
			d := routedDispatcher(client)
			outcome, err := d.Execute(context.Background(), domain.ActionAnalyze, tc.run)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if client.lastReq.Model != tc.model || client.lastReq.Effort != tc.effort {
				t.Errorf("call model/effort = %q/%q, want %q/%q", client.lastReq.Model, client.lastReq.Effort, tc.model, tc.effort)
			}
			if outcome.Provider == nil || outcome.Provider.ModelSource != tc.wantSource || outcome.Provider.Provider != "claude" {
				t.Errorf("provider ref = %+v, want source %q", outcome.Provider, tc.wantSource)
			}
		})
	}
}

// The failed call records the route as well: the run.json entry of a failed step says which
// model it was on.
func TestExecute_FailedCallRecordsModelSource(t *testing.T) {
	client := &fakeClient{err: context.DeadlineExceeded}
	d := routedDispatcher(client)
	outcome, err := d.Execute(context.Background(), domain.ActionAnalyze, runDecided(domain.DecisionSourceJev, domain.ComplexitySimple))
	if err == nil {
		t.Fatal("expected the call error")
	}
	if outcome.Provider == nil || outcome.Provider.ModelSource != "by_complexity.simple" {
		t.Errorf("provider ref = %+v", outcome.Provider)
	}
}
