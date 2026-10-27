package config

import (
	"fmt"
	"maps"
	"math"
	"path"
	"regexp"
	"slices"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
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

	if cfg.Actions.Analyze != nil {
		problems = append(problems, "actions.analyze was removed: analysis is no longer a stage, the clarify step "+
			"makes the task clear before the decision and a stage that reads the code is actions.investigate "+
			"(specs/clarify)")
	}
	if cfg.Actions.Plan != nil {
		problems = append(problems, "actions.plan was renamed actions.investigate: planning became the investigation "+
			"stage, which reads the code and plans a change or answers; move the section under the new name, "+
			"it takes the same keys, by_complexity included (specs/clarify)")
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
	if v := cfg.Limits.MaxCostUSD; v != nil && (*v <= 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
		problems = append(problems, fmt.Sprintf("limits.max_cost_usd must be positive and finite, got %v", *v))
	}

	problems = append(problems, checkProblems(cfg)...)
	problems = append(problems, byComplexityProblems(cfg)...)
	problems = append(problems, extraReviewerProblems(cfg)...)
	if len(cfg.AllowedModels) > 0 {
		problems = append(problems, allowedModelProblems(cfg)...)
	}

	problems = append(problems, promptLimitProblems(cfg.Limits.Prompt)...)
	problems = append(problems, verifyProblems(cfg)...)
	problems = append(problems, escalateProblems(cfg)...)

	if cfg.Execution.Timeout <= 0 {
		problems = append(problems, "execution.timeout must be a positive duration")
	}
	if cfg.Execution.RateLimitWait < 0 {
		problems = append(problems, "execution.rate_limit_wait must not be negative (0 turns waiting off)")
	}

	switch cfg.Execution.Isolation {
	case "", IsolationWorktree, IsolationInplace:
	default:
		problems = append(problems, fmt.Sprintf("execution.isolation must be inplace (the default) or worktree, got %q", cfg.Execution.Isolation))
	}
	if cfg.Execution.Isolation == IsolationInplace && strings.TrimSpace(cfg.Worktree.Setup) != "" {
		problems = append(problems, "worktree.setup needs execution.isolation: worktree; with inplace it would never run")
	}

	problems = append(problems, branchProblems(cfg.Worktree.Branch)...)
	problems = append(problems, decisionProblems(cfg)...)
	problems = append(problems, pathRuleProblems(cfg)...)
	problems = append(problems, providersProblems(cfg)...)

	problems = append(problems, sandboxProblems(cfg.Test)...)
	problems = append(problems, envPassProblems(cfg)...)
	switch cfg.Test.Baseline {
	case "", BaselineRequireGreen, BaselineOff:
	default:
		problems = append(problems, fmt.Sprintf("test.baseline must be require_green (the default) or off, got %q", cfg.Test.Baseline))
	}

	switch cfg.Security.SecretScan.Mode {
	case "", SecretScanStop, SecretScanWarn, SecretScanOff:
	default:
		problems = append(problems, fmt.Sprintf("security.secret_scan.mode must be stop (the default), warn or off, got %q", cfg.Security.SecretScan.Mode))
	}
	for i, p := range cfg.Security.SecretScan.AllowPaths {
		if err := domain.ValidateProtectedPattern(p); err != nil {
			problems = append(problems, fmt.Sprintf("security.secret_scan.allow_paths[%d] %q: %v", i, p, err))
		}
	}

	for i, p := range cfg.Security.ProtectedPaths {
		if err := domain.ValidateProtectedPattern(p); err != nil {
			problems = append(problems, fmt.Sprintf("security.protected_paths[%d] %q: %v", i, p, err))
		}
	}

	if cfg.Jira != nil {
		problems = append(problems, validateJira(*cfg.Jira)...)
	}
	if cfg.Story != nil {
		problems = append(problems, validateStory(*cfg.Story)...)
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

// branchProblems validates worktree.branch (specs/delivery/SPEC.md §4.3): a template with only the
// placeholders {id}, {key}, {slug} and {date}, and a route with a provider, a model and a positive
// timeout for the call that reads the instructions.
func branchProblems(b BranchConfig) []string {
	var problems []string
	if b.Template != "" {
		if err := domain.CheckBranchTemplate(b.Template); err != nil {
			problems = append(problems, fmt.Sprintf("worktree.branch.template %q: %v", b.Template, err))
		}
	}
	if fi := b.FromInstructions; fi != nil {
		problems = append(problems, validateProviderModel(branchFromInstructionsKey, fi.ProviderModel)...)
		if fi.Timeout <= 0 {
			problems = append(problems, branchFromInstructionsKey+".timeout must be a positive duration")
		}
	}
	return problems
}

// verifyProblems validates actions.implement.verify and limits.verify (specs/quality/SPEC.md
// §6.3). The key belongs to implement alone; the model triggers these runs, so they need
// the sandbox, and the server is built into the claude adapter's MCP support.
func verifyProblems(cfg Config) []string {
	var problems []string
	others := []struct {
		name string
		a    ActionConfig
	}{
		{"research", cfg.Actions.Research},
		{"investigate", cfg.Actions.Investigate},
		{"review", cfg.Actions.Review},
	}
	for _, o := range others {
		if o.a.verifySet || o.a.Verify {
			problems = append(problems, fmt.Sprintf("actions.%s.verify: unknown key (verify is accepted only on actions.implement)", o.name))
		}
	}
	if cfg.Actions.Implement.Verify {
		if strings.TrimSpace(cfg.Test.Command) == "" {
			problems = append(problems, "actions.implement.verify needs test.command: it is the command the verify tool runs")
		}
		if !cfg.Test.Sandboxed() {
			problems = append(problems, "actions.implement.verify needs test.sandbox bwrap: the model triggers these runs, so they never run unsandboxed")
		}
		if p := cfg.Actions.Implement.Provider; p != "" && p != "claude" && p != "codex" {
			problems = append(problems, fmt.Sprintf("actions.implement.verify is only supported with provider claude or codex, got %s", p))
		}
	}
	v := cfg.Limits.Verify
	if v.MaxCallsPerStep != nil && (*v.MaxCallsPerStep < 1 || *v.MaxCallsPerStep > domain.MaxVerifyCalls) {
		problems = append(problems, fmt.Sprintf("limits.verify.max_calls_per_step must be between 1 and %d, got %d", domain.MaxVerifyCalls, *v.MaxCallsPerStep))
	}
	if v.Timeout() <= 0 {
		problems = append(problems, fmt.Sprintf("limits.verify.call_timeout must be a positive duration, got %s", v.Timeout()))
	}
	return problems
}

// escalateProblems validates actions.implement.escalate (specs/decision-shape/SPEC.md §8): the
// key belongs to implement alone, and it needs by_complexity, the table it climbs.
func escalateProblems(cfg Config) []string {
	var problems []string
	for _, o := range []struct {
		name string
		a    ActionConfig
	}{
		{"research", cfg.Actions.Research},
		{"investigate", cfg.Actions.Investigate},
		{"review", cfg.Actions.Review},
	} {
		if o.a.escalateSet {
			problems = append(problems, fmt.Sprintf("actions.%s.escalate: unknown key (escalate is accepted only on actions.implement)", o.name))
		}
	}
	if cfg.Actions.Implement.Escalate && len(cfg.Actions.Implement.ByComplexity) == 0 {
		problems = append(problems, "actions.implement.escalate needs actions.implement.by_complexity: a fix runs on the route of the level above")
	}
	return problems
}

// checkProblems validates checks (specs/quality/SPEC.md §5.1): at most domain.MaxChecks,
// each with a name matching domain.CheckNameRE, unique, and a command; and test.command,
// since checks run inside the test step.
func checkProblems(cfg Config) []string {
	var problems []string
	if len(cfg.Checks) > domain.MaxChecks {
		problems = append(problems, fmt.Sprintf("checks: %d checks, at most %d", len(cfg.Checks), domain.MaxChecks))
	}
	if len(cfg.Checks) > 0 && strings.TrimSpace(cfg.Test.Command) == "" {
		problems = append(problems, "checks need test.command: they run inside the test step, which needs its command")
	}
	seen := map[string]bool{}
	for i, c := range cfg.Checks {
		if !domain.CheckNameRE.MatchString(c.Name) {
			problems = append(problems, fmt.Sprintf("checks[%d].name %q must match %s", i, c.Name, domain.CheckNameRE))
		} else if seen[c.Name] {
			problems = append(problems, fmt.Sprintf("checks[%d].name %q is used by an earlier check", i, c.Name))
		}
		seen[c.Name] = true
		if strings.TrimSpace(c.Command) == "" {
			problems = append(problems, fmt.Sprintf("checks[%d].command is required", i))
		}
	}
	return problems
}

// byComplexityProblems validates every action's by_complexity (specs/quality/SPEC.md §9.1):
// the keys are complexities, each entry names a model or an effort, and an effort follows
// the rules of the action's own effort - a level the CLI knows, on a provider that accepts it.
// An entry's escalate (specs/decision-shape/SPEC.md §8.1) follows the same rules, on implement
// only.
func byComplexityProblems(cfg Config) []string {
	var problems []string
	for _, a := range []struct {
		name   string
		action ActionConfig
	}{
		{"research", cfg.Actions.Research},
		{"investigate", cfg.Actions.Investigate},
		{"implement", cfg.Actions.Implement},
		{"review", cfg.Actions.Review},
	} {
		levels := make([]string, 0, len(a.action.ByComplexity))
		for c := range a.action.ByComplexity {
			levels = append(levels, string(c))
		}
		slices.Sort(levels)
		for _, level := range levels {
			key := fmt.Sprintf("actions.%s.by_complexity.%s", a.name, level)
			if _, err := domain.ParseComplexity(level); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %q is not a complexity (want %s)", key, level, complexityNames()))
				continue
			}
			entry := a.action.ByComplexity[domain.Complexity(level)]
			problems = append(problems, modelEffortProblems(key, entry.Model, entry.Effort, a.action.Provider)...)
			switch {
			case entry.Escalate == nil:
			case a.name != "implement":
				problems = append(problems, fmt.Sprintf("%s.escalate: unknown key (escalate is accepted only on actions.implement)", key))
			default:
				problems = append(problems, modelEffortProblems(key+".escalate", entry.Escalate.Model, entry.Escalate.Effort, a.action.Provider)...)
			}
		}
	}
	return problems
}

// modelEffortProblems validates one by_complexity entry or its escalate at key: it names a model
// or an effort, and an effort is a level the CLI knows, on a provider that accepts it.
func modelEffortProblems(key, model, effort, prov string) []string {
	switch {
	case strings.TrimSpace(model) == "" && effort == "":
		return []string{key + " needs a model or an effort"}
	case effort == "":
		return nil
	case !slices.Contains(EffortLevels, effort):
		return []string{fmt.Sprintf("%s.effort %q must be one of: %s", key, effort, strings.Join(EffortLevels, ", "))}
	case prov != string(provider.Claude) && prov != string(provider.Codex):
		return []string{fmt.Sprintf("%s.effort is only supported with provider claude or codex, not %q", key, prov)}
	}
	return nil
}

// extraReviewerProblems validates actions.review.models (specs/quality/SPEC.md §7.5): at most
// MaxExtraReviewers non-empty names, none repeated and none the base model or one of the
// review by_complexity models, which would only ask the same question twice. The key belongs to review alone.
func extraReviewerProblems(cfg Config) []string {
	var problems []string
	for _, o := range []struct {
		name string
		a    ActionConfig
	}{
		{"research", cfg.Actions.Research},
		{"investigate", cfg.Actions.Investigate},
		{"implement", cfg.Actions.Implement},
	} {
		if o.a.modelsSet || len(o.a.Models) > 0 {
			problems = append(problems, fmt.Sprintf("actions.%s.models: unknown key (models is accepted only on actions.review)", o.name))
		}
	}
	review := cfg.Actions.Review
	if len(review.Models) > MaxExtraReviewers {
		problems = append(problems, fmt.Sprintf("actions.review.models: %d models, at most %d", len(review.Models), MaxExtraReviewers))
	}
	// The base reviewer is the base model, or the by_complexity model the decided complexity
	// routes to, so an extra reviewer must differ from every one of them.
	seen := map[string]bool{strings.TrimSpace(review.Model): true}
	for _, e := range review.ByComplexity {
		if name := strings.TrimSpace(e.Model); name != "" {
			seen[name] = true
		}
	}
	for i, m := range review.Models {
		name := strings.TrimSpace(m)
		switch {
		case name == "":
			problems = append(problems, fmt.Sprintf("actions.review.models[%d] must not be empty", i))
		case seen[name]:
			problems = append(problems, fmt.Sprintf("actions.review.models[%d] %q repeats the base model, a by_complexity model or an earlier entry", i, m))
		}
		seen[name] = true
	}
	return problems
}

// allowedModelProblems checks every model the run can call against the org's allowed_models
// (specs/governance/SPEC.md §6.2): each action's base model and by_complexity models, the
// extra reviewers, the translator, the clarify step, the LLM decision provider and the model variables of
// providers.claude.api_env. A route with no model is an
// error, because the list cannot check the CLI's default. A by_complexity entry with no model
// inherits the base, which is checked on its own; an empty extra reviewer is already refused
// by extraReviewerProblems.
func allowedModelProblems(cfg Config) []string {
	var problems []string
	check := func(key, model string) {
		switch model = strings.TrimSpace(model); {
		case model == "":
			problems = append(problems, fmt.Sprintf("%s: no model is named; allowed_models (%s) cannot check the CLI default", key, cfg.AllowedModelsFrom))
		case !slices.Contains(cfg.AllowedModels, model):
			problems = append(problems, fmt.Sprintf("%s: model %s is not in allowed_models (%s)", key, model, cfg.AllowedModelsFrom))
		}
	}
	for _, a := range []struct {
		name   string
		action ActionConfig
	}{
		{"research", cfg.Actions.Research},
		{"investigate", cfg.Actions.Investigate},
		{"implement", cfg.Actions.Implement},
		{"review", cfg.Actions.Review},
	} {
		key := "actions." + a.name
		check(key, a.action.Model)
		levels := make([]string, 0, len(a.action.ByComplexity))
		for c := range a.action.ByComplexity {
			levels = append(levels, string(c))
		}
		slices.Sort(levels)
		for _, level := range levels {
			entry := a.action.ByComplexity[domain.Complexity(level)]
			if strings.TrimSpace(entry.Model) != "" {
				check(key+".by_complexity."+level, entry.Model)
			}
			if entry.Escalate != nil && strings.TrimSpace(entry.Escalate.Model) != "" {
				check(key+".by_complexity."+level+".escalate", entry.Escalate.Model)
			}
		}
		for i, m := range a.action.Models {
			if strings.TrimSpace(m) != "" {
				check(fmt.Sprintf("%s.models[%d]", key, i), m)
			}
		}
	}
	check("translation", cfg.Translation.Model)
	if cfg.Clarify.Active() {
		check("clarify", cfg.Clarify.Model)
	}
	if fi := cfg.Worktree.Branch.FromInstructions; fi != nil {
		check(branchFromInstructionsKey, fi.Model)
	}
	if !cfg.Decision.UsesJev() {
		check("decision", cfg.Decision.Model)
	}
	// A model variable in providers.claude.api_env changes the model a route reaches in api
	// mode (specs/claude-auth/SPEC.md §17.3, D8). An empty value is apiEnvProblems' to report.
	apiEnv := cfg.Providers.Claude.APIEnv
	for _, name := range slices.Sorted(maps.Keys(apiEnv)) {
		if domain.ClaudeModelVar(name) && strings.TrimSpace(apiEnv[name]) != "" {
			check("providers.claude.api_env."+name, apiEnv[name])
		}
	}
	return problems
}

func complexityNames() string {
	var names []string
	for _, c := range domain.ComplexityOrder() {
		names = append(names, string(c))
	}
	return strings.Join(names, ", ")
}

// EffortLevels are the values accepted by the Claude and Codex CLI adapters.
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
		case pm.Provider != string(provider.Claude) && pm.Provider != string(provider.Codex):
			problems = append(problems, fmt.Sprintf("%s.effort is only supported with provider claude or codex, not %q", action, pm.Provider))
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

func promptLimitProblems(p PromptLimitsConfig) []string {
	var problems []string
	for _, l := range []struct {
		key      string
		value    *int
		min, max int
	}{
		{"limits.prompt.max_diff_bytes", p.MaxDiffBytes, MinMaxDiffBytes, MaxMaxDiffBytes},
		{"limits.prompt.max_test_output_bytes", p.MaxTestOutputBytes, MinMaxTestOutputBytes, MaxMaxTestOutputBytes},
	} {
		if l.value != nil && (*l.value < l.min || *l.value > l.max) {
			problems = append(problems, fmt.Sprintf("%s must be between %d and %d, got %d", l.key, l.min, l.max, *l.value))
		}
	}
	for i, g := range p.DiffSummarize {
		if strings.TrimSpace(g) == "" {
			problems = append(problems, fmt.Sprintf("limits.prompt.diff_summarize[%d] must not be empty", i))
			continue
		}
		// path.Match reports a malformed pattern even when the name does not match.
		if _, err := path.Match(g, ""); err != nil {
			problems = append(problems, fmt.Sprintf("limits.prompt.diff_summarize[%d] %q: %v", i, g, err))
		}
	}
	return problems
}

// envNameRE is a portable environment variable name.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// credentialSubstrings and credentialSegments make a test.env_pass name look like a
// credential (specs/hardening/SPEC.md §6.2): a substring anywhere in the upper-cased name,
// or a whole "_"-separated segment. AUTH, KEY and PASS are segments only, so GIT_AUTHOR_NAME
// or KEYBOARD_LAYOUT pass while SSH_AUTH_SOCK, STRIPE_KEY and DB_PASS do not. There is no
// override: a test that needs a credential does not belong in a run that executes
// model-written code.
var (
	credentialSubstrings = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "APIKEY", "PRIVATEKEY"}
	credentialSegments   = []string{"AUTH", "KEY", "KEYS", "PASS", "COOKIE"}
)

// credentialLike returns why name looks like a credential, or "".
func credentialLike(name string) string {
	upper := strings.ToUpper(name)
	for _, sub := range credentialSubstrings {
		if strings.Contains(upper, sub) {
			return "contains " + sub
		}
	}
	for _, seg := range strings.Split(upper, "_") {
		if slices.Contains(credentialSegments, seg) {
			return "has the segment " + seg
		}
	}
	return ""
}

func envPassProblems(cfg Config) []string {
	var problems []string
	credentials := cfg.CredentialEnvNames()
	for i, name := range cfg.Test.EnvPass {
		key := fmt.Sprintf("test.env_pass[%d] %q", i, name)
		switch {
		case !envNameRE.MatchString(name):
			problems = append(problems, key+": must be an exact environment variable name (letters, digits, _; no wildcards)")
		case slices.Contains(credentials, name):
			problems = append(problems, key+": is a credential this config names; the test command never gets it")
		case execution.IsParentSessionVar(name):
			problems = append(problems, key+": belongs to the agent session that launched the orchestrator; no child process gets it")
		default:
			if why := credentialLike(name); why != "" {
				problems = append(problems, key+": looks like a credential ("+why+"); the test command runs model-written "+
					"code and never gets credentials")
			}
		}
	}
	return problems
}

func sandboxProblems(t TestConfig) []string {
	var problems []string
	switch t.Sandbox {
	case "", SandboxNone, SandboxBwrap:
	default:
		problems = append(problems, fmt.Sprintf("test.sandbox must be bwrap (the default) or none, got %q", t.Sandbox))
	}
	if !t.Sandboxed() && len(t.ROBinds)+len(t.RWBinds) > 0 {
		problems = append(problems, "test.ro_binds / test.rw_binds only apply under the sandbox (test.sandbox: bwrap, the default), not with none")
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
