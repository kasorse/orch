package domain

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// ContextDepth is how much repository context a task needs before it can be
// classified (specs/JEV integration/SPEC.md §4). It is a string rather than a bool so
// finer depths can be added later without a breaking change.
type ContextDepth string

const (
	ContextDepthNone    ContextDepth = "none"
	ContextDepthProject ContextDepth = "project"
)

var contextDepths = []ContextDepth{ContextDepthNone, ContextDepthProject}

// ParseContextDepth validates a context depth name.
func ParseContextDepth(s string) (ContextDepth, error) {
	d := ContextDepth(s)
	if !contains(contextDepths, d) {
		return "", fmt.Errorf("unsupported context depth %q (want one of: %s)", s, joinValues(contextDepths))
	}
	return d, nil
}

// ContextDepths returns every supported depth, shallowest first.
func ContextDepths() []ContextDepth {
	return append([]ContextDepth(nil), contextDepths...)
}

// Complexity is the task complexity scale (SPEC.md §7).
type Complexity string

const (
	ComplexitySimple  Complexity = "simple"
	ComplexityMedium  Complexity = "medium"
	ComplexityComplex Complexity = "complex"
)

// complexityOrder runs from least to most complex. A Score question's level i is
// complexityOrder[i], and the policy's complexity rule walks it from the top down.
var complexityOrder = []Complexity{ComplexitySimple, ComplexityMedium, ComplexityComplex}

// ParseComplexity validates a complexity name, e.g. from configuration.
func ParseComplexity(s string) (Complexity, error) {
	c := Complexity(s)
	if !contains(complexityOrder, c) {
		return "", fmt.Errorf("unsupported complexity %q (want one of: %s)", s, joinValues(complexityOrder))
	}
	return c, nil
}

// ComplexityOrder returns every supported complexity, least complex first.
func ComplexityOrder() []Complexity {
	return append([]Complexity(nil), complexityOrder...)
}

// ComplexitySource names the question type a complexity distribution came from. Both
// are asked in the same request while the two are being compared; the policy decides
// on one (the primary) and only logs the other (TASKS.md В1).
type ComplexitySource string

const (
	ComplexitySourceChoice ComplexitySource = "choice"
	ComplexitySourceScore  ComplexitySource = "score"
)

var complexitySources = []ComplexitySource{ComplexitySourceChoice, ComplexitySourceScore}

// ParseComplexitySource validates a complexity source name.
func ParseComplexitySource(s string) (ComplexitySource, error) {
	src := ComplexitySource(s)
	if !contains(complexitySources, src) {
		return "", fmt.Errorf("unsupported complexity source %q (want one of: %s)", s, joinValues(complexitySources))
	}
	return src, nil
}

// ContextDepthDecision is a provider's raw answer to the pre-check: a probability for
// every depth. The provider never picks one - the policy does.
type ContextDepthDecision struct {
	Probabilities map[ContextDepth]float64 `json:"probabilities"`
	Confidence    float64                  `json:"confidence"`
	// PClear is P(clear): the probability that the request is clear enough to act on
	// (specs/clarify), asked in the same call. Go compares it with policy.clarify.threshold.
	PClear float64 `json:"p_clear"`
	// Usage is what the call that answered cost, when the provider reports it. It is not
	// part of the answer: the trace records it separately (DecisionTrace.Usage).
	Usage *Usage `json:"-"`
}

func (d ContextDepthDecision) Validate() error {
	if err := validateDistribution(d.Probabilities, contextDepths); err != nil {
		return fmt.Errorf("context depth: %w", err)
	}
	if err := validateUnit("context depth confidence", d.Confidence); err != nil {
		return err
	}
	if err := validateUnit("clarity probability", d.PClear); err != nil {
		return err
	}
	return nil
}

// ClarityDecision is a provider's answer to the clarity question asked alone, on a request and
// the clarification so far (decision.Provider.Clarity): P(clear), compared by the policy with
// policy.clarify.threshold.
type ClarityDecision struct {
	PClear float64 `json:"p_clear"`
	// Usage is what the call cost, like ContextDepthDecision.Usage.
	Usage *Usage `json:"-"`
}

func (d ClarityDecision) Validate() error {
	return validateUnit("clarity probability", d.PClear)
}

