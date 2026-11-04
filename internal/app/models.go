package app

import (
	"fmt"

	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
)

// modelRoutes is decision.json's models: for each action of the workflow that makes a
// provider call, the provider, model, effort and source the decision implies under cfg
// (specs/quality/SPEC.md §9.3). A manual decision gets the base for every action. domain
// cannot import config, so the trace is completed here.
func modelRoutes(cfg config.Config, d domain.Decision, wf domain.Workflow) map[string]domain.ModelRoute {
	models := map[string]domain.ModelRoute{}
	for _, step := range wf.Steps {
		if r, ok := routeOf(cfg, step.Action, &d); ok {
			models[string(step.Action)] = r
		}
	}
	return models
}

func routeOf(cfg config.Config, action domain.ActionType, d *domain.Decision) (domain.ModelRoute, bool) {
	pm, source, ok := cfg.Actions.Route(action, d)
	if !ok {
		return domain.ModelRoute{}, false
	}
	return domain.ModelRoute{Provider: pm.Provider, Model: pm.Model, Effort: pm.Effort, Source: source}, true
}

// modelChangeWarnings compares the route each remaining action gets from the persisted
// decision and the current config with the one decision.json recorded when the run started,
// and names every model or effort that differs - whatever the cause, a changed
// by_complexity table or a changed base model. A run written before decision.json had
// models (nil) gets none.
func modelChangeWarnings(cfg config.Config, d *domain.Decision, recorded map[string]domain.ModelRoute, remaining []domain.ActionType) []string {
	if recorded == nil || d == nil {
		return nil
	}
	var warnings []string
	seen := map[domain.ActionType]bool{}
	for _, action := range remaining {
		if seen[action] {
			continue
		}
		seen[action] = true
		was, ok := recorded[string(action)]
		if !ok {
			continue
		}
		now, ok := routeOf(cfg, action, d)
		if !ok {
			continue
		}
		if now.Model != was.Model {
			warnings = append(warnings, fmt.Sprintf("%s model changed since the run started: %s → %s", action, was.Model, now.Model))
		}
		if now.Effort != was.Effort {
			warnings = append(warnings, fmt.Sprintf("%s effort changed since the run started: %s → %s", action, orDefault(was.Effort), orDefault(now.Effort)))
		}
	}
	return warnings
}

// orDefault names an unset effort: the CLI's own default applies.
func orDefault(effort string) string {
	if effort == "" {
		return "(default)"
	}
	return effort
}
