package config

import (
	"fmt"
	"os"
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

// DependencyOptions says which optional integrations this invocation needs, so their
// dependencies are checked only then: a jira section in the file must not break a run
// that does not use --jira (specs/jira/SPEC.md §7.2).
type DependencyOptions struct {
	// Jira checks that the jira section exists and its credential variables are set:
	// run --jira, and orchestrator validate whenever the section is present.
	Jira bool

	// Listener checks what orchestrator jira listen needs: the Jira section and its variables (as
	// Jira), the GitLab token variable and git (specs/jira-listener/SPEC.md §2).
	Listener bool

	// Cost says which providers report a call's cost, for limits.max_cost_usd. *provider.Registry
	// implements it. Nil means none does, so a budget is then refused.
	Cost CostChecker

	// getenv reads the environment; nil means os.Getenv. Tests inject it so they don't
	// have to mutate the process environment.
	getenv func(string) string
}

// CostChecker reports whether a provider reports the cost of its calls.
type CostChecker interface {
	ReportsCost(providerID string) bool
}

// ValidateDependencies verifies, for every provider referenced by cfg - every action,
// translation, the clarify step when it is on, and an LLM decision provider - that the corresponding CLI executable is
// present in PATH, for the Jev decision provider that its API key is set, and with
// opts.Jira that the Jira credential variables are set. It must run after Validate and
// before any provider or decision object is constructed, so a missing dependency is
// caught before any AI call or HTTP request is made - even one only needed by a later
// step (MVP.md §7).
func ValidateDependencies(cfg Config, checker DependencyChecker, opts DependencyOptions) error {
	problems := jevKeyProblems(cfg.Decision)
	pms := append(cfg.actionProviderModels(), decisionProviderModels(cfg)...)
	problems = append(problems, checkExecutablesPresent(pms, checker)...)
	problems = append(problems, sandboxDependencyProblems(cfg.Test, checker)...)
	problems = append(problems, budgetProblems(cfg, opts.Cost)...)
	problems = append(problems, storyBudgetProblems(cfg, opts.Cost)...)
	if opts.Jira || opts.Listener {
		problems = append(problems, jiraCredentialProblems(cfg.Jira, opts.getenv)...)
	}
	if opts.Listener {
		problems = append(problems, listenerCredentialProblems(cfg.GitLab, opts.getenv)...)
		if _, err := checker.LookPath("git"); err != nil {
			problems = append(problems, "the listener runs git, but executable \"git\" was not found in PATH")
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

// budgetProblems refuses limits.max_cost_usd when an action's provider reports no cost: the
// budget could then never be reached. by_complexity and review.models change the model, never
// the provider, so the actions' own routes cover them.
func budgetProblems(cfg Config, checker CostChecker) []string {
	if cfg.Limits.MaxCostUSD == nil {
		return nil
	}
	var problems []string
	for _, pm := range cfg.actionProviderModels() {
		if pm.Provider == "" || (checker != nil && checker.ReportsCost(pm.Provider)) {
			continue
		}
		problems = append(problems, fmt.Sprintf("limits.max_cost_usd cannot be enforced: %s (actions.%s) reports no cost",
			pm.Provider, pm.Action))
	}
	// The clarify call is a decision-phase call that counts against the budget (specs/clarify).
	if clarify := cfg.Clarify; clarify.Active() && clarify.Provider != "" && (checker == nil || !checker.ReportsCost(clarify.Provider)) {
		problems = append(problems, fmt.Sprintf("limits.max_cost_usd cannot be enforced: %s (clarify) reports no cost", clarify.Provider))
	}
	// So is the branch-name call (specs/delivery/SPEC.md §4.3).
	if fi := cfg.Worktree.Branch.FromInstructions; fi != nil && fi.Provider != "" && (checker == nil || !checker.ReportsCost(fi.Provider)) {
		problems = append(problems, fmt.Sprintf("limits.max_cost_usd cannot be enforced: %s (%s) reports no cost", fi.Provider, branchFromInstructionsKey))
	}
	return problems
}

// checkExecutablesPresent groups pms by provider and reports, for each provider whose
// CLI is not on PATH, which pseudo-actions it would have backed. It is shared by
// ValidateDependencies and ValidateDecisionConfigDependencies (decision.go) so both
// produce the same message shape from the same logic.
func checkExecutablesPresent(pms []namedProviderModel, checker DependencyChecker) []string {
	usage := make(map[string][]string)
	for _, pm := range pms {
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

	return problems
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

// sandboxDependencyProblems checks, when the test command is sandboxed (test.sandbox: bwrap,
// the default), that bwrap is on PATH and that
// every bind exists. Whether bwrap can actually create a sandbox on this host is probed
// separately (testexec.Probe), right after this check.
func sandboxDependencyProblems(t TestConfig, checker DependencyChecker) []string {
	if !t.Sandboxed() {
		return nil
	}
	var problems []string
	if _, err := checker.LookPath("bwrap"); err != nil {
		problems = append(problems, "the test command runs under bwrap (test.sandbox: bwrap, the default), but executable "+
			"\"bwrap\" was not found in PATH.\n\n"+
			"Install bubblewrap (e.g. apt install bubblewrap), or set test.sandbox: none to run tests unsandboxed: "+
			"the code under test, which a model wrote, then reaches the network, $HOME and your files.")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return append(problems, fmt.Sprintf("test.sandbox is bwrap, but the home directory is unknown: %v", err))
	}
	ro, rw := t.ResolveBinds(home)
	for _, b := range append(ro, rw...) {
		if _, err := os.Stat(b); err != nil {
			problems = append(problems, fmt.Sprintf("test sandbox bind %s does not exist: %v", b, err))
		}
	}
	return problems
}
