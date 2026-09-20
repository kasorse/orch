package config

import (
	"fmt"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/provider"
)

// Validate checks structural correctness of the config: version, classifier and
// action provider/model presence, supported provider names, test command presence,
// and positive limits/timeout. It aggregates every problem found into a single
// *apperr.ConfigError rather than failing on the first issue, so a user sees the full
// list of what to fix in one pass.
func Validate(cfg Config) error {
	var problems []string

	if cfg.Version != 1 {
		problems = append(problems, fmt.Sprintf("unsupported config version %d (expected 1)", cfg.Version))
	}

	for _, pm := range cfg.actionProviderModels() {
		problems = append(problems, validateProviderModel(pm.Action, pm.ProviderModel)...)
	}

	for _, a := range cfg.actionTimeouts() {
		if a.Timeout < 0 {
			problems = append(problems, fmt.Sprintf("actions.%s.timeout must not be negative, got %s", a.Action, a.Timeout))
		}
	}

	if strings.TrimSpace(cfg.Test.Command) == "" {
		problems = append(problems, "test.command is required")
	}

	if cfg.Limits.MaxSteps <= 0 {
		problems = append(problems, fmt.Sprintf("limits.max_steps must be positive, got %d", cfg.Limits.MaxSteps))
	}
	if cfg.Limits.MaxFixIterations <= 0 {
		problems = append(problems, fmt.Sprintf("limits.max_fix_iterations must be positive, got %d", cfg.Limits.MaxFixIterations))
	}

	if cfg.Execution.Timeout <= 0 {
		problems = append(problems, "execution.timeout must be a positive duration")
	}

	if len(problems) == 0 {
		return nil
	}

	return apperr.NewConfigError(
		fmt.Sprintf("configuration validation failed:\n\n%s", strings.Join(problems, "\n")),
		nil,
	)
}

func validateProviderModel(action string, pm ProviderModel) []string {
	var problems []string

	if strings.TrimSpace(pm.Provider) == "" {
		problems = append(problems, fmt.Sprintf("%s.provider is required", action))
	} else if !provider.IsSupported(pm.Provider) {
		problems = append(problems, fmt.Sprintf(
			"%s.provider %q is not a supported provider (supported: %s)",
			action, pm.Provider, supportedProviderNames(),
		))
	}

	if strings.TrimSpace(pm.Model) == "" {
		problems = append(problems, fmt.Sprintf("%s.model is required", action))
	}

	return problems
}

func supportedProviderNames() string {
	ids := provider.SupportedProviders()
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = string(id)
	}
	return strings.Join(names, ", ")
}
