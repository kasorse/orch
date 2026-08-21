package config

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/provider"
)

// DependencyChecker wraps behavior equivalent to exec.LookPath so callers never call
// exec.LookPath directly from business logic, keeping dependency validation testable.
type DependencyChecker interface {
	LookPath(file string) (string, error)
}

type ExecDependencyChecker struct{}

func (ExecDependencyChecker) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

// FakeDependencyChecker is a test double: Missing[executable] = true simulates that
// executable not being found in PATH.
type FakeDependencyChecker struct {
	Missing map[string]bool
}

func (f FakeDependencyChecker) LookPath(file string) (string, error) {
	if f.Missing[file] {
		return "", fmt.Errorf("exec: %q: executable file not found in $PATH", file)
	}
	return "/fake/bin/" + file, nil
}

// ValidateDependencies verifies, for every provider referenced by cfg (including the
// classifier's provider), that the corresponding CLI executable is present in PATH.
// It must run after Validate and before any provider/classifier object is constructed,
// so a missing dependency is caught before any AI call is made.
func ValidateDependencies(cfg Config, checker DependencyChecker) error {
	usage := make(map[string][]string)
	for _, pm := range cfg.actionProviderModels() {
		if pm.Provider == "" {
			continue
		}
		usage[pm.Provider] = append(usage[pm.Provider], pm.Action)
	}

	providers := make([]string, 0, len(usage))
	for p := range usage {
		providers = append(providers, p)
	}
	sort.Strings(providers)

	var problems []string
	for _, p := range providers {
		exe, ok := provider.ExecutableFor(p)
		if !ok {
			continue
		}
		if _, err := checker.LookPath(exe); err == nil {
			continue
		}

		actions := usage[p]
		sort.Strings(actions)

		if len(actions) == 1 {
			problems = append(problems, fmt.Sprintf(
				"provider %q is configured for action %q,\nbut executable %q was not found in PATH.\n\nInstall the %s or update the configuration.",
				p, actions[0], exe, cliDisplayName(p),
			))
		} else {
			problems = append(problems, fmt.Sprintf(
				"provider %q is required by actions [%s],\nbut executable %q was not found in PATH.\n\nInstall the %s or update the configuration.",
				p, strings.Join(actions, ", "), exe, cliDisplayName(p),
			))
		}
	}

	if len(problems) == 0 {
		return nil
	}

	return apperr.NewDependencyError(
		fmt.Sprintf("configuration validation failed:\n\n%s", strings.Join(problems, "\n\n")),
		nil,
	)
}

func cliDisplayName(providerID string) string {
	switch providerID {
	case string(provider.Claude):
		return "Claude CLI"
	case string(provider.Codex):
		return "Codex CLI"
	case string(provider.Gemini):
		return "Gemini CLI"
	default:
		return providerID + " CLI"
	}
}
