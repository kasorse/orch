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

type ActionsConfig struct {
	Analyze   ProviderModel `yaml:"analyze"`
	Plan      ProviderModel `yaml:"plan"`
	Implement ProviderModel `yaml:"implement"`
	Review    ProviderModel `yaml:"review"`
	Answer    ProviderModel `yaml:"answer"`
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
	if raw.Timeout == "" {
		e.Timeout = 0
		return nil
	}
	d, err := time.ParseDuration(raw.Timeout)
	if err != nil {
		return fmt.Errorf("execution.timeout %q: %w", raw.Timeout, err)
	}
	e.Timeout = d
	return nil
}

type Config struct {
	Version    int             `yaml:"version"`
	Classifier ProviderModel   `yaml:"classifier"`
	Actions    ActionsConfig   `yaml:"actions"`
	Test       TestConfig      `yaml:"test"`
	Limits     LimitsConfig    `yaml:"limits"`
	Execution  ExecutionConfig `yaml:"execution"`
}

// actionProviderModels returns every configured action alongside its pseudo-action
// name, in a fixed order, for validation/dependency-checking iteration.
func (c Config) actionProviderModels() []namedProviderModel {
	return []namedProviderModel{
		{Action: "classifier", ProviderModel: c.Classifier},
		{Action: "analyze", ProviderModel: c.Actions.Analyze},
		{Action: "plan", ProviderModel: c.Actions.Plan},
		{Action: "implement", ProviderModel: c.Actions.Implement},
		{Action: "review", ProviderModel: c.Actions.Review},
		{Action: "answer", ProviderModel: c.Actions.Answer},
	}
}

type namedProviderModel struct {
	Action string
	ProviderModel
}