// ComplexityDecision is a provider's raw complexity distribution. Score is set only
// when the distribution came from a Score question: the probability-weighted level,
// between 0 (simple) and len(complexityOrder)-1 (complex).
type ComplexityDecision struct {
	Probabilities map[Complexity]float64 `json:"probabilities"`
	Confidence    float64                `json:"confidence"`
	Score         *float64               `json:"score,omitempty"`
}

// Size is the distribution's expected level, Σ i·P(level i) over ComplexityOrder:
// P(medium) + 2·P(complex), in [0, 2] (specs/decision-shape/SPEC.md §5.1). It is computed
// from the probabilities for every provider alike; Score, when a provider sends one, is
// recorded and never used.
func (d ComplexityDecision) Size() float64 {
	size := 0.0
	for i, c := range complexityOrder {
		size += float64(i) * d.Probabilities[c]
	}
	return size
}

func (d ComplexityDecision) Validate() error {
	if err := validateDistribution(d.Probabilities, complexityOrder); err != nil {
		return fmt.Errorf("complexity: %w", err)
	}
	if err := validateUnit("complexity confidence", d.Confidence); err != nil {
		return err
	}
	if d.Score != nil {
		maxLevel := float64(len(complexityOrder) - 1)
		if s := *d.Score; math.IsNaN(s) || s < 0 || s > maxLevel {
			return fmt.Errorf("complexity score %v outside [0, %v]", s, maxLevel)
		}
	}
	return nil
}

// ProbabilityDecision is the probability that one stage is required. Jev's yes/no
// questions carry no confidence, so neither does this.
type ProbabilityDecision struct {
	Probability float64 `json:"probability"`
}

// askedStages are the stages a decision provider is asked about (specs/decision-shape/SPEC.md
// §6.1), in stage order: facts about the task that its size does not already carry. Investigation,
// testing and review come from the level and from development instead.
var askedStages = []Stage{StageResearch, StageDevelopment}

// AskedStages returns the stages a decision provider answers a probability for.
func AskedStages() []Stage { return append([]Stage(nil), askedStages...) }

// StageDecisions holds the probability of each stage a provider was asked about; a stage that
// was not asked is nil.
type StageDecisions struct {
	Research      *ProbabilityDecision `json:"research,omitempty"`
	Investigation *ProbabilityDecision `json:"investigation,omitempty"`
	Development   *ProbabilityDecision `json:"development,omitempty"`
	Testing       *ProbabilityDecision `json:"testing,omitempty"`
	Review        *ProbabilityDecision `json:"review,omitempty"`
}

// field returns a pointer to s's field for stage, or nil for an unknown stage.
func (s *StageDecisions) field(stage Stage) **ProbabilityDecision {
	switch stage {
	case StageResearch:
		return &s.Research
	case StageInvestigation:
		return &s.Investigation
	case StageDevelopment:
		return &s.Development
	case StageTesting:
		return &s.Testing
	case StageReview:
		return &s.Review
	default:
		return nil
	}
}

// Get returns the decision for stage, or false for an unknown stage or one not asked.
func (s StageDecisions) Get(stage Stage) (ProbabilityDecision, bool) {
	f := s.field(stage)
	if f == nil || *f == nil {
		return ProbabilityDecision{}, false
	}
	return **f, true
}

// Set stores the decision for stage and reports whether stage is known.
func (s *StageDecisions) Set(stage Stage, d ProbabilityDecision) bool {
	f := s.field(stage)
	if f == nil {
		return false
	}
	*f = &d
	return true
}

// StageProbability pairs a stage with its decision.
type StageProbability struct {
	Stage    Stage
	Decision ProbabilityDecision
}

// All returns the decision of every stage that has one, in execution order.
func (s StageDecisions) All() []StageProbability {
	out := make([]StageProbability, 0, len(stageOrder))
	for _, st := range stageOrder {
		if d, ok := s.Get(st); ok {
			out = append(out, StageProbability{Stage: st, Decision: d})
		}
	}
	return out
}

// Validate requires a probability for every asked stage, none for the others, and each in
// [0, 1].
func (s StageDecisions) Validate() error {
	for _, st := range stageOrder {
		_, has := s.Get(st)
		if asked := slices.Contains(askedStages, st); has != asked {
			if asked {
				return fmt.Errorf("stage %s has no probability", st)
			}
			return fmt.Errorf("stage %s is not asked, but has a probability", st)
		}
	}
	for _, sp := range s.All() {
		if err := validateUnit(fmt.Sprintf("stage %s probability", sp.Stage), sp.Decision.Probability); err != nil {
			return err
		}
	}
	return nil
}

