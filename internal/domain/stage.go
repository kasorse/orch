package domain

import (
	"fmt"
	"strings"
)

// Stage is one unit of work a decision provider can require for a task
// (specs/JEV integration/SPEC.md §8). Which stages run is decided by the policy
// from per-stage probabilities; in which order they run is fixed by stageOrder,
// never by the provider.
type Stage string

const (
	StageResearch      Stage = "research"
	StageInvestigation Stage = "investigation"
	StageDevelopment   Stage = "development"
	StageTesting       Stage = "testing"
	StageReview        Stage = "review"
)

// stageOrder is the deterministic execution order (SPEC.md §13). It is also the
// complete set of supported stages.
var stageOrder = []Stage{
	StageResearch,
	StageInvestigation,
	StageDevelopment,
	StageTesting,
	StageReview,
}

// stageActions maps each stage to the workflow action that executes it. It is a
// table rather than a switch so tests and documentation can walk it.
var stageActions = map[Stage]ActionType{
	StageResearch:      ActionResearch,
	StageInvestigation: ActionInvestigate,
	StageDevelopment:   ActionImplement,
	StageTesting:       ActionTest,
	StageReview:        ActionReview,
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

// stageAliases are the action names accepted by --stage besides the canonical stage
// names (specs/stage-select/SPEC.md §4.1). They are never persisted.
var stageAliases = map[string]Stage{
	"investigate": StageInvestigation,
	"implement":   StageDevelopment,
	"test":        StageTesting,
}

// removedStages are the stage and action names specs/clarify took out or renamed: --stage
// refuses them by name instead of reporting an unknown stage, so nobody wonders whether it was
// a typo, and planning is never mapped to investigation silently.
var removedStages = map[string]bool{"analysis": true, "analyze": true, "planning": true, "plan": true}

// ParseStageFlag parses an operator-given stage name: trimmed, case-insensitive, a
// canonical stage or an action-name alias. ParseStage stays strict because it reads
// persisted data.
func ParseStageFlag(s string) (Stage, error) {
	n := strings.ToLower(strings.TrimSpace(s))
	if st := Stage(n); stageActions[st] != "" {
		return st, nil
	}
	if st, ok := stageAliases[n]; ok {
		return st, nil
	}
	if removedStages[n] {
		return "", fmt.Errorf("stage %q was removed: analysis became the clarify step before the decision and "+
			"planning became investigation (specs/clarify); valid: %s (aliases: investigate, implement, test)",
			s, joinValues(stageOrder))
	}
	return "", fmt.Errorf("unknown stage %q; valid: %s (aliases: investigate, implement, test)", s, joinValues(stageOrder))
}

// Action returns the workflow action that executes s, or false for an unknown stage.
func (s Stage) Action() (ActionType, bool) {
	a, ok := stageActions[s]
	return a, ok
}
