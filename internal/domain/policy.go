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

	// ComplexityThreshold[c] is the minimum cumulative probability of c and every more
	// complex option that selects c or higher (TASKS.md В2: walked from the top down,
	// so ComplexityComplex is checked before ComplexityMedium). ComplexitySimple has
	// no entry - it is the default when neither Medium's nor Complex's threshold is
	// met.
	ComplexityThreshold map[Complexity]float64

	// StageThreshold[s] is the minimum probability that enables s on its own.
	StageThreshold map[Stage]float64

	// ForceStages[c] lists stages that are enabled whenever c is the selected
	// complexity, regardless of their own probability (SPEC.md §12).
	ForceStages map[Complexity][]Stage
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
	Selected Complexity         `json:"selected"`
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
}

// StageOutcome is one stage's result from the Policy Engine: whether its probability
// alone cleared the configured threshold, whether it was forced on by the selected
// complexity, and the resulting Enabled bit workflow.Build (T04) acts on. Reason is a
// short human-readable summary of the first three fields, e.g. "p=0.74 >= 0.70" or
// "forced: complexity=complex" - exactly the trace line MVP.md §36 asks for.
// Probability and Threshold are nil when there was no provider probability to report,
// i.e. a stage selected by hand (ApplyForced, T16).
type StageOutcome struct {
	Stage       Stage    `json:"stage"`
	Probability *float64 `json:"probability,omitempty"`
	Threshold   *float64 `json:"threshold,omitempty"`
	Forced      bool     `json:"forced"`
	Enabled     bool     `json:"enabled"`
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
