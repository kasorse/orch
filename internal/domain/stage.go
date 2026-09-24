package domain

import "fmt"

// Stage is one unit of work a decision provider can require for a task
// (specs/JEV integration/SPEC.md §8). Which stages run is decided by the policy
// from per-stage probabilities; in which order they run is fixed by stageOrder,
// never by the provider.
type Stage string

const (
	StageResearch    Stage = "research"
	StageAnalysis    Stage = "analysis"
	StagePlanning    Stage = "planning"
	StageDevelopment Stage = "development"
	StageTesting     Stage = "testing"
	StageReview      Stage = "review"
)

// stageOrder is the deterministic execution order (SPEC.md §13). It is also the
// complete set of supported stages.
var stageOrder = []Stage{
	StageResearch,
	StageAnalysis,
	StagePlanning,
	StageDevelopment,
	StageTesting,
	StageReview,
}

// stageActions maps each stage to the workflow action that executes it. It is a
// table rather than a switch so tests and documentation can walk it.
var stageActions = map[Stage]ActionType{
	StageResearch:    ActionResearch,
	StageAnalysis:    ActionAnalyze,
	StagePlanning:    ActionPlan,
	StageDevelopment: ActionImplement,
	StageTesting:     ActionTest,
	StageReview:      ActionReview,
}

// StageOrder returns every supported stage in execution order. The slice is a fresh
// copy, so callers may modify it.
func StageOrder() []Stage {
	return append([]Stage(nil), stageOrder...)
}

// ParseStage validates a stage name, e.g. from configuration.
func ParseStage(s string) (Stage, error) {
	st := Stage(s)
	if _, ok := stageActions[st]; !ok {
		return "", fmt.Errorf("unsupported stage %q (want one of: %s)", s, joinValues(stageOrder))
	}
	return st, nil
}

// Action returns the workflow action that executes s, or false for an unknown stage.
func (s Stage) Action() (ActionType, bool) {
	a, ok := stageActions[s]
	return a, ok
}
