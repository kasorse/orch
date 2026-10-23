package policy

import (
	"fmt"
	"slices"

	"orchestrator/internal/domain"
)

// ForceDevelopment turns on development, and testing with it, for a task that is a code change
// by construction (specs/story/SPEC.md §11.2): a story task. Development is recorded as forced
// with reason; testing, if off, is enabled with the same reason. A review that SelectStages
// dropped only because development was off ("dropped: no code change") comes back when its own
// probability cleared its threshold or the template forces it, with the reason the policy gave
// it. Nothing else is touched: a stage already enabled keeps its reason, and so does the rest
// of the decision.
//
// A decision with development already on is returned as it was. One the operator made by hand
// (the choice offered when the decision provider failed) has no probabilities and gets the same
// forcing. dec is not modified. It runs after Explore, and again on a decision the operator
// edited on the review screen, whose edit may have switched development off.
func ForceDevelopment(dec domain.Decision, reason string) domain.Decision {
	stages := slices.Clone(dec.Stages.Stages)
	dev := -1
	for i, so := range stages {
		if so.Stage == domain.StageDevelopment {
			dev = i
		}
	}
	if dev < 0 || stages[dev].Enabled {
		return dec
	}

	stages[dev].Enabled = true
	stages[dev].Forced = true
	stages[dev].Explored = false
	stages[dev].Reason = reason
	for i, so := range stages {
		switch so.Stage {
		case domain.StageTesting:
			if !so.Enabled {
				stages[i].Enabled = true
				stages[i].Reason = reason
			}
		case domain.StageReview:
			if !so.Enabled && so.Reason == droppedNoCode && (so.Forced || cleared(so)) {
				stages[i].Enabled = true
				stages[i].Reason = restoredReason(so, dec.Complexity.Selected)
			}
		}
	}
	dec.Stages.Stages = stages
	return dec
}

// droppedNoCode is the reason SelectStages gives a testing or review stage it turned off for
// lack of development.
const droppedNoCode = "dropped: no code change"

func cleared(so domain.StageOutcome) bool {
	return so.Probability != nil && so.Threshold != nil && *so.Probability >= *so.Threshold
}

// restoredReason is the reason SelectStages would have given so with development on.
func restoredReason(so domain.StageOutcome, complexity domain.Complexity) string {
	if so.Probability != nil && so.Threshold != nil {
		return stageReason(*so.Probability, *so.Threshold, so.Forced, complexity)
	}
	return fmt.Sprintf("forced: complexity=%s", complexity)
}

// SkipPlanning turns off research, analysis and planning for a task whose plan is already
// written (a story task: its task file is the plan, specs/story/SPEC.md §11.2), with reason.
// It runs once on the decision; an operator's edit on the review screen may turn them back on.
// dec is not modified.
func SkipPlanning(dec domain.Decision, reason string) domain.Decision {
	stages := slices.Clone(dec.Stages.Stages)
	for i, so := range stages {
		switch so.Stage {
		case domain.StageResearch, domain.StageAnalysis, domain.StagePlanning:
			if so.Enabled {
				stages[i].Enabled, stages[i].Forced, stages[i].Explored, stages[i].Reason = false, false, false, reason
			}
		}
	}
	dec.Stages.Stages = stages
	return dec
}
