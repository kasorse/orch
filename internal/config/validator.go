package config

import (
	"fmt"
	"slices"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

// Validate checks structural correctness of the config: version, action provider/model
// presence, supported provider names, test command presence, positive limits/timeout,
// the decision surface (decision, translation, policy, context - see decision.go) and,
// when present, the jira section (jira.go).
// It aggregates every problem found into a single *apperr.ConfigError rather than
// failing on the first issue, so a user sees the full list of what to fix in one pass.
func Validate(cfg Config) error {
	var problems []string

	switch cfg.Version {
	case CurrentVersion:
	case 1:
		problems = append(problems, "unsupported config version 1 (expected 2): version 2 replaces classifier "+
			"with decision (provider, model, timeout, complexity, on_failure, jev), adds translation, policy "+
			"and context, adds actions.research, and removes actions.answer - see orchestrator.example.yaml")
	default:
		problems = append(problems, fmt.Sprintf("unsupported config version %d (expected %d)", cfg.Version, CurrentVersion))
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

	problems = append(problems, decisionProblems(cfg)...)

	problems = append(problems, sandboxProblems(cfg.Test)...)

	for i, p := range cfg.Security.ProtectedPaths {
		if err := domain.ValidateProtectedPattern(p); err != nil {
			problems = append(problems, fmt.Sprintf("security.protected_paths[%d] %q: %v", i, p, err))
		}
	}

	if cfg.Jira != nil {
		problems = append(problems, validateJira(*cfg.Jira)...)
	}

	providers := map[string]string{}
	for _, pm := range cfg.actionProviderModels() {
		providers[pm.Action] = pm.Provider
	}
	problems = append(problems, validateExtensions(cfg.Extensions, providers)...)

	if len(problems) == 0 {
		return nil
	}

	return apperr.NewConfigError(
		fmt.Sprintf("configuration validation failed:\n\n%s", strings.Join(problems, "\n")),
		nil,
	)
}

// EffortLevels are the accepted values of an action's effort, as the claude CLI names them.
var EffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

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

	if pm.Effort != "" {
		switch {
		case !slices.Contains(EffortLevels, pm.Effort):
			problems = append(problems, fmt.Sprintf("%s.effort %q must be one of: %s", action, pm.Effort, strings.Join(EffortLevels, ", ")))
		case pm.Provider != string(provider.Claude):
			problems = append(problems, fmt.Sprintf("%s.effort is only supported with provider claude, not %q", action, pm.Provider))
		}
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

func sandboxProblems(t TestConfig) []string {
	var problems []string
	switch t.Sandbox {
	case "", "none", SandboxBwrap:
	default:
		problems = append(problems, fmt.Sprintf("test.sandbox must be none or bwrap, got %q", t.Sandbox))
	}
	if !t.Sandboxed() && len(t.ROBinds)+len(t.RWBinds) > 0 {
		problems = append(problems, "test.ro_binds / test.rw_binds only apply with test.sandbox: bwrap")
	}
	for _, set := range []struct {
		key   string
		binds []string
	}{{"test.ro_binds", t.ROBinds}, {"test.rw_binds", t.RWBinds}} {
		for i, b := range set.binds {
			if err := validateBind(b); err != nil {
				problems = append(problems, fmt.Sprintf("%s[%d] %q: %v", set.key, i, b, err))
			}
		}
	}
	return problems
}

// validateBind accepts an absolute or "~/"-relative path to one directory or file. The
// root and the home directory themselves are refused: re-exposing either undoes the
// sandbox.
func validateBind(b string) error {
	rest, home := strings.CutPrefix(b, "~/")
	switch {
	case !home && !strings.HasPrefix(b, "/"):
		return fmt.Errorf("must be absolute or start with ~/")
	case slices.Contains(strings.Split(b, "/"), ".."):
		return fmt.Errorf("must not contain ..")
	case home && strings.Trim(rest, "/.") == "", !home && strings.Trim(b, "/.") == "":
		return fmt.Errorf("must not be the root or the home directory itself")
	}
	return nil
}
