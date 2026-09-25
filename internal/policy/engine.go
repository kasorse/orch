// Package policy implements the deterministic Policy Engine (specs/JEV integration/
// SPEC.md §12): it turns a decision provider's raw probabilities into the enabled
// context depth, complexity, and stages the rest of the orchestrator acts on, plus the
// trace that explains why. Like internal/workflow, this package imports only
// internal/domain - "Jev provides bounded probabilistic decisions; the Orchestrator
// retains deterministic control over workflow construction" (SPEC.md §23), and this
// package is where that control lives.
package policy

import (
	"fmt"

	"orchestrator/internal/domain"
)

// SelectContextDepth decides whether a task needs project context (SPEC.md §4):
// ContextDepthProject is selected when the probability of PROJECT in d is at least
// p's ProjectContextThreshold. The comparison is inclusive, matching SelectStages'
// threshold rule.
func SelectContextDepth(d domain.ContextDepthDecision, p domain.DecisionPolicy) domain.ContextDepth {
	if d.Probabilities[domain.ContextDepthProject] >= p.ProjectContextThreshold {
		return domain.ContextDepthProject
	}
	return domain.ContextDepthNone
}

// SelectComplexity picks a complexity from d by walking the most complex option down
// (TASKS.md В2): Complex is selected when its own probability is at least
// p.ComplexityThreshold[Complex]; otherwise Medium is selected when the combined
// probability of Medium and Complex is at least p.ComplexityThreshold[Medium];
// otherwise Simple. Both comparisons are inclusive.
//
// This is deliberately not argmax: on a distribution spread thin across all three
// options, argmax could pick Simple even though Medium+Complex together carry most of
// the probability mass. Walking from the top down, and summing as it goes, means a
// near-tie is resolved toward the more complex (and so more cautious) option - an
// extra analysis pass costs less than a missed one.
func SelectComplexity(d domain.ComplexityDecision, p domain.DecisionPolicy) domain.Complexity {
	if d.Probabilities[domain.ComplexityComplex] >= p.ComplexityThreshold[domain.ComplexityComplex] {
		return domain.ComplexityComplex
	}
	mass := d.Probabilities[domain.ComplexityMedium] + d.Probabilities[domain.ComplexityComplex]
	if mass >= p.ComplexityThreshold[domain.ComplexityMedium] {
		return domain.ComplexityMedium
	}
	return domain.ComplexitySimple
}

// SelectStages decides which stages run (SPEC.md §12): a stage is enabled when its
// probability is at least its configured threshold (inclusive), or when it is listed
// in p.ForceStages[complexity] regardless of probability. If nothing clears its
// threshold and nothing is forced, analysis alone is enabled (TASKS.md В6) rather than
// returning an empty workflow.
func SelectStages(stages domain.StageDecisions, complexity domain.Complexity, p domain.DecisionPolicy) domain.StageSelection {
	forced := forcedSet(p, complexity)

	var sel domain.StageSelection
	for _, sp := range stages.All() {
		prob := sp.Decision.Probability
		threshold := p.StageThreshold[sp.Stage]
		isForced := forced[sp.Stage]
		enabled := prob >= threshold || isForced

		sel.Stages = append(sel.Stages, domain.StageOutcome{
			Stage:       sp.Stage,
			Probability: &prob,
			Threshold:   &threshold,
			Forced:      isForced,
			Enabled:     enabled,
			Reason:      stageReason(prob, threshold, isForced, complexity),
		})
	}

	if !anyEnabled(sel) {
		for i := range sel.Stages {
			if sel.Stages[i].Stage == domain.StageAnalysis {
				sel.Stages[i].Enabled = true
				sel.Stages[i].Reason = "forced: no stage cleared its threshold, defaulting to analysis"
				break
			}
		}
	}

	return sel
}

