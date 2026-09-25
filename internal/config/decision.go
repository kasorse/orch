// This file defines and validates the Jev integration's config surface
// (specs/JEV integration/TASKS.md T02): the decision provider, translation, policy
// thresholds, and context-collection budget. See the Config.Decision/Translation/
// Policy/Context doc comment in config.go for why this validation is not yet part of
// Validate/ValidateDependencies' fail-fast chain.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"orchestrator/internal/apperr"
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

// ComplexityPolicyConfig configures the cumulative complexity thresholds (TASKS.md
// В2): Complex is selected when P(complex) is at least Complex; otherwise Medium is
// selected when P(medium)+P(complex) is at least Medium; otherwise Simple.
type ComplexityPolicyConfig struct {
	Complex *float64 `yaml:"complex"`
	Medium  *float64 `yaml:"medium"`
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

	complexityThreshold := make(map[domain.Complexity]float64, 2)
	if c.Policy.Complexity.Complex != nil {
		complexityThreshold[domain.ComplexityComplex] = *c.Policy.Complexity.Complex
	}
	if c.Policy.Complexity.Medium != nil {
		complexityThreshold[domain.ComplexityMedium] = *c.Policy.Complexity.Medium
	}

	projectThreshold := 0.0
	if c.Policy.ContextDepth.ProjectThreshold != nil {
		projectThreshold = *c.Policy.ContextDepth.ProjectThreshold
	}

	return domain.DecisionPolicy{
		ProjectContextThreshold: projectThreshold,
		ComplexityThreshold:     complexityThreshold,
		StageThreshold:          stageThreshold,
		ForceStages:             forceStages,
	}
}

// ValidateDecisionConfig checks structural correctness of the Jev integration's config
// surface: decision.jev, decision.on_failure, translation, policy thresholds, context
// budget, and actions.research. It mirrors Validate's aggregate-every-problem style but
// is a separate function - see the Config.Decision doc comment in config.go for why.
func ValidateDecisionConfig(cfg Config) error {
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

	problems = append(problems, validateProviderModel("research", cfg.Actions.Research.ProviderModel)...)
	if cfg.Actions.Research.Timeout < 0 {
		problems = append(problems, fmt.Sprintf(
			"actions.research.timeout must not be negative, got %s", cfg.Actions.Research.Timeout,
		))
	}

	if len(problems) == 0 {
		return nil
	}
	return apperr.NewConfigError(
		fmt.Sprintf("configuration validation failed:\n\n%s", strings.Join(problems, "\n")),
		nil,
	)
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

func validatePolicyConfig(p PolicyConfig) []string {
	var problems []string

	if p.ContextDepth.ProjectThreshold == nil {
		problems = append(problems, "policy.context_depth.project_threshold is required")
	} else if v := *p.ContextDepth.ProjectThreshold; v < 0 || v > 1 {
		problems = append(problems, fmt.Sprintf("policy.context_depth.project_threshold must be in [0, 1], got %v", v))
	}

	if p.Complexity.Complex == nil {
		problems = append(problems, "policy.complexity.complex is required")
	} else if v := *p.Complexity.Complex; v < 0 || v > 1 {
		problems = append(problems, fmt.Sprintf("policy.complexity.complex must be in [0, 1], got %v", v))
	}
	if p.Complexity.Medium == nil {
		problems = append(problems, "policy.complexity.medium is required")
	} else if v := *p.Complexity.Medium; v < 0 || v > 1 {
		problems = append(problems, fmt.Sprintf("policy.complexity.medium must be in [0, 1], got %v", v))
	}

	for _, st := range domain.StageOrder() {
		v, ok := p.Stages.Thresholds[string(st)]
		if !ok {
			problems = append(problems, fmt.Sprintf("policy.stages.thresholds.%s is required", st))
			continue
		}
		if v < 0 || v > 1 {
			problems = append(problems, fmt.Sprintf("policy.stages.thresholds.%s must be in [0, 1], got %v", st, v))
		}
	}
	for key := range p.Stages.Thresholds {
		if _, err := domain.ParseStage(key); err != nil {
			problems = append(problems, fmt.Sprintf("policy.stages.thresholds has unknown stage %q", key))
		}
	}

	for key, stages := range p.Stages.Force {
		if _, err := domain.ParseComplexity(key); err != nil {
			problems = append(problems, fmt.Sprintf("policy.stages.force has unknown complexity %q", key))
			continue
		}
		for _, s := range stages {
			if _, err := domain.ParseStage(s); err != nil {
				problems = append(problems, fmt.Sprintf("policy.stages.force.%s has unknown stage %q", key, s))
			}
		}
	}

	return problems
}

// ValidateDecisionConfigDependencies checks the parts of the Jev integration's config
// that require touching the environment: the executables backing translation, research
// and - for an LLM decision provider - the decisions themselves, and, for Jev, that
// decision.jev.api_key_env names a set environment variable (unless decision.jev.mock
// is set: the mock needs no key). It must run after ValidateDecisionConfig, for the
// same reason ValidateDependencies must run after Validate.
func ValidateDecisionConfigDependencies(cfg Config, checker DependencyChecker) error {
	var problems []string

	d := cfg.Decision
	if env := strings.TrimSpace(d.Jev.APIKeyEnv); d.UsesJev() && env != "" && !d.Jev.Mock {
		if v, ok := os.LookupEnv(env); !ok || strings.TrimSpace(v) == "" {
			problems = append(problems, fmt.Sprintf("jev decision provider: %s is not configured", env))
		}
	}

	pms := []namedProviderModel{
		{Action: "translation", ProviderModel: cfg.Translation.ProviderModel},
		{Action: "research", ProviderModel: cfg.Actions.Research.ProviderModel},
	}
	if !d.UsesJev() {
		pms = append(pms, namedProviderModel{Action: "decision", ProviderModel: ProviderModel{Provider: d.Provider, Model: d.Model}})
	}
	problems = append(problems, checkExecutablesPresent(pms, checker)...)

	if len(problems) == 0 {
		return nil
	}
	return apperr.NewDependencyError(
		fmt.Sprintf("configuration validation failed:\n\n%s", strings.Join(problems, "\n\n")),
		nil,
	)
}
