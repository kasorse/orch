package domain

import (
	"errors"
	"fmt"
)

// DecisionPolicy is the deterministic rule set the Policy Engine (internal/policy,
// specs/JEV integration/TASKS.md T03) applies to a decision provider's raw
// probabilities to pick a ContextDepth, a Complexity, and the enabled stages. It
// decouples the Policy Engine from config's YAML structures the same way LimitsView
// decouples the workflow engine (see workflow.go) - internal/policy may import only
// domain, so config.Config.DecisionPolicy() builds this value for it.
type DecisionPolicy struct {
	// ProjectContextThreshold: the pre-check selects ContextDepthProject when
	// P(project) is at least this.
	ProjectContextThreshold float64

	// ClarityThreshold: the task is clear enough when P(clear) is at least this
	// (policy.clarify.threshold, specs/clarify). It is part of PolicyHash.
	ClarityThreshold float64

	// ComplexityCuts split a complexity distribution's Size into levels
	// (specs/decision-shape/SPEC.md §5.2).
	ComplexityCuts ComplexityCuts

	// StageThreshold[s] is the minimum probability that enables s on its own.
	StageThreshold map[Stage]float64

	// ForceStages[c] lists stages that are enabled whenever c is the selected
	// complexity, regardless of their own probability (SPEC.md §12).
	ForceStages map[Complexity][]Stage

	// Explore, when set, turns a stage just below its threshold on for a share of runs
	// (specs/governance/SPEC.md §7.4). It changes no threshold, so it is left out of
	// PolicyHash: explored runs stay in the calibration group of the thresholds they probe.
	Explore *ExploreSettings `json:"-"`
}

// ComplexityCuts is policy.complexity.cuts: a Size at or above Complex selects complex,
// otherwise at or above Medium selects medium, otherwise simple. Config validation keeps
// 0 < Medium < Complex <= 2.
type ComplexityCuts struct {
	Medium  float64 `json:"medium"`
	Complex float64 `json:"complex"`
}

// ExploreSettings is policy.explore: Band is how far below its threshold a stage's probability
// may be to be explored, Rate the share of runs, in (0, 0.5], that explore it.
type ExploreSettings struct {
	Band float64
	Rate float64
}

// DecisionSource records who decided: the Jev API, an LLM configured as the decision
// provider (T22), or - when the provider failed - a human (TASKS.md В9: the user is
// asked by hand; there is no automatic fallback from one provider to another).
type DecisionSource string

const (
	DecisionSourceJev  DecisionSource = "jev"
	DecisionSourceLLM  DecisionSource = "llm"
	DecisionSourceUser DecisionSource = "user"
	// DecisionSourceManual: the operator named the stages up front (run --stage).
	DecisionSourceManual DecisionSource = "manual"
)

// ContextDepthOutcome is the Policy Engine's trace of the pre-check decision:
// Probability is P(project) - the only value SelectContextDepth's rule compares
// against Threshold, regardless of which depth was actually selected.
type ContextDepthOutcome struct {
	Selected    ContextDepth `json:"selected"`
	Probability float64      `json:"probability"`
	Threshold   float64      `json:"threshold"`
}

// ComplexitySourceOutcome is what one complexity question (choice or score) alone
// contributed: its raw distribution and what SelectComplexity would pick from it by
// itself.
type ComplexitySourceOutcome struct {
	Decision ComplexityDecision `json:"decision"`
	// Size is Decision.Size(), the number the cuts compare; nil in a trace written before
	// specs/decision-shape.
	Size     *float64   `json:"size,omitempty"`
	Selected Complexity `json:"selected"`
}

// ComplexityOutcome is the Policy Engine's full complexity trace. Sources holds one
// entry per question actually asked (just Primary when the shadow question was not
// asked - TASKS.md В1); Agree is true when every asked source selected the same
// complexity, which is what T21 uses to compare choice against score.
type ComplexityOutcome struct {
	Primary  ComplexitySource                             `json:"primary"`
	Sources  map[ComplexitySource]ComplexitySourceOutcome `json:"sources"`
	Selected Complexity                                   `json:"selected"`
	Agree    bool                                         `json:"agree"`
	// Cuts are the policy's cuts the sources' sizes were compared with; nil when no provider
	// answered (a manual or a user decision) and in a trace written before
	// specs/decision-shape.
	Cuts *ComplexityCuts `json:"cuts,omitempty"`
}

// StageOutcome is one stage's result from the Policy Engine: whether its probability
// alone cleared the configured threshold, whether it was forced on by the selected
// complexity, and the resulting Enabled bit workflow.Build (T04) acts on. Reason is a
// short human-readable summary of the first three fields, e.g. "p=0.74 >= 0.70" or
// "forced: complexity=complex" - exactly the trace line MVP.md §36 asks for.
// Probability and Threshold are nil when there was no provider probability to report,
// i.e. a stage selected by hand (ApplyForced, T16). Explored marks a stage that policy.explore
// turned on below its threshold; it implies Enabled and not Forced.
type StageOutcome struct {
	Stage       Stage    `json:"stage"`
	Probability *float64 `json:"probability,omitempty"`
	Threshold   *float64 `json:"threshold,omitempty"`
	Forced      bool     `json:"forced"`
	Enabled     bool     `json:"enabled"`
	Explored    bool     `json:"explored,omitempty"`
	Reason      string   `json:"reason"`
}

