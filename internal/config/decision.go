// This file defines and validates the decision config surface (specs/JEV
// integration/TASKS.md T02, T22): the decision provider, translation, policy thresholds,
// and context-collection budget. Validate and ValidateDependencies include these checks.
package config

import (
	"fmt"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

// OnFailure selects what happens when the Jev decision provider fails (SPEC.md §17,
// TASKS.md В9: this project asks the user rather than falling back to an LLM
// provider).
type OnFailure string

const (
	// OnFailureAsk lets the user choose complexity and stages by hand (T16).
	OnFailureAsk OnFailure = "ask"
	// OnFailureFail ends the run with the decision error.
	OnFailureFail OnFailure = "fail"
)

var validOnFailure = map[OnFailure]bool{OnFailureAsk: true, OnFailureFail: true}

// DecisionProviderJev is decision.provider's value for the Jev HTTP provider; any
// other value names a CLI provider (claude, codex, gemini) answering the same
// questions (T22).
const DecisionProviderJev = "jev"

// ComplexityQuestionsConfig selects which complexity question the Policy Engine
// decides on (Primary) and whether to also ask the other one purely for comparison
// logging (Shadow) - see TASKS.md В1.
type ComplexityQuestionsConfig struct {
	Primary string `yaml:"primary"`
	Shadow  bool   `yaml:"shadow"`
}

// Sources returns the complexity questions to ask: the primary one, plus the other
// when Shadow is on. Call only after validation.
func (c ComplexityQuestionsConfig) Sources() []domain.ComplexitySource {
	primary := domain.ComplexitySource(c.Primary)
	sources := []domain.ComplexitySource{primary}
	if c.Shadow {
		for _, src := range []domain.ComplexitySource{domain.ComplexitySourceChoice, domain.ComplexitySourceScore} {
			if src != primary {
				sources = append(sources, src)
			}
		}
	}
	return sources
}

// JevConfig holds the Jev HTTP transport settings (T06), used only when
// decision.provider is jev. The model, timeout and questions are provider-neutral and
// live on DecisionConfig.
type JevConfig struct {
	BaseURL    string `yaml:"base_url"`
	APIKeyEnv  string `yaml:"api_key_env"`
	MaxRetries int    `yaml:"max_retries"`
	// Mock swaps the HTTP client for the offline mock (jev.NewMockClient): no network,
	// no API key, deterministic keyword-based answers. It exists while Jev API access
	// is unavailable; the rest of decision.jev is still validated as usual.
	Mock bool `yaml:"mock"`

	// apiKeyLiteralSet records whether the YAML set a literal "api_key" field, which
	// ValidateDecisionConfig rejects: the key must come from the environment
	// (api_key_env), never from a file that might end up committed.
	apiKeyLiteralSet bool
}

// UnmarshalYAML captures whether a literal api_key field was present, without ever
// exposing its value as a Go field.
func (j *JevConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		BaseURL    string `yaml:"base_url"`
		APIKeyEnv  string `yaml:"api_key_env"`
		APIKey     string `yaml:"api_key"`
		MaxRetries int    `yaml:"max_retries"`
		Mock       bool   `yaml:"mock"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	j.BaseURL = raw.BaseURL
	j.APIKeyEnv = raw.APIKeyEnv
	j.apiKeyLiteralSet = raw.APIKey != ""
	j.MaxRetries = raw.MaxRetries
	j.Mock = raw.Mock
	return nil
}

// DecisionConfig configures the decision provider (T06/T07/T09/T22), selected like
// the classifier: a provider and a model. Provider is jev or a CLI provider; the flow
// is the same either way (TASKS.md T22).
type DecisionConfig struct {
	Provider string `yaml:"provider"`
	// Model is the Jev model version for jev, the CLI model otherwise.
	Model string `yaml:"model"`
	// Timeout bounds one HTTP request for jev, one CLI call otherwise.
	Timeout    time.Duration
	Complexity ComplexityQuestionsConfig `yaml:"complexity"`
	OnFailure  OnFailure                 `yaml:"on_failure"`
	// Jev is read only when Provider is jev.
	Jev JevConfig `yaml:"jev"`
}

// UsesJev reports whether decisions go to the Jev HTTP API.
func (d DecisionConfig) UsesJev() bool { return d.Provider == DecisionProviderJev }

// UnmarshalYAML parses the timeout through the same duration-string handling as
// ActionConfig/ExecutionConfig.
func (d *DecisionConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Provider   string                    `yaml:"provider"`
		Model      string                    `yaml:"model"`
		Timeout    string                    `yaml:"timeout"`
		Complexity ComplexityQuestionsConfig `yaml:"complexity"`
		OnFailure  OnFailure                 `yaml:"on_failure"`
		Jev        JevConfig                 `yaml:"jev"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	timeout, err := parseDurationField("decision.timeout", raw.Timeout)
	if err != nil {
		return err
	}
	*d = DecisionConfig{
		Provider: raw.Provider, Model: raw.Model, Timeout: timeout,
		Complexity: raw.Complexity, OnFailure: raw.OnFailure, Jev: raw.Jev,
	}
	return nil
}

// TranslationConfig configures the request translation step (T15). Unlike
// ActionConfig, Timeout is required and positive: there is no run-wide timeout to
// silently fall back on, and an unbounded translation call would be a silent way to
// blow through execution.timeout before the decision provider is even called.
type TranslationConfig struct {
	ProviderModel
	Timeout time.Duration
}

func (t *TranslationConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
		Timeout  string `yaml:"timeout"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	t.Provider = raw.Provider
	t.Model = raw.Model
	d, err := parseDurationField("translation.timeout", raw.Timeout)
	if err != nil {
		return err
	}
	t.Timeout = d
	return nil
}

// ContextDepthPolicyConfig configures the pre-check threshold (T03).
type ContextDepthPolicyConfig struct {
	// ProjectThreshold is a pointer so an omitted value (nil) can be told apart from
	// an explicit 0, which is a legal - if extreme - threshold.
	ProjectThreshold *float64 `yaml:"project_threshold"`
}

// ComplexityPolicyConfig configures the cuts on a complexity distribution's size
// (specs/decision-shape/SPEC.md §5.2). Complex and Medium, the probability thresholds
// before it, are declared only so strict decoding accepts them and the validator can name
// their replacement: the same key with another meaning would change a policy silently.
type ComplexityPolicyConfig struct {
	Cuts    *ComplexityCutsConfig `yaml:"cuts"`
	Complex any                   `yaml:"complex"`
	Medium  any                   `yaml:"medium"`
}

// ComplexityCutsConfig is policy.complexity.cuts.
type ComplexityCutsConfig struct {
	Medium  *float64 `yaml:"medium"`
	Complex *float64 `yaml:"complex"`
}

// StagePolicyConfig configures per-stage enablement thresholds and the stages that
// are always enabled once a given complexity is selected (SPEC.md §12).
// Keys are stage/complexity names, validated against domain.ParseStage/
// domain.ParseComplexity - a plain map (not domain.Stage-keyed) because this is the
// YAML-facing shape; DecisionPolicy() converts it once validation has passed.
type StagePolicyConfig struct {
	Thresholds map[string]float64  `yaml:"thresholds"`
	Force      map[string][]string `yaml:"force"`
}

// PolicyConfig configures the Policy Engine (T03). It corresponds to the "workflow:"
// section in specs/JEV integration/SPEC.md §12 - named "policy" here because this
// codebase has no separate "workflow:" config section, and "policy" names the
// component (internal/policy) that consumes it.
type PolicyConfig struct {
	ContextDepth ContextDepthPolicyConfig `yaml:"context_depth"`
	Complexity   ComplexityPolicyConfig   `yaml:"complexity"`
	Stages       StagePolicyConfig        `yaml:"stages"`
	// Paths are the path rules, applied in file order (specs/governance/SPEC.md §4.1).
	Paths []PathRuleConfig `yaml:"paths"`
	// Explore, when set, turns a stage just below its threshold on for a share of runs
	// (specs/governance/SPEC.md §7.4). It is absent by default, and the org file cannot set it.
	Explore *ExploreConfig `yaml:"explore"`
}

// ExploreConfig is policy.explore: a stage whose probability is within Band below its threshold
// is turned on in Rate of the runs, chosen by the run id.
type ExploreConfig struct {
	Band float64 `yaml:"band"`
	Rate float64 `yaml:"rate"`
}

// PathRuleConfig is one policy.paths entry: a change matching Match needs RequireStages in the
// workflow and, with Approval, a person's yes before it is committed. DenyChange is declared
// only so strict decoding accepts the key and the validator can reject it with its own message.
type PathRuleConfig struct {
	Match         string   `yaml:"match"`
	RequireStages []string `yaml:"require_stages"`
	Approval      string   `yaml:"approval"`
	DenyChange    any      `yaml:"deny_change"`
}

// PathRules converts the validated policy.paths into the domain rules a run records, in file
// order; nil when there are none. Call it only after Validate.
func (c Config) PathRules() []domain.PathRule {
	if len(c.Policy.Paths) == 0 {
		return nil
	}
	rules := make([]domain.PathRule, len(c.Policy.Paths))
	for i, p := range c.Policy.Paths {
		rules[i] = domain.PathRule{Match: p.Match, Approval: p.Approval}
		for _, s := range p.RequireStages {
			rules[i].RequireStages = append(rules[i].RequireStages, domain.Stage(s))
		}
	}
	return rules
}

// pathRuleProblems validates policy.paths: a usable pattern, known stages without duplicates,
// approval manual or absent, a rule that is not empty, and no deny_change.
func pathRuleProblems(cfg Config) []string {
	var problems []string
	for i, p := range cfg.Policy.Paths {
		at := fmt.Sprintf("policy.paths[%d]", i)
		if err := domain.ValidateProtectedPattern(p.Match); err != nil {
			problems = append(problems, fmt.Sprintf("%s.match %q: %v", at, p.Match, err))
		}
		seen := map[string]bool{}
		for j, s := range p.RequireStages {
			if _, err := domain.ParseStage(s); err != nil {
				problems = append(problems, fmt.Sprintf("%s.require_stages[%d]: %v", at, j, err))
			} else if seen[s] {
				problems = append(problems, fmt.Sprintf("%s.require_stages[%d]: duplicate stage %q", at, j, s))
			}
			seen[s] = true
		}
		if p.Approval != "" && p.Approval != domain.PathApprovalManual {
			problems = append(problems, fmt.Sprintf("%s.approval must be %q or absent, got %q", at, domain.PathApprovalManual, p.Approval))
		}
		if len(p.RequireStages) == 0 && p.Approval == "" && p.DenyChange == nil {
			problems = append(problems, fmt.Sprintf("%s is empty: it needs require_stages, approval, or both", at))
		}
		if p.DenyChange != nil {
			problems = append(problems, at+".deny_change: use security.protected_paths: "+
				"the guard already refuses any change there, git-ignored files included")
		}
	}
	return problems
}

// ContextConfig bounds what the Context Manager (T08) collects and sends to the
// decision provider.
type ContextConfig struct {
	MaxFiles  int      `yaml:"max_files"`
	MaxBytes  int      `yaml:"max_bytes"`
	ExtraDeny []string `yaml:"extra_deny"`
}

// DecisionPolicy converts the validated policy configuration into the domain-only
// value the Policy Engine (internal/policy, T03) consumes. Callers must call
// ValidateDecisionConfig first: DecisionPolicy assumes every required field is
// present and in range, exactly like domain.LimitsView's construction in app.go
// assumes Validate already ran.
func (c Config) DecisionPolicy() domain.DecisionPolicy {
	stageThreshold := make(map[domain.Stage]float64, len(c.Policy.Stages.Thresholds))
	for k, v := range c.Policy.Stages.Thresholds {
		if st, err := domain.ParseStage(k); err == nil {
			stageThreshold[st] = v
		}
	}

	forceStages := make(map[domain.Complexity][]domain.Stage, len(c.Policy.Stages.Force))
	for k, stages := range c.Policy.Stages.Force {
		cx, err := domain.ParseComplexity(k)
		if err != nil {
			continue
		}
		parsed := make([]domain.Stage, 0, len(stages))
		for _, s := range stages {
			if st, err := domain.ParseStage(s); err == nil {
				parsed = append(parsed, st)
			}
		}
		forceStages[cx] = parsed
	}

	var cuts domain.ComplexityCuts
	if cc := c.Policy.Complexity.Cuts; cc != nil {
		if cc.Medium != nil {
			cuts.Medium = *cc.Medium
		}
		if cc.Complex != nil {
			cuts.Complex = *cc.Complex
		}
	}

	projectThreshold := 0.0
	if c.Policy.ContextDepth.ProjectThreshold != nil {
		projectThreshold = *c.Policy.ContextDepth.ProjectThreshold
	}

	var explore *domain.ExploreSettings
	if c.Policy.Explore != nil {
		explore = &domain.ExploreSettings{Band: c.Policy.Explore.Band, Rate: c.Policy.Explore.Rate}
	}

	return domain.DecisionPolicy{
		ProjectContextThreshold: projectThreshold,
		ComplexityCuts:          cuts,
		StageThreshold:          stageThreshold,
		ForceStages:             forceStages,
		Explore:                 explore,
	}
}

// decisionProblems validates the decision surface: decision, decision.on_failure,
// translation, policy thresholds, and context budget. It is part of Validate.
func decisionProblems(cfg Config) []string {
	var problems []string

	problems = append(problems, validateDecision(cfg.Decision)...)

	if !validOnFailure[cfg.Decision.OnFailure] {
		problems = append(problems, fmt.Sprintf(
			"decision.on_failure must be %q or %q, got %q", OnFailureAsk, OnFailureFail, cfg.Decision.OnFailure,
		))
	}

	problems = append(problems, validateProviderModel("translation", cfg.Translation.ProviderModel)...)
	if cfg.Translation.Timeout <= 0 {
		problems = append(problems, "translation.timeout must be a positive duration")
	}

	problems = append(problems, validatePolicyConfig(cfg.Policy)...)

	if cfg.Context.MaxFiles <= 0 {
		problems = append(problems, fmt.Sprintf("context.max_files must be positive, got %d", cfg.Context.MaxFiles))
	}
	if cfg.Context.MaxBytes <= 0 {
		problems = append(problems, fmt.Sprintf("context.max_bytes must be positive, got %d", cfg.Context.MaxBytes))
	}
	for i, pattern := range cfg.Context.ExtraDeny {
		if strings.TrimSpace(pattern) == "" {
			problems = append(problems, fmt.Sprintf("context.extra_deny[%d] must not be empty", i))
		}
	}
	return problems
}

// validateDecision checks the provider-neutral decision settings, then the Jev block
// only when Jev is the provider: with an LLM provider the jev block may stay in the file
// untouched, so switching providers is a one-line change. A literal api_key is rejected
// either way - a secret in a config file is a problem whoever reads it.
func validateDecision(d DecisionConfig) []string {
	var problems []string

	switch {
	case strings.TrimSpace(d.Provider) == "":
		problems = append(problems, "decision.provider is required")
	case d.Provider != DecisionProviderJev && !provider.IsSupported(d.Provider):
		problems = append(problems, fmt.Sprintf(
			"decision.provider %q is not supported (supported: %s, %s)",
			d.Provider, DecisionProviderJev, supportedProviderNames(),
		))
	}
	if strings.TrimSpace(d.Model) == "" {
		problems = append(problems, "decision.model is required")
	}
	if d.Timeout <= 0 {
		problems = append(problems, "decision.timeout must be a positive duration")
	}
	if _, err := domain.ParseComplexitySource(d.Complexity.Primary); err != nil {
		problems = append(problems, fmt.Sprintf("decision.complexity.primary: %v", err))
	}

	if d.Jev.apiKeyLiteralSet {
		problems = append(problems, "decision.jev.api_key must not be set in the configuration file; "+
			"name an environment variable via decision.jev.api_key_env instead")
	}
	if d.UsesJev() {
		problems = append(problems, validateJevConfig(d.Jev)...)
	}
	return problems
}

func validateJevConfig(j JevConfig) []string {
	var problems []string

	if strings.TrimSpace(j.BaseURL) == "" {
		problems = append(problems, "decision.jev.base_url is required")
	} else if u, err := url.Parse(j.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		problems = append(problems, fmt.Sprintf("decision.jev.base_url %q must be a valid http(s) URL", j.BaseURL))
	}

	if strings.TrimSpace(j.APIKeyEnv) == "" {
		problems = append(problems, "decision.jev.api_key_env is required")
	}

	if j.MaxRetries < 0 || j.MaxRetries > 3 {
		problems = append(problems, fmt.Sprintf("decision.jev.max_retries must be between 0 and 3, got %d", j.MaxRetries))
	}

	return problems
}

// replacedByCuts is the message for a probability threshold the size cuts replaced.
const replacedByCuts = "policy.complexity.%s was replaced by policy.complexity.cuts: the level is now cut " +
	"from size = P(medium) + 2·P(complex), in [0, 2] (specs/decision-shape/SPEC.md §5.2)"

// validateComplexityPolicy checks policy.complexity: no old threshold key, and
// 0 < cuts.medium < cuts.complex <= 2.
func validateComplexityPolicy(c ComplexityPolicyConfig) []string {
	var problems []string
	if c.Medium != nil {
		problems = append(problems, fmt.Sprintf(replacedByCuts, "medium"))
	}
	if c.Complex != nil {
		problems = append(problems, fmt.Sprintf(replacedByCuts, "complex"))
	}
	if c.Cuts == nil {
		return append(problems, "policy.complexity.cuts is required")
	}
	medium, complex := c.Cuts.Medium, c.Cuts.Complex
	if medium == nil {
		problems = append(problems, "policy.complexity.cuts.medium is required")
	}
	if complex == nil {
		problems = append(problems, "policy.complexity.cuts.complex is required")
	}
	if medium != nil && complex != nil && !(0 < *medium && *medium < *complex && *complex <= 2) {
		problems = append(problems, fmt.Sprintf(
			"policy.complexity.cuts must satisfy 0 < medium < complex <= 2, got medium %v, complex %v", *medium, *complex))
	}
	return problems
}

// analysisRemoved says what took the place of the analysis stage (specs/clarify): a key kept
// under the old name would silently mean something else, so it is refused and never mapped.
const analysisRemoved = "analysis is not a stage any more: the clarify step makes the task clear before the " +
	"decision (specs/clarify), and a stage that reads the code is investigation"

// planningRenamed says what took the place of the planning stage (specs/clarify), for the same
// reason: planning is never read as investigation.
const planningRenamed = "planning was renamed investigation: the stage that reads the code plans a change or " +
	"answers (specs/clarify)"

// forceStageProblem checks one stage of policy.stages.force.<complexity> and returns the problem,
// or "" when it is a stage.
func forceStageProblem(complexity, stage string) string {
	switch stage {
	case "analysis":
		return fmt.Sprintf("policy.stages.force.%s: %s", complexity, analysisRemoved)
	case "planning":
		return fmt.Sprintf("policy.stages.force.%s: %s", complexity, planningRenamed)
	}
	if _, err := domain.ParseStage(stage); err != nil {
		return fmt.Sprintf("policy.stages.force.%s has unknown stage %q", complexity, stage)
	}
	return ""
}

func validatePolicyConfig(p PolicyConfig) []string {
	var problems []string

	if p.ContextDepth.ProjectThreshold == nil {
		problems = append(problems, "policy.context_depth.project_threshold is required")
	} else if v := *p.ContextDepth.ProjectThreshold; v < 0 || v > 1 {
		problems = append(problems, fmt.Sprintf("policy.context_depth.project_threshold must be in [0, 1], got %v", v))
	}

	problems = append(problems, validateComplexityPolicy(p.Complexity)...)

	for _, st := range domain.AskedStages() {
		v, ok := p.Stages.Thresholds[string(st)]
		if !ok {
			problems = append(problems, fmt.Sprintf("policy.stages.thresholds.%s is required", st))
			continue
		}
		if v < 0 || v > 1 {
			problems = append(problems, fmt.Sprintf("policy.stages.thresholds.%s must be in [0, 1], got %v", st, v))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(p.Stages.Thresholds)) {
		st, err := domain.ParseStage(key)
		switch {
		case key == "analysis":
			problems = append(problems, "policy.stages.thresholds.analysis: "+analysisRemoved+
				"; whether the task is clear enough is policy.clarify.threshold")
		case key == "planning":
			problems = append(problems, "policy.stages.thresholds.planning: "+planningRenamed+
				"; the investigation stage has no threshold, it comes from policy.stages.force per complexity")
		case err != nil:
			problems = append(problems, fmt.Sprintf("policy.stages.thresholds has unknown stage %q", key))
		case !slices.Contains(domain.AskedStages(), st):
			problems = append(problems, fmt.Sprintf("policy.stages.thresholds.%s: %s is not asked any more; it comes from "+
				"policy.stages.force per complexity, and testing with development (specs/decision-shape/SPEC.md §6)", key, key))
		}
	}

	for key, stages := range p.Stages.Force {
		if _, err := domain.ParseComplexity(key); err != nil {
			problems = append(problems, fmt.Sprintf("policy.stages.force has unknown complexity %q", key))
			continue
		}
		for _, s := range stages {
			if msg := forceStageProblem(key, s); msg != "" {
				problems = append(problems, msg)
			}
		}
	}

	if e := p.Explore; e != nil {
		// Written as a negated range so a NaN is rejected too.
		if !(e.Band > 0 && e.Band <= 0.2) {
			problems = append(problems, fmt.Sprintf("policy.explore.band must be in (0, 0.2], got %v", e.Band))
		}
		if !(e.Rate > 0 && e.Rate <= 0.5) {
			problems = append(problems, fmt.Sprintf("policy.explore.rate must be in (0, 0.5], got %v", e.Rate))
		}
	}

	return problems
}

// jevKeyProblems reports a missing Jev API key: api_key_env must name a set
// environment variable, unless the provider is not Jev or the mock is used.
func jevKeyProblems(d DecisionConfig) []string {
	env := strings.TrimSpace(d.Jev.APIKeyEnv)
	if !d.UsesJev() || d.Jev.Mock || env == "" {
		return nil
	}
	if v, ok := os.LookupEnv(env); !ok || strings.TrimSpace(v) == "" {
		return []string{fmt.Sprintf("jev decision provider: %s is not configured", env)}
	}
	return nil
}

// decisionProviderModels lists the CLIs the decision flow calls: translation always
// (fail-fast must not depend on the request's language), and the decision provider
// when it is an LLM.
func decisionProviderModels(cfg Config) []namedProviderModel {
	pms := []namedProviderModel{{Action: "translation", ProviderModel: cfg.Translation.ProviderModel}}
	if !cfg.Decision.UsesJev() {
		pms = append(pms, namedProviderModel{Action: "decision", ProviderModel: ProviderModel{Provider: cfg.Decision.Provider, Model: cfg.Decision.Model}})
	}
	return pms
}
