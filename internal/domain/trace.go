package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
)

// PolicyHash is the lowercase hex sha256 of the JSON of p. encoding/json writes map keys in
// sorted order, so the order a policy's maps were filled in does not change the hash; a
// renamed or added DecisionPolicy field does, and starts a new group of runs in the reports
// (Explore is the exception: it has no JSON name, because it changes no threshold).
// It is empty only for a policy that cannot be encoded (a NaN or infinite threshold, which
// config validation never lets through), so such a decision is recorded without a hash
// rather than stopping the run.
func PolicyHash(p DecisionPolicy) string {
	data, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

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
	// PolicyHash is PolicyHash of the DecisionPolicy the decision used, so runs before and
	// after a threshold change are told apart (specs/governance/SPEC.md §7.2). Absent for a
	// manual decision and in a trace written before the field existed.
	PolicyHash string `json:"policy_hash,omitempty"`
	// QuestionsHash is decision.QuestionsHash() of the questions the provider was asked:
	// sizes from different wordings are not comparable (specs/decision-shape/SPEC.md §5.3).
	// Absent for a manual decision and in a trace written before the field existed.
	QuestionsHash string `json:"questions_hash,omitempty"`

	// ContextDepth is nil when the pre-check itself failed.
	ContextDepth   *ContextDepthTrace `json:"context_depth,omitempty"`
	ContextLimited bool               `json:"context_limited"`
	ContextError   string             `json:"context_error,omitempty"`
	// Clarity is the clarity question's answers and what Go made of them (specs/clarify). It is
	// recorded and decides nothing yet. Absent after a failed pre-check, for a story task and in
	// a trace written before the question existed.
	Clarity *ClarityTrace `json:"clarity,omitempty"`

	Complexity ComplexityOutcome `json:"complexity"`
	Stages     []StageOutcome    `json:"stages"`
	// Scope is the provider's answer to the shadow scope question (specs/story/SPEC.md §13):
	// recorded for a later calibration, read by nothing. Absent for a manual decision, a
	// story task, a failed classification and in a trace written before the question existed.
	Scope    *ScopeDecision  `json:"scope,omitempty"`
	Workflow []ActionType    `json:"workflow"`
	Latency  DecisionLatency `json:"latency"`
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

// ClarityStatus is how the clarity checks of a run ended.
type ClarityStatus string

const (
	// ClarityClear: the last check reached the threshold.
	ClarityClear ClarityStatus = "clear"
	// ClarityUnclear: the last check stayed below the threshold.
	ClarityUnclear ClarityStatus = "unclear"
	// ClarityDisabled: clarify.enabled is false; the question is asked and recorded, and the
	// run goes on whatever P(clear) is.
	ClarityDisabled ClarityStatus = "disabled"
)

// Valid reports whether s is a status decision.json may record.
func (s ClarityStatus) Valid() bool {
	return s == ClarityClear || s == ClarityUnclear || s == ClarityDisabled
}

// ClarityTrace is decision.json: clarity (specs/clarify). Checks holds P(clear) of the
// pre-check and then of every re-check, in order; Threshold is policy.clarify.threshold. Status
// is the outcome of the last check, and Reason says it the way a stage's reason does.
type ClarityTrace struct {
	Threshold float64       `json:"threshold"`
	Checks    []float64     `json:"checks"`
	Status    ClarityStatus `json:"status"`
	Reason    string        `json:"reason,omitempty"`
}

// MaxClarityChecks bounds Checks of a decision.json read back: the pre-check, and a re-check
// after each of at most clarify.max_rounds (5) additions and the clarify step itself.
const MaxClarityChecks = 8

// Validate checks a ClarityTrace read back from decision.json.
func (c ClarityTrace) Validate() error {
	if math.IsNaN(c.Threshold) || c.Threshold <= 0 || c.Threshold > 1 {
		return fmt.Errorf("threshold %v outside (0, 1]", c.Threshold)
	}
	if n := len(c.Checks); n < 1 || n > MaxClarityChecks {
		return fmt.Errorf("%d checks, want 1 to %d", n, MaxClarityChecks)
	}
	for i, p := range c.Checks {
		if err := validateUnit(fmt.Sprintf("checks[%d]", i), p); err != nil {
			return err
		}
	}
	if !c.Status.Valid() {
		return fmt.Errorf("unknown status %q", c.Status)
	}
	return nil
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
	// TranslationClarified is the second translation, of the request followed by its
	// clarification (specs/clarify), made only after a clarification was added.
	TranslationClarified *Usage `json:"translation_clarified,omitempty"`
	// Clarify is the usage of the clarify step's call that reported any (at most one per run).
	Clarify []Usage `json:"clarify,omitempty"`
	// ClarityRecheck is the usage of each clarity re-check that reported any, in order.
	ClarityRecheck []Usage `json:"clarity_recheck,omitempty"`
}

// Empty reports whether no round recorded usage.
func (u DecisionUsage) Empty() bool {
	return u.Translation == nil && u.ContextDepth == nil && u.Classification == nil &&
		u.TranslationClarified == nil && len(u.Clarify) == 0 && len(u.ClarityRecheck) == 0
}

// Calls returns the usage of every decision call that reported any: the rounds of the decision
// proper, then the clarify step's call and the clarity re-checks.
func (u DecisionUsage) Calls() []Usage {
	var out []Usage
	for _, c := range []*Usage{u.Translation, u.ContextDepth, u.Classification, u.TranslationClarified} {
		if c != nil {
			out = append(out, *c)
		}
	}
	out = append(out, u.Clarify...)
	return append(out, u.ClarityRecheck...)
}

// ClarifyCalls returns the usage of the calls a manual decision (run --stage) makes: the clarify
// step's and the clarity re-checks. They are also the decision calls limits.max_cost_usd counts.
func (u DecisionUsage) ClarifyCalls() []Usage {
	return append(append([]Usage(nil), u.Clarify...), u.ClarityRecheck...)
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
