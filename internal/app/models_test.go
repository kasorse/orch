package app

import (
	"reflect"
	"testing"

	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

func routedConfig() config.Config {
	pm := func(model string) config.ActionConfig {
		return config.ActionConfig{ProviderModel: config.ProviderModel{Provider: "claude", Model: model}}
	}
	cfg := config.Config{Actions: config.ActionsConfig{
		Research: pm("sonnet"), Analyze: pm("sonnet"), Plan: pm("sonnet"), Implement: pm("sonnet"), Review: pm("sonnet"),
	}}
	cfg.Actions.Implement.Effort = "medium"
	cfg.Actions.Implement.ByComplexity = map[domain.Complexity]config.ModelEffort{
		domain.ComplexitySimple:  {Model: "haiku", Effort: "low"},
		domain.ComplexityComplex: {Model: "opus", Effort: "high"},
	}
	return cfg
}

func buildWorkflow(t *testing.T, stages ...domain.Stage) domain.Workflow {
	t.Helper()
	d, err := domain.ManualDecision(stages)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.Build(d.Stages)
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestModelRoutes(t *testing.T) {
	wf := buildWorkflow(t, domain.StageDevelopment, domain.StageTesting, domain.StageReview)
	decided := domain.Decision{Source: domain.DecisionSourceJev, Complexity: domain.ComplexityOutcome{Selected: domain.ComplexitySimple}}

	got := modelRoutes(routedConfig(), decided, wf)
	want := map[string]domain.ModelRoute{
		"implement": {Provider: "claude", Model: "haiku", Effort: "low", Source: "by_complexity.simple"},
		"review":    {Provider: "claude", Model: "sonnet", Source: "base"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("simple models = %+v, want %+v", got, want)
	}

	// An edit on the review screen changes the complexity, and so the route.
	edited := decided.WithEdit(domain.ComplexityComplex, []domain.Stage{domain.StageDevelopment}, nil)
	if got := modelRoutes(routedConfig(), edited, wf)["implement"]; got.Model != "opus" || got.Source != "by_complexity.complex" {
		t.Errorf("edited implement route = %+v", got)
	}

	manual, err := domain.ManualDecision([]domain.Stage{domain.StageDevelopment, domain.StageReview})
	if err != nil {
		t.Fatal(err)
	}
	got = modelRoutes(routedConfig(), manual, wf)
	if r := got["implement"]; r.Model != "sonnet" || r.Effort != "medium" || r.Source != "base" {
		t.Errorf("manual implement route = %+v, want the base", r)
	}
	for action, r := range got {
		if r.Source != "base" {
			t.Errorf("manual %s source = %q, want base", action, r.Source)
		}
	}
}

func TestModelChangeWarnings(t *testing.T) {
	wf := buildWorkflow(t, domain.StageDevelopment, domain.StageReview)
	d := domain.Decision{Source: domain.DecisionSourceJev, Complexity: domain.ComplexityOutcome{Selected: domain.ComplexitySimple}}
	recorded := modelRoutes(routedConfig(), d, wf)
	remaining := []domain.ActionType{domain.ActionImplement, domain.ActionTest, domain.ActionReview}

	if got := modelChangeWarnings(routedConfig(), &d, recorded, remaining); len(got) != 0 {
		t.Errorf("unchanged config warned: %v", got)
	}

	changed := routedConfig()
	changed.Actions.Implement.ByComplexity[domain.ComplexitySimple] = config.ModelEffort{Model: "sonnet", Effort: "low"}
	got := modelChangeWarnings(changed, &d, recorded, remaining)
	if want := []string{"implement model changed since the run started: haiku → sonnet"}; !reflect.DeepEqual(got, want) {
		t.Errorf("by_complexity change = %q, want %q", got, want)
	}

	// Any model change warns, not only one made through by_complexity.
	rebased := routedConfig()
	rebased.Actions.Review.Model = "opus"
	rebased.Actions.Review.Effort = "high"
	got = modelChangeWarnings(rebased, &d, recorded, remaining)
	want := []string{
		"review model changed since the run started: sonnet → opus",
		"review effort changed since the run started: (default) → high",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("base change = %q, want %q", got, want)
	}

	// Only the actions still to run are compared.
	if got := modelChangeWarnings(rebased, &d, recorded, []domain.ActionType{domain.ActionImplement}); len(got) != 0 {
		t.Errorf("a finished action warned: %v", got)
	}

	// A run written before decision.json had models, or without a decision, gets none.
	if got := modelChangeWarnings(rebased, &d, nil, remaining); got != nil {
		t.Errorf("old run warned: %v", got)
	}
	if got := modelChangeWarnings(rebased, nil, recorded, remaining); got != nil {
		t.Errorf("run without a decision warned: %v", got)
	}
}
