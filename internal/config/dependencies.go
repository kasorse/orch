package config

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/provider"
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

	// GitLab checks what orchestrator revise needs of the GitLab API: the gitlab section,
	// api_token_env and its variable (specs/revise/SPEC.md §2).
	GitLab bool

	// Cost says which providers report a call's cost, for limits.max_cost_usd. *provider.Registry
	// implements it. Nil means none does, so a budget is then refused.
	Cost CostChecker

	// getenv reads the environment; nil means os.Getenv. Tests inject it so they don't
	// have to mutate the process environment.
	getenv func(string) string
}

// CostChecker reports whether a provider reports the cost of its calls to a model.
type CostChecker interface {
	ReportsCost(providerID, model string) bool
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
	// A provider on the API transport runs no executable (specs/api-transport/SPEC.md §9).
	var pms []namedProviderModel
	for _, pm := range append(cfg.actionProviderModels(), decisionProviderModels(cfg)...) {
		if !cfg.Providers.UsesAPI(pm.Provider) {
			pms = append(pms, pm)
		}
	}
	problems = append(problems, checkExecutablesPresent(pms, checker)...)
	problems = append(problems, sandboxDependencyProblems(cfg.Test, checker)...)
	problems = append(problems, budgetProblems(cfg, opts.Cost)...)
	problems = append(problems, storyBudgetProblems(cfg, opts.Cost)...)
	if opts.Jira || opts.Listener {
		problems = append(problems, jiraCredentialProblems(cfg.Jira, opts.getenv)...)
	}
	if opts.GitLab {
		problems = append(problems, gitlabAPIProblems(cfg.GitLab, opts.getenv)...)
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

// budgetProblems refuses limits.max_cost_usd when a route the budget counts - an action's
// models (base, by_complexity, escalations, extra reviewers), clarify and branch naming - goes
// to a provider that reports no cost for its model: the budget could then never be reached. A
// CLI reports it for every model; an API client only for a model it has a price for
// (specs/api-transport/SPEC.md §7).
func budgetProblems(cfg Config, checker CostChecker) []string {
	if cfg.Limits.MaxCostUSD == nil {
		return nil
	}
	return costProblems(cfg, checker, "limits.max_cost_usd", func(key string) bool {
		return strings.HasPrefix(key, "actions.") || key == "clarify" || key == branchFromInstructionsKey
	})
}

// costProblems reports each route counts selects whose provider reports no cost for its model:
// for a CLI provider once per action (its answer does not depend on the model), for an API one
// once per action and model. The clarify route counts only while clarify is on, as modelRoutes
// lists it.
func costProblems(cfg Config, checker CostChecker, budget string, counts func(key string) bool) []string {
	var problems []string
	seen := map[[3]string]bool{}
	for _, r := range modelRoutes(cfg) {
		if r.Provider == "" || !counts(r.Key) {
			continue
		}
		root := r.Key
		if parts := strings.SplitN(r.Key, ".", 3); parts[0] == "actions" && len(parts) > 1 {
			root = parts[0] + "." + parts[1]
		}
		model := ""
		if cfg.Providers.UsesAPI(r.Provider) {
			model = r.Model
		}
		if id := [3]string{root, r.Provider, model}; seen[id] {
			continue
		} else {
			seen[id] = true
		}
		if checker != nil && checker.ReportsCost(r.Provider, r.Model) {
			continue
		}
		msg := fmt.Sprintf("%s cannot be enforced: %s (%s) reports no cost", budget, r.Provider, r.Key)
		if cfg.Providers.UsesAPI(r.Provider) {
			msg = fmt.Sprintf("%s cannot be enforced: %s (%s) has no price for model %s; add it to providers.%s.prices",
				budget, r.Provider, r.Key, r.Model, r.Provider)
		}
		problems = append(problems, msg)
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