// ApplyForced merges a user's manual stage selection (T16 and T19: chosen by hand,
// either because the decision provider failed or via interactive editing) with
// p.ForceStages[complexity]: a stage is enabled if the user picked it or if it is
// forced by the selected complexity. The one exception is excluded - forced stages the
// user switched off explicitly, after a warning (T16) - which stay disabled and keep
// Forced set, so the trace shows the policy was overridden rather than silently
// missing. Unlike SelectStages, there is no provider probability to report, so every
// outcome's Probability and Threshold are nil.
//
// Unlike SelectStages, this does not apply the В6 empty-selection fallback: T16's
// interactive prompt already refuses an empty pick, so an empty result here would
// mean a caller bug, not a real "nothing qualified" case worth silently patching over.
func ApplyForced(complexity domain.Complexity, userStages, excluded []domain.Stage, p domain.DecisionPolicy) domain.StageSelection {
	picked := make(map[domain.Stage]bool, len(userStages))
	for _, s := range userStages {
		picked[s] = true
	}
	switchedOff := make(map[domain.Stage]bool, len(excluded))
	for _, s := range excluded {
		switchedOff[s] = true
	}
	forced := forcedSet(p, complexity)

	var sel domain.StageSelection
	for _, st := range domain.StageOrder() {
		isForced := forced[st]
		isPicked := picked[st]
		enabled := isPicked || isForced

		var reason string
		switch {
		case isForced && switchedOff[st]:
			reason = fmt.Sprintf("forced: complexity=%s, excluded by user", complexity)
			enabled = false
		case isForced && isPicked:
			reason = fmt.Sprintf("selected by user, also forced: complexity=%s", complexity)
		case isForced:
			reason = fmt.Sprintf("forced: complexity=%s", complexity)
		case isPicked:
			reason = "selected by user"
		default:
			reason = "not selected by user"
		}

		sel.Stages = append(sel.Stages, domain.StageOutcome{
			Stage:   st,
			Forced:  isForced,
			Enabled: enabled,
			Reason:  reason,
		})
	}
	return sel
}

// Evaluate is the Policy Engine's single entry point once both the pre-check and the
// final classification have returned (classification.Service, T09): it selects
// context depth, complexity - from primary, compared against shadow when the provider
// was asked for both (TASKS.md В1) - and stages, and returns the complete
// domain.Decision trace with Source set to the Jev API.
//
// It errors if primary is not among classification.Complexity's keys: that would mean
// the decision provider was configured to prefer a question it never actually asked,
// which is a wiring bug worth failing loudly on rather than silently defaulting.
func Evaluate(
	depth domain.ContextDepthDecision,
	classification domain.TaskClassification,
	primary domain.ComplexitySource,
	p domain.DecisionPolicy,
) (domain.Decision, error) {
	if _, ok := classification.Complexity[primary]; !ok {
		return domain.Decision{}, fmt.Errorf(
			"policy: primary complexity source %q was not asked (classification has: %v)",
			primary, complexitySourceKeys(classification.Complexity),
		)
	}

	depthOutcome := domain.ContextDepthOutcome{
		Selected:    SelectContextDepth(depth, p),
		Probability: depth.Probabilities[domain.ContextDepthProject],
		Threshold:   p.ProjectContextThreshold,
	}

	complexityOutcome := evaluateComplexity(classification.Complexity, primary, p)
	stageSelection := SelectStages(classification.Stages, complexityOutcome.Selected, p)

	return domain.Decision{
		Source:       domain.DecisionSourceJev,
		ContextDepth: depthOutcome,
		Complexity:   complexityOutcome,
		Stages:       stageSelection,
	}, nil
}

func evaluateComplexity(
	dists map[domain.ComplexitySource]domain.ComplexityDecision,
	primary domain.ComplexitySource,
	p domain.DecisionPolicy,
) domain.ComplexityOutcome {
	sources := make(map[domain.ComplexitySource]domain.ComplexitySourceOutcome, len(dists))
	for src, d := range dists {
		sources[src] = domain.ComplexitySourceOutcome{Decision: d, Selected: SelectComplexity(d, p)}
	}

	selected := sources[primary].Selected

	agree := true
	for _, so := range sources {
		if so.Selected != selected {
			agree = false
			break
		}
	}

	return domain.ComplexityOutcome{
		Primary:  primary,
		Sources:  sources,
		Selected: selected,
		Agree:    agree,
	}
}

func forcedSet(p domain.DecisionPolicy, complexity domain.Complexity) map[domain.Stage]bool {
	set := make(map[domain.Stage]bool, len(p.ForceStages[complexity]))
	for _, s := range p.ForceStages[complexity] {
		set[s] = true
	}
	return set
}

func anyEnabled(sel domain.StageSelection) bool {
	for _, s := range sel.Stages {
		if s.Enabled {
			return true
		}
	}
	return false
}

// stageReason renders the short trace line MVP.md §36 asks for, e.g.
// "p=0.74 >= 0.70" or "forced: complexity=complex". When a stage clears its threshold
// and is also forced, both facts are reported.
func stageReason(prob, threshold float64, forced bool, complexity domain.Complexity) string {
	cleared := prob >= threshold
	switch {
	case forced && cleared:
		return fmt.Sprintf("p=%.2f >= %.2f, also forced: complexity=%s", prob, threshold, complexity)
	case forced:
		return fmt.Sprintf("forced: complexity=%s", complexity)
	case cleared:
		return fmt.Sprintf("p=%.2f >= %.2f", prob, threshold)
	default:
		return fmt.Sprintf("p=%.2f < %.2f", prob, threshold)
	}
}

func complexitySourceKeys(m map[domain.ComplexitySource]domain.ComplexityDecision) []domain.ComplexitySource {
	out := make([]domain.ComplexitySource, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
