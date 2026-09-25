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