// StageSelection is every stage's outcome, always in StageOrder.
type StageSelection struct {
	Stages []StageOutcome `json:"stages"`
}

// EnabledStages returns every enabled stage, in StageOrder. This is what
// workflow.Build (T04) filters StageOrder by.
func (s StageSelection) EnabledStages() []Stage {
	out := make([]Stage, 0, len(s.Stages))
	for _, so := range s.Stages {
		if so.Enabled {
			out = append(out, so.Stage)
		}
	}
	return out
}

// Decision is the Policy Engine's complete output for one task: everything needed to
// answer "why did the orchestrator run review?" (MVP.md §36), and what
// classification.Service (T09) persists as decision.json (T11).
type Decision struct {
	Source       DecisionSource      `json:"source"`
	ContextDepth ContextDepthOutcome `json:"context_depth"`
	Complexity   ComplexityOutcome   `json:"complexity"`
	Stages       StageSelection      `json:"stages"`
	// Edit is set when the operator changed the complexity or the stages on the review
	// screen before approving; the provider's probabilities above are kept as answered.
	Edit *DecisionEdit `json:"edit,omitempty"`
}

// DecisionEdit records what the operator overrode on the review screen.
type DecisionEdit struct {
	// DecidedComplexity is the complexity before the edit ("" when none was decided).
	DecidedComplexity Complexity `json:"decided_complexity,omitempty"`
	// DecidedStages are the stages that were enabled before the edit.
	DecidedStages []Stage `json:"decided_stages"`
}

// WithEdit returns d with the operator's choice applied: complexity (empty keeps d's) and
// exactly the enabled stages. forced lists the stages policy forces for the resulting
// complexity; they are marked Forced, and one the operator left off is recorded as
// overridden rather than silently missing. Probabilities and thresholds are kept. When
// nothing changed, d is returned as it was, without an Edit.
func (d Decision) WithEdit(complexity Complexity, enabled, forced []Stage) Decision {
	if complexity == "" {
		complexity = d.Complexity.Selected
	}
	on := map[Stage]bool{}
	for _, s := range enabled {
		on[s] = true
	}
	isForced := map[Stage]bool{}
	for _, s := range forced {
		isForced[s] = true
	}
	before := d.Stages.EnabledStages()
	changed := complexity != d.Complexity.Selected || len(before) != len(enabled)
	for _, s := range before {
		if !on[s] {
			changed = true
		}
	}
	if !changed {
		return d
	}

	out := d
	out.Edit = &DecisionEdit{DecidedComplexity: d.Complexity.Selected, DecidedStages: before}
	out.Complexity.Selected = complexity
	out.Stages.Stages = make([]StageOutcome, len(d.Stages.Stages))
	for i, so := range d.Stages.Stages {
		was := so.Enabled
		so.Enabled = on[so.Stage]
		so.Forced = isForced[so.Stage]
		// An explored stage the operator kept stays explored; one left off or now forced is
		// no longer a stage exploration turned on.
		so.Explored = so.Explored && so.Enabled && was && !so.Forced
		switch {
		case so.Forced && !so.Enabled:
			so.Reason = fmt.Sprintf("forced: complexity=%s, excluded by user", complexity)
		case so.Enabled && !was:
			so.Reason = "selected by user"
		case !so.Enabled && was:
			so.Reason = "deselected by user"
		}
		out.Stages.Stages[i] = so
	}
	return out
}

// ManualDecision builds the Decision for stages the operator named with --stage
// (specs/stage-select/SPEC.md §5): no context, no complexity, and no probability or
// threshold, since none was asked. Duplicates collapse and the caller's order is
// irrelevant - the outcomes are always in StageOrder.
func ManualDecision(stages []Stage) (Decision, error) {
	if len(stages) == 0 {
		return Decision{}, errors.New("no stage selected")
	}
	chosen := map[Stage]bool{}
	for _, st := range stages {
		if _, ok := stageActions[st]; !ok {
			return Decision{}, fmt.Errorf("unsupported stage %q (want one of: %s)", st, joinValues(stageOrder))
		}
		chosen[st] = true
	}
	outcomes := make([]StageOutcome, 0, len(stageOrder))
	for _, st := range stageOrder {
		reason := "not selected"
		if chosen[st] {
			reason = "selected with --stage"
		}
		outcomes = append(outcomes, StageOutcome{Stage: st, Enabled: chosen[st], Reason: reason})
	}
	return Decision{
		Source:       DecisionSourceManual,
		ContextDepth: ContextDepthOutcome{Selected: ContextDepthNone},
		Stages:       StageSelection{Stages: outcomes},
	}, nil
}
