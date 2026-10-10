package domain

// DecisionTrace is decision.json (TASKS.md T11): everything needed to reconstruct why a
// run got its workflow - what was sent, what the provider answered, what the policy made
// of it, and how long each step took. It is written once, after classification.
type DecisionTrace struct {
	Request string `json:"request"`
	// RequestEN is the text sent to the decision provider when it differs from Request.
	RequestEN   string           `json:"request_en,omitempty"`
	Translation TranslationTrace `json:"translation"`

	Source DecisionSource `json:"source"`
	// DecisionError is the provider failure that led to a manual choice (source user).
	DecisionError string `json:"decision_error,omitempty"`
	// Provider is the decision provider asked; Model the exact version that answered
	// (empty when the final classification never returned).
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`

	// ContextDepth is nil when the pre-check itself failed.
	ContextDepth   *ContextDepthTrace `json:"context_depth,omitempty"`
	ContextLimited bool               `json:"context_limited"`
	ContextError   string             `json:"context_error,omitempty"`

	Complexity ComplexityOutcome `json:"complexity"`
	Stages     []StageOutcome    `json:"stages"`
	Workflow   []ActionType      `json:"workflow"`
	Latency    DecisionLatency   `json:"latency"`
	// Usage is what each decision call cost; nil when no call reported any (Jev's mock,
	// --stage, a provider that reports nothing).
	Usage *DecisionUsage `json:"usage,omitempty"`
	// Edit is the operator's override on the review screen, if any (Decision.Edit).
	Edit *DecisionEdit `json:"edit,omitempty"`
	// Models is the route each action in the workflow gets under this decision, keyed by
	// action (specs/quality/SPEC.md §9.3). Absent in a trace written before by_complexity.
	Models map[string]ModelRoute `json:"models,omitempty"`
}

// ModelRoute is the provider, model and effort one action runs with, and where the model
// came from (ProviderRef.ModelSource).
type ModelRoute struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort,omitempty"`
	Source   string `json:"source"`
}

// TranslationTrace records the language step (T15).
type TranslationTrace struct {
	Detected      string  `json:"detected"`
	CyrillicShare float64 `json:"cyrillic_share"`
	Translated    bool    `json:"translated"`
	Failed        bool    `json:"failed"`
	Reason        string  `json:"reason,omitempty"`
	Provider      string  `json:"provider,omitempty"`
	Model         string  `json:"model,omitempty"`
	LatencyMS     int64   `json:"latency_ms,omitempty"`
}

// ContextDepthTrace is the pre-check: the provider's distribution and the policy's pick.
type ContextDepthTrace struct {
	Probabilities map[ContextDepth]float64 `json:"probabilities"`
	Confidence    float64                  `json:"confidence"`
	ContextDepthOutcome
}

// DecisionLatency is each classification step's duration in milliseconds; a step that
// did not run is 0. ManualChoiceMS is the user's thinking time, not the provider's.
type DecisionLatency struct {
	TranslationMS    int64 `json:"translation_ms"`
	PreCheckMS       int64 `json:"pre_check_ms"`
	ContextMS        int64 `json:"context_ms"`
	ClassificationMS int64 `json:"classification_ms"`
	ManualChoiceMS   int64 `json:"manual_choice_ms,omitempty"`
}

// DecisionUsage is each decision round's provider call usage; a round that did not run,
// or whose provider reported nothing, is nil. Jev reports tokens and no cost.
type DecisionUsage struct {
	Translation    *Usage `json:"translation,omitempty"`
	ContextDepth   *Usage `json:"context_depth,omitempty"`
	Classification *Usage `json:"classification,omitempty"`
}

// Empty reports whether no round recorded usage.
func (u DecisionUsage) Empty() bool {
	return u.Translation == nil && u.ContextDepth == nil && u.Classification == nil
}

// ManualTrace is decision.json for stages the operator named with --stage
// (specs/stage-select/SPEC.md §5): no provider, model, context, complexity or latency,
// because nothing was asked.
func ManualTrace(request string, d Decision, wf Workflow) DecisionTrace {
	actions := make([]ActionType, len(wf.Steps))
	for i, s := range wf.Steps {
		actions[i] = s.Action
	}
	return DecisionTrace{
		Request:     request,
		Translation: TranslationTrace{},
		Source:      d.Source,
		Stages:      d.Stages.Stages,
		Workflow:    actions,
	}
}
