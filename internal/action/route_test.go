package action

import (
	"context"
	"testing"

	"orchestrator/internal/config"
	"orchestrator/internal/domain"
)

func routedDispatcher(client *fakeClient) *Dispatcher {
	d := newDispatcher(client)
	d.Config.Actions.Plan.Effort = "medium"
	d.Config.Actions.Plan.ByComplexity = map[domain.Complexity]config.ModelEffort{
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
			outcome, err := d.Execute(context.Background(), domain.ActionPlan, tc.run)
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
	outcome, err := d.Execute(context.Background(), domain.ActionPlan, runDecided(domain.DecisionSourceJev, domain.ComplexitySimple))
	if err == nil {
		t.Fatal("expected the call error")
	}
	if outcome.Provider == nil || outcome.Provider.ModelSource != "by_complexity.simple" {
		t.Errorf("provider ref = %+v", outcome.Provider)
	}
}

// specs/decision-shape §8: a fix after a failed test or review runs one level up, and a resume
// that repeats a failed fix still does; the first implement and --stage keep their route.
func TestRoute_EscalatesAFix(t *testing.T) {
	d := newDispatcher(&fakeClient{})
	d.Config.Actions.Implement.Escalate = true
	d.Config.Actions.Implement.ByComplexity = map[domain.Complexity]config.ModelEffort{
		domain.ComplexitySimple: {Model: "haiku"},
		domain.ComplexityMedium: {Model: "sonnet"},
	}
	history := func(actions ...domain.ActionType) []domain.StepExecution {
		out := make([]domain.StepExecution, len(actions))
		for i, a := range actions {
			out[i] = domain.StepExecution{Index: i, Action: a}
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		run    *domain.Run
		model  string
		source string
	}{
		{"first implement", runDecided(domain.DecisionSourceLLM, domain.ComplexitySimple), "haiku", "by_complexity.simple"},
		{"after a plan", withHistory(runDecided(domain.DecisionSourceLLM, domain.ComplexitySimple), history(domain.ActionPlan)), "haiku", "by_complexity.simple"},
		{"after a failed test", withHistory(runDecided(domain.DecisionSourceLLM, domain.ComplexitySimple), history(domain.ActionImplement, domain.ActionTest)), "sonnet", "escalated.medium"},
		{"after a review", withHistory(runDecided(domain.DecisionSourceLLM, domain.ComplexitySimple), history(domain.ActionImplement, domain.ActionTest, domain.ActionReview)), "sonnet", "escalated.medium"},
		{"resume repeats a failed fix", withHistory(runDecided(domain.DecisionSourceLLM, domain.ComplexitySimple), history(domain.ActionImplement, domain.ActionTest, domain.ActionImplement)), "sonnet", "escalated.medium"},
		{"no entry above medium", withHistory(runDecided(domain.DecisionSourceLLM, domain.ComplexityMedium), history(domain.ActionImplement, domain.ActionTest)), "sonnet", "by_complexity.medium"},
		{"manual", withHistory(runDecided(domain.DecisionSourceManual, ""), history(domain.ActionImplement, domain.ActionTest)), "m", "base"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pm, source := d.route(tc.run, domain.ActionImplement)
			if pm.Model != tc.model || source != tc.source {
				t.Errorf("route = %q, %q; want %q, %q", pm.Model, source, tc.model, tc.source)
			}
		})
	}
	// Only implement escalates: a review after a failed test keeps its own route.
	if _, source := d.route(withHistory(runDecided(domain.DecisionSourceLLM, domain.ComplexitySimple), history(domain.ActionImplement, domain.ActionTest)), domain.ActionReview); source != "base" {
		t.Errorf("review route source = %q, want base", source)
	}
}

func withHistory(run *domain.Run, execs []domain.StepExecution) *domain.Run {
	run.Executions = execs
	return run
}
