// Package config defines the orchestrator's YAML configuration schema and the
// validation/dependency-checking that must run, in full, before any AI call or
// repository modification (see Validate and ValidateDependencies).
package config

import (
	"fmt"
	"time"
)

type ProviderModel struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// ActionConfig is a ProviderModel plus an optional per-action timeout override. Timeout
// only tightens the bound for that one action within the run's existing deadline - it
// can never extend execution below runs past execution.timeout (context.WithTimeout
// nested under the run-wide context always yields the earlier of the two deadlines,
// per MVP.md §25). Zero means "no override": the action is bounded only by whatever of
// execution.timeout remains, exactly as before this field existed.
type ActionConfig struct {
	ProviderModel
	Timeout time.Duration
}

// UnmarshalYAML parses ActionConfig's own provider/model/timeout keys directly (rather
// than relying on yaml.v3's inline-embedding tag) so Timeout can go through the same
// duration-string parsing as ExecutionConfig.Timeout.
func (a *ActionConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
		Timeout  string `yaml:"timeout"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	a.Provider = raw.Provider
	a.Model = raw.Model
	d, err := parseDurationField("timeout", raw.Timeout)
	if err != nil {
		return err
	}
	a.Timeout = d
	return nil
}

// parseDurationField parses a YAML duration string field, shared by ActionConfig and
// ExecutionConfig's UnmarshalYAML: an empty string means "unset" (zero), and a non-empty
// one must be a valid time.ParseDuration string. name identifies the field in the
// wrapped parse error (e.g. "timeout" or "execution.timeout").
func parseDurationField(name, raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s %q: %w", name, raw, err)
	}
	return d, nil
}

type ActionsConfig struct {
	Research  ActionConfig `yaml:"research"`
	Analyze   ActionConfig `yaml:"analyze"`
	Plan      ActionConfig `yaml:"plan"`
	Implement ActionConfig `yaml:"implement"`
	Review    ActionConfig `yaml:"review"`
}

type TestConfig struct {
	Command string `yaml:"command"`
}

type LimitsConfig struct {
	MaxSteps         int `yaml:"max_steps"`
	MaxFixIterations int `yaml:"max_fix_iterations"`
}

// ExecutionConfig.Timeout is parsed from a duration string like "15m" via a custom
// UnmarshalYAML, since YAML has no native duration type.
type ExecutionConfig struct {
	Timeout time.Duration `yaml:"timeout"`
}

func (e *ExecutionConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Timeout string `yaml:"timeout"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	d, err := parseDurationField("execution.timeout", raw.Timeout)
	if err != nil {
		return err
	}
	e.Timeout = d
	return nil
}

// CurrentVersion is the only config version this build accepts.
const CurrentVersion = 2

type Config struct {
	Version   int             `yaml:"version"`
	Actions   ActionsConfig   `yaml:"actions"`
	Test      TestConfig      `yaml:"test"`
	Limits    LimitsConfig    `yaml:"limits"`
	Execution ExecutionConfig `yaml:"execution"`

	// Decision, Translation, Policy, and Context configure how the workflow is decided
	// (specs/JEV integration/TASKS.md T02, T22); see decision.go.
	Decision    DecisionConfig    `yaml:"decision"`
	Translation TranslationConfig `yaml:"translation"`
	Policy      PolicyConfig      `yaml:"policy"`
	Context     ContextConfig     `yaml:"context"`

	// Security holds the knobs of the execution-authority guards (specs/permissions/
	// SPEC.md §7, §9). None of them widens what a step may do.
	Security SecurityConfig `yaml:"security"`
}

type SecurityConfig struct {
	// RequireCleanWorktree is a pointer so an absent key (nil) can default to true while an
	// explicit false still means false; read it through CleanWorktreeRequired.
	RequireCleanWorktree *bool `yaml:"require_clean_worktree"`
	// ProtectedPaths extends domain.DefaultProtectedPaths: patterns an implement step must
	// not change.
	ProtectedPaths []string `yaml:"protected_paths"`
}

// CleanWorktreeRequired reports whether a workflow with implement must start on a clean
// worktree - true unless security.require_clean_worktree is explicitly false.
func (s SecurityConfig) CleanWorktreeRequired() bool {
	return s.RequireCleanWorktree == nil || *s.RequireCleanWorktree
}

// actionProviderModels returns every configured action alongside its pseudo-action
// name, in a fixed order, for validation/dependency-checking iteration.
func (c Config) actionProviderModels() []namedProviderModel {
	return []namedProviderModel{
		{Action: "research", ProviderModel: c.Actions.Research.ProviderModel},
		{Action: "analyze", ProviderModel: c.Actions.Analyze.ProviderModel},
		{Action: "plan", ProviderModel: c.Actions.Plan.ProviderModel},
		{Action: "implement", ProviderModel: c.Actions.Implement.ProviderModel},
		{Action: "review", ProviderModel: c.Actions.Review.ProviderModel},
	}
}

type namedProviderModel struct {
	Action string
	ProviderModel
}

// actionTimeouts returns each action's configured timeout override alongside its
// pseudo-action name, for validation.
func (c Config) actionTimeouts() []namedTimeout {
	return []namedTimeout{
		{Action: "research", Timeout: c.Actions.Research.Timeout},
		{Action: "analyze", Timeout: c.Actions.Analyze.Timeout},
		{Action: "plan", Timeout: c.Actions.Plan.Timeout},
		{Action: "implement", Timeout: c.Actions.Implement.Timeout},
		{Action: "review", Timeout: c.Actions.Review.Timeout},
	}
}

type namedTimeout struct {
	Action  string
	Timeout time.Duration
}