// ScopeDecision is a provider's answer to the shadow scope question (specs/story/SPEC.md
// §13): the probability that the task needs several changes each worth merging on its own.
// It is recorded and decides nothing - the policy, the workflow and the routes never read it.
type ScopeDecision struct {
	Story float64 `json:"story"`
}

func (d ScopeDecision) Validate() error {
	return validateUnit("scope story probability", d.Story)
}

// ClassificationInput is what the final classification sees. Project is nil when the
// depth is none, or when it is project but context collection failed - ContextLimited
// tells those two apart. The depth and limitation are the orchestrator's facts; a
// provider copies them into its TaskClassification unchanged.
type ClassificationInput struct {
	// Request is the text sent to the decision provider - the English translation
	// when the user's request was translated.
	Request        string
	ContextDepth   ContextDepth
	ContextLimited bool
	Project        *ProjectContext
	// SkipScope leaves the scope question out of the call: a story task is never a story
	// (specs/story/SPEC.md §13). The zero value asks it. It is not part of the state sent to
	// the provider.
	SkipScope bool
}

// TaskClassification is a decision provider's raw result: probability distributions
// only. Selecting a complexity and the enabled stages from them is the policy's job.
type TaskClassification struct {
	ContextDepth   ContextDepth `json:"context_depth"`
	ContextLimited bool         `json:"context_limited"`
	// Complexity holds one distribution per question type asked (TASKS.md В1).
	Complexity map[ComplexitySource]ComplexityDecision `json:"complexity"`
	Stages     StageDecisions                          `json:"stages"`
	// Scope is the shadow scope answer; nil when the question was not asked.
	Scope *ScopeDecision `json:"scope,omitempty"`
	// Provider names the decision provider that answered, e.g. "jev".
	Provider string `json:"provider"`
	// Model is the exact model version that answered, as reported by the provider.
	Model string `json:"model,omitempty"`
	// Usage is what the call that answered cost, when the provider reports it (see
	// ContextDepthDecision.Usage).
	Usage *Usage `json:"-"`
}

func (c TaskClassification) Validate() error {
	if !contains(contextDepths, c.ContextDepth) {
		return fmt.Errorf("unsupported context depth %q", c.ContextDepth)
	}
	if len(c.Complexity) == 0 {
		return fmt.Errorf("no complexity distribution")
	}
	for src := range c.Complexity {
		if !contains(complexitySources, src) {
			return fmt.Errorf("unsupported complexity source %q", src)
		}
	}
	// Walk sources in a fixed order so the first reported problem is deterministic.
	for _, src := range complexitySources {
		d, ok := c.Complexity[src]
		if !ok {
			continue
		}
		if err := d.Validate(); err != nil {
			return fmt.Errorf("%s %w", src, err)
		}
		if src != ComplexitySourceScore && d.Score != nil {
			return fmt.Errorf("%s complexity must not carry a score", src)
		}
	}
	if c.Scope != nil {
		if err := c.Scope.Validate(); err != nil {
			return err
		}
	}
	return c.Stages.Validate()
}

// distributionTolerance absorbs float rounding in provider-reported probabilities,
// which are documented to sum to 1.
const distributionTolerance = 0.01

// validateDistribution checks that dist has exactly the keys in want, each a valid
// probability, summing to 1 within distributionTolerance.
func validateDistribution[K ~string](dist map[K]float64, want []K) error {
	if len(dist) != len(want) {
		return fmt.Errorf("distribution has %d options, want %d (%s)", len(dist), len(want), joinValues(want))
	}
	sum := 0.0
	for _, k := range want {
		p, ok := dist[k]
		if !ok {
			return fmt.Errorf("distribution is missing option %q", k)
		}
		if err := validateUnit(fmt.Sprintf("probability of %q", k), p); err != nil {
			return err
		}
		sum += p
	}
	if math.Abs(sum-1) > distributionTolerance {
		return fmt.Errorf("probabilities sum to %v, want 1", sum)
	}
	return nil
}

// validateUnit checks that v is a number in [0, 1].
func validateUnit(name string, v float64) error {
	if math.IsNaN(v) || v < 0 || v > 1 {
		return fmt.Errorf("%s %v outside [0, 1]", name, v)
	}
	return nil
}

func contains[T comparable](values []T, v T) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func joinValues[T ~string](values []T) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = string(v)
	}
	return strings.Join(parts, ", ")
}
