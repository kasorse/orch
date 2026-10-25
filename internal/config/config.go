// Package config defines the orchestrator's YAML configuration schema and the
// validation/dependency-checking that must run, in full, before any AI call or
// repository modification (see Validate and ValidateDependencies).
package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

type ProviderModel struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	// Effort is the optional reasoning-effort level for the model (low, medium, high,
	// xhigh, max). Empty means the CLI's own default. Only providers that can enforce it
	// accept it (see EffortProviders); it is never silently dropped.
	Effort string `yaml:"effort"`
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
	// Verify gives the implement session the orchestrator's verify tools, which run
	// test.command and the checks (specs/quality/SPEC.md §6). Only actions.implement accepts
	// it; Validate rejects the key anywhere else.
	Verify bool
	// ByComplexity names a model and/or effort per decided complexity (specs/quality/SPEC.md
	// §9). The provider is fixed per action. A key that is not a complexity is kept as
	// written, so Validate can name it; read the table through Route.
	ByComplexity map[domain.Complexity]ModelEffort
	// Escalate routes an implement that fixes a failed test or review as the next complexity
	// up (specs/decision-shape/SPEC.md §8), for a level whose entry names no escalate of its
	// own. Only actions.implement accepts it, and only with ByComplexity; read it through
	// ActionsConfig.EscalatedRoute.
	Escalate bool
	// Models are the extra reviewers of actions.review: model names on the action's provider,
	// called one after another after the base model with the same prompt (specs/quality/SPEC.md
	// §7.5). Only actions.review accepts it; Validate rejects the key anywhere else.
	Models []string

	// verifySet and modelsSet record that the key was written, so Validate can name it on an
	// action that does not accept it.
	verifySet   bool
	modelsSet   bool
	escalateSet bool
}

// MaxExtraReviewers bounds actions.review.models.
const MaxExtraReviewers = 4

// ModelEffort is one by_complexity entry. A field left empty falls back to the action's
// base value. There is no provider: the strict parser rejects the key.
type ModelEffort struct {
	Model  string `yaml:"model"`
	Effort string `yaml:"effort"`
	// Escalate is the route of an implement at this level that fixes a failed test or review
	// (specs/decision-shape/SPEC.md §8.1): its non-empty fields override the entry's. Only
	// actions.implement accepts it.
	Escalate *Escalation `yaml:"escalate"`
}

// Escalation is a by_complexity entry's escalate: a model and/or an effort, nothing nested.
type Escalation struct {
	Model  string `yaml:"model"`
	Effort string `yaml:"effort"`
}

// Route is the provider, model and effort for an action under the decided complexity c, and
// where they came from. A manual decision (run --stage) and a complexity without an entry
// get the base; otherwise the entry's non-empty fields override it. The provider is always
// the base one.
func (a ActionConfig) Route(c domain.Complexity, manual bool) (ProviderModel, string) {
	if manual || c == "" {
		return a.ProviderModel, domain.ModelSourceBase
	}
	entry, ok := a.ByComplexity[c]
	if !ok {
		return a.ProviderModel, domain.ModelSourceBase
	}
	pm := a.ProviderModel
	if entry.Model != "" {
		pm.Model = entry.Model
	}
	if entry.Effort != "" {
		pm.Effort = entry.Effort
	}
	return pm, domain.ModelSourceByComplexity + string(c)
}

// escalatedRoute is the route of a fix at complexity c. The entry of c with its own escalate
// gives its route overridden by the escalate, with the source by_complexity.<c>.escalate.
// Otherwise Escalate gives the route one complexity above c, with the source
// escalated.<level>; ok is false without Escalate, for complex (nothing above it), and when the
// level above has no by_complexity entry, so a fix never runs on something other than a
// configured route.
func (a ActionConfig) escalatedRoute(c domain.Complexity) (ProviderModel, string, bool) {
	if entry, ok := a.ByComplexity[c]; ok && entry.Escalate != nil {
		pm, _ := a.Route(c, false)
		if entry.Escalate.Model != "" {
			pm.Model = entry.Escalate.Model
		}
		if entry.Escalate.Effort != "" {
			pm.Effort = entry.Escalate.Effort
		}
		return pm, domain.ModelSourceByComplexity + string(c) + domain.ModelSourceEscalateSuffix, true
	}
	if !a.Escalate {
		return ProviderModel{}, "", false
	}
	order := domain.ComplexityOrder()
	i := slices.Index(order, c)
	if i < 0 || i+1 >= len(order) {
		return ProviderModel{}, "", false
	}
	up := order[i+1]
	if _, ok := a.ByComplexity[up]; !ok {
		return ProviderModel{}, "", false
	}
	pm, _ := a.Route(up, false)
	return pm, domain.ModelSourceEscalated + string(up), true
}

// UnmarshalYAML parses ActionConfig's own provider/model/timeout keys directly (rather
// than relying on yaml.v3's inline-embedding tag) so Timeout can go through the same
// duration-string parsing as ExecutionConfig.Timeout.
func (a *ActionConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
		Effort   string `yaml:"effort"`
		Timeout  string `yaml:"timeout"`
		Verify   *bool  `yaml:"verify"`
		Escalate *bool  `yaml:"escalate"`

		// Models are pointers because yaml.v3 drops a null entry of a []string without a
		// word; a pointer keeps it visible, so it can be refused.
		Models []*string `yaml:"models"`

		// ByComplexity keeps its keys as strings so Validate can report one that is not a
		// complexity; ModelEffort has no provider key, so the strict parser rejects one.
		ByComplexity map[string]ModelEffort `yaml:"by_complexity"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	a.Provider = raw.Provider
	a.Model = raw.Model
	a.Effort = raw.Effort
	a.Verify = raw.Verify != nil && *raw.Verify
	a.verifySet = raw.Verify != nil
	a.Escalate = raw.Escalate != nil && *raw.Escalate
	a.escalateSet = raw.Escalate != nil
	a.Models = nil
	if raw.Models != nil {
		a.Models = make([]string, len(raw.Models))
		for i, m := range raw.Models {
			if m == nil {
				return fmt.Errorf("models[%d] must not be null or empty", i)
			}
			a.Models[i] = *m
		}
	}
	a.modelsSet = raw.Models != nil
	a.ByComplexity = nil
	if raw.ByComplexity != nil {
		a.ByComplexity = make(map[domain.Complexity]ModelEffort, len(raw.ByComplexity))
		for k, v := range raw.ByComplexity {
			a.ByComplexity[domain.Complexity(k)] = v
		}
	}
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

// ActionsConfig is the actions section. Analyze and Plan are declared only so strict decoding
// accepts the keys and the validator can reject them with the replacement: the analyze action is
// gone and plan became investigate (specs/clarify), and a route kept under the old name would
// silently mean something else.
type ActionsConfig struct {
	Research    ActionConfig `yaml:"research"`
	Analyze     any          `yaml:"analyze"`
	Plan        any          `yaml:"plan"`
	Investigate ActionConfig `yaml:"investigate"`
	Implement   ActionConfig `yaml:"implement"`
	Review      ActionConfig `yaml:"review"`
}

// For returns the configuration of a workflow action that makes a provider call; ok is false
// for one that does not (test).
func (a ActionsConfig) For(t domain.ActionType) (ac ActionConfig, ok bool) {
	switch t {
	case domain.ActionResearch:
		return a.Research, true
	case domain.ActionInvestigate:
		return a.Investigate, true
	case domain.ActionImplement:
		return a.Implement, true
	case domain.ActionReview:
		return a.Review, true
	}
	return ActionConfig{}, false
}

// Route is the model an action runs with under a run's decision: ActionConfig.Route with the
// decision's complexity, and a manual decision (run --stage) or none getting the base. ok is
// false for an action that makes no provider call.
func (a ActionsConfig) Route(t domain.ActionType, d *domain.Decision) (pm ProviderModel, source string, ok bool) {
	ac, ok := a.For(t)
	if !ok {
		return ProviderModel{}, "", false
	}
	if d == nil {
		pm, source = ac.Route("", true)
	} else {
		pm, source = ac.Route(d.Complexity.Selected, d.Source == domain.DecisionSourceManual)
	}
	return pm, source, true
}

// EscalatedRoute is the route of an implement that fixes a failed test or review, when
// one is configured: the decided level's by_complexity escalate, or under
// actions.implement.escalate the by_complexity entry one level above the decided complexity. ok is false for every other action, a manual decision (run --stage) and when
// there is no level above with an entry; the caller then keeps Route.
func (a ActionsConfig) EscalatedRoute(t domain.ActionType, d *domain.Decision) (pm ProviderModel, source string, ok bool) {
	if t != domain.ActionImplement || d == nil || d.Source == domain.DecisionSourceManual {
		return ProviderModel{}, "", false
	}
	return a.Implement.escalatedRoute(d.Complexity.Selected)
}

type TestConfig struct {
	Command string `yaml:"command"`
	// Sandbox is "bwrap" (or empty, the default): run the command under bubblewrap with the
	// filesystem read-only, $HOME hidden and no network (specs/permissions/SPEC.md §8) -
	// or "none", written out, to run it unsandboxed.
	Sandbox string `yaml:"sandbox"`
	// ROBinds and RWBinds re-expose paths inside the sandbox - absolute or "~/"-relative,
	// e.g. a module cache read-only and a build cache writable. Only valid with bwrap.
	ROBinds []string `yaml:"ro_binds"`
	RWBinds []string `yaml:"rw_binds"`
	// EnvPass names environment variables the test command inherits on top of the default
	// allowlist (execution.TestEnv; specs/hardening/SPEC.md §6.2). Exact names; a name that
	// looks like a credential is refused.
	EnvPass []string `yaml:"env_pass"`
	// Baseline is require_green (or empty, the default) or off: whether a workflow with
	// implement and test runs the test command once before the first step and refuses a red
	// suite (specs/hardening/SPEC.md §8). Read it through PreRunTest.
	Baseline string `yaml:"baseline"`
}

// CheckConfig is one operator-defined deterministic check (specs/quality/SPEC.md §5): a
// command the test step runs before test.command, and the pre-run test too. Like
// test.command it is trusted operator configuration and goes through `sh -c`.
type CheckConfig struct {
	Name    string `yaml:"name"`
	Command string `yaml:"command"`
}

// test.baseline values.
const (
	BaselineRequireGreen = "require_green"
	BaselineOff          = "off"
)

// PreRunTest reports whether the test command runs before the first step of a workflow
// that implements and tests: unless test.baseline is off, it does.
func (t TestConfig) PreRunTest() bool { return t.Baseline != BaselineOff }

// SandboxBwrap is the one sandbox test.sandbox currently supports, and the default;
// SandboxNone runs the test command unsandboxed and must be written out
// (specs/hardening/SPEC.md §6.3).
const (
	SandboxBwrap = "bwrap"
	SandboxNone  = "none"
)

// Sandboxed reports whether the test command runs under a sandbox: unless test.sandbox
// is explicitly none, it does.
func (t TestConfig) Sandboxed() bool { return t.Sandbox == "" || t.Sandbox == SandboxBwrap }

// ResolveBinds expands "~/" against home and cleans every bind path.
func (t TestConfig) ResolveBinds(home string) (ro, rw []string) {
	resolve := func(paths []string) []string {
		out := make([]string, 0, len(paths))
		for _, p := range paths {
			if rest, ok := strings.CutPrefix(p, "~/"); ok {
				p = filepath.Join(home, rest)
			}
			out = append(out, filepath.Clean(p))
		}
		return out
	}
	return resolve(t.ROBinds), resolve(t.RWBinds)
}

type LimitsConfig struct {
	MaxSteps         int `yaml:"max_steps"`
	MaxFixIterations int `yaml:"max_fix_iterations"`
	// StopWithoutProgress is a pointer so an absent key (nil) can default to true while an
	// explicit false still means false; read it through StopsWithoutProgress.
	StopWithoutProgress *bool `yaml:"stop_without_progress"`
	// MaxCostUSD is the budget of the workflow steps (specs/governance/SPEC.md §5): optional,
	// nil means no budget. Only the steps' cost counts, not the decision phase's.
	MaxCostUSD *float64 `yaml:"max_cost_usd"`
	// Prompt bounds the repository data embedded in a prompt (specs/hardening/SPEC.md §5).
	// The section and each key are optional.
	Prompt PromptLimitsConfig `yaml:"prompt"`
	// Verify bounds the verify tools of the implement step (specs/quality/SPEC.md §6.3).
	Verify VerifyLimitsConfig `yaml:"verify"`
}

// StopsWithoutProgress reports whether the fix loop stops when an iteration makes no progress
// (specs/quality/SPEC.md §8.1): limits.stop_without_progress, true when absent.
func (l LimitsConfig) StopsWithoutProgress() bool {
	return l.StopWithoutProgress == nil || *l.StopWithoutProgress
}

// Defaults of limits.verify.
const (
	DefaultVerifyMaxCalls    = 8
	DefaultVerifyCallTimeout = 10 * time.Minute
)

// VerifyLimitsConfig is limits.verify. Both keys are optional; read them through MaxCalls and
// Timeout. MaxCallsPerStep is a pointer so an explicit 0 is refused as out of range instead
// of reading as unset.
type VerifyLimitsConfig struct {
	MaxCallsPerStep *int
	CallTimeout     time.Duration

	// callTimeoutSet records that call_timeout was written, so an explicit 0s is refused.
	callTimeoutSet bool
}

// UnmarshalYAML parses call_timeout through the same duration-string parsing as the other
// timeouts.
func (v *VerifyLimitsConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		MaxCallsPerStep *int   `yaml:"max_calls_per_step"`
		CallTimeout     string `yaml:"call_timeout"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	d, err := parseDurationField("limits.verify.call_timeout", raw.CallTimeout)
	if err != nil {
		return err
	}
	v.MaxCallsPerStep = raw.MaxCallsPerStep
	v.CallTimeout = d
	v.callTimeoutSet = raw.CallTimeout != ""
	return nil
}

// MaxCalls is the most verify calls one implement step may make: max_calls_per_step, or its
// default.
func (v VerifyLimitsConfig) MaxCalls() int {
	if v.MaxCallsPerStep == nil {
		return DefaultVerifyMaxCalls
	}
	return *v.MaxCallsPerStep
}

// Timeout is the bound on one verify call: call_timeout, or its default.
func (v VerifyLimitsConfig) Timeout() time.Duration {
	if v.CallTimeout == 0 && !v.callTimeoutSet {
		return DefaultVerifyCallTimeout
	}
	return v.CallTimeout
}

// Defaults and ranges of limits.prompt (specs/hardening/SPEC.md §5.1).
const (
	DefaultMaxDiffBytes       = 262144
	MinMaxDiffBytes           = 16 << 10
	MaxMaxDiffBytes           = 4 << 20
	DefaultMaxTestOutputBytes = 32768
	MinMaxTestOutputBytes     = 4 << 10
	MaxMaxTestOutputBytes     = 1 << 20
)

// PromptLimitsConfig is limits.prompt. The byte limits are pointers so an absent key (nil)
// takes the default while an explicit 0 is still refused as out of range; read them through
// DiffBytes and TestOutputBytes.
type PromptLimitsConfig struct {
	MaxDiffBytes       *int `yaml:"max_diff_bytes"`
	MaxTestOutputBytes *int `yaml:"max_test_output_bytes"`
	// DiffSummarize adds globs to the files the review diff shows as a stat line only.
	DiffSummarize []string `yaml:"diff_summarize"`
}

// DiffBytes is the review diff's budget: max_diff_bytes, or its default.
func (p PromptLimitsConfig) DiffBytes() int {
	if p.MaxDiffBytes == nil {
		return DefaultMaxDiffBytes
	}
	return *p.MaxDiffBytes
}

// TestOutputBytes is the budget for a failing test's output in the implement prompt:
// max_test_output_bytes, or its default.
func (p PromptLimitsConfig) TestOutputBytes() int {
	if p.MaxTestOutputBytes == nil {
		return DefaultMaxTestOutputBytes
	}
	return *p.MaxTestOutputBytes
}

// ExecutionConfig.Timeout is parsed from a duration string like "15m" via a custom
// UnmarshalYAML, since YAML has no native duration type.
type ExecutionConfig struct {
	Timeout time.Duration `yaml:"timeout"`
	// Isolation is where a workflow with implement runs: inplace (the default) or worktree
	// (specs/worktree/SPEC.md §6); run --inplace and run --worktree override it for one run.
	// Empty when absent, so Validate can tell a written inplace; read it through
	// IsolationMode.
	Isolation string `yaml:"isolation"`
	// RateLimitWait turns on waiting for a step the account's usage limit refused: the run
	// sleeps until the reset time the CLI reported, or this long when it reported none, and
	// retries the step. 0 fails the run at once; absent means DefaultRateLimitWait.
	RateLimitWait time.Duration `yaml:"rate_limit_wait"`
}

// execution.isolation values.
const (
	IsolationWorktree = "worktree"
	IsolationInplace  = "inplace"
)

// IsolationMode is execution.isolation, inplace when absent.
func (e ExecutionConfig) IsolationMode() string {
	if e.Isolation == "" {
		return IsolationInplace
	}
	return e.Isolation
}

// DefaultRateLimitWait is execution.rate_limit_wait when the key is absent.
const DefaultRateLimitWait = 5 * time.Minute

func (e *ExecutionConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Timeout       string `yaml:"timeout"`
		Isolation     string `yaml:"isolation"`
		RateLimitWait string `yaml:"rate_limit_wait"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	d, err := parseDurationField("execution.timeout", raw.Timeout)
	if err != nil {
		return err
	}
	e.Timeout = d
	e.Isolation = raw.Isolation
	e.RateLimitWait = DefaultRateLimitWait
	if raw.RateLimitWait != "" {
		if e.RateLimitWait, err = parseDurationField("execution.rate_limit_wait", raw.RateLimitWait); err != nil {
			return err
		}
	}
	return nil
}

// WorktreeConfig is the top-level worktree section (specs/worktree/SPEC.md §6).
type WorktreeConfig struct {
	// Setup is a trusted operator command run once in a new worktree before any model step,
	// like test.command. It only applies to a worktree run (execution.isolation: worktree or
	// run --worktree).
	Setup string `yaml:"setup"`
}

// WorktreeRun reports whether a run of wf happens in its own git worktree: isolation is
// worktree and the workflow contains implement. A workflow without implement runs in place
// (specs/worktree/SPEC.md §6, O3).
func (c Config) WorktreeRun(wf domain.Workflow) bool {
	return c.Execution.IsolationMode() == IsolationWorktree && wf.Contains(domain.ActionImplement)
}

// CurrentVersion is the only config version this build accepts.
const CurrentVersion = 2

type Config struct {
	Version   int             `yaml:"version"`
	Actions   ActionsConfig   `yaml:"actions"`
	Test      TestConfig      `yaml:"test"`
	Limits    LimitsConfig    `yaml:"limits"`
	Execution ExecutionConfig `yaml:"execution"`

	// Worktree configures the worktree isolation of a run (specs/worktree/SPEC.md §6).
	Worktree WorktreeConfig `yaml:"worktree"`

	// Review holds what the review step's result changes in the run (specs/quality/SPEC.md §7.3).
	Review ReviewConfig `yaml:"review"`

	// Checks run in order before test.command, inside the test step and the pre-run test
	// (specs/quality/SPEC.md §5). Absent means none, as before.
	Checks []CheckConfig `yaml:"checks"`

	// Decision, Translation, Policy, and Context configure how the workflow is decided
	// (specs/JEV integration/TASKS.md T02, T22); see decision.go.
	Decision    DecisionConfig    `yaml:"decision"`
	Translation TranslationConfig `yaml:"translation"`
	Policy      PolicyConfig      `yaml:"policy"`
	Context     ContextConfig     `yaml:"context"`

	// Security holds the knobs of the execution-authority guards (specs/permissions/
	// SPEC.md §7, §9). None of them widens what a step may do.
	Security SecurityConfig `yaml:"security"`

	// Jira is where run --jira fetches the task from (specs/jira/SPEC.md §7); nil when the
	// section is absent, which is valid - only run --jira needs it.
	Jira *JiraConfig `yaml:"jira"`

	// Story configures story plan and story run (specs/story/SPEC.md §5); nil when the section
	// is absent, which is valid.
	Story *StoryConfig `yaml:"story"`

	// Extensions opts provider calls into the operator's MCP servers and Claude user
	// settings (extensions.go). Absent means none, as before.
	Extensions ExtensionsConfig `yaml:"extensions"`

	// Providers holds how the claude CLI is authenticated (providers.go). Absent means auth:
	// auto with api_key_env ANTHROPIC_API_KEY.
	Providers ProvidersConfig `yaml:"providers"`

	// AutoCommit commits a completed run's changes without asking, in every mode
	// (MVP.md §42.6): the operator's yes, given in advance. False keeps the question.
	AutoCommit bool `yaml:"auto_commit"`

	// AllowedModels and AllowedModelsFrom come from the org file (org.go) and never from the
	// repository's: "-" makes strict decoding treat allowed_models there as an unknown key.
	// Validate checks every model the run can call against the list; From is the org file.
	AllowedModels     []string `yaml:"-"`
	AllowedModelsFrom string   `yaml:"-"`
}

// ReviewConfig is the top-level review section.
type ReviewConfig struct {
	// FixMedium sends a review that passes but reports a medium issue back to implement once,
	// as a fix iteration. The run records it at creation (domain.RunOptions), so a resume follows
	// the rule the run started with. Default false.
	FixMedium bool `yaml:"fix_medium"`
}

type SecurityConfig struct {
	// RequireCleanWorktree is a pointer so an absent key (nil) can default to true while an
	// explicit false still means false; read it through CleanWorktreeRequired.
	RequireCleanWorktree *bool `yaml:"require_clean_worktree"`
	// ProtectedPaths extends domain.DefaultProtectedPaths: patterns an implement step must
	// not change.
	ProtectedPaths []string `yaml:"protected_paths"`
	// AllowUnverifiedCLI lets a run proceed on a provider CLI version outside the range its
	// permission flags were verified on; the run records and warns about it
	// (specs/hardening/SPEC.md §7.2). A CLI that cannot be executed is refused regardless.
	AllowUnverifiedCLI bool `yaml:"allow_unverified_cli"`
	// SecretScan is what happens when implement or the test command adds what looks like a
	// secret to the working tree (specs/hardening/SPEC.md §9).
	SecretScan SecretScanConfig `yaml:"secret_scan"`
}

// SecretScanConfig is security.secret_scan.
type SecretScanConfig struct {
	// Mode is stop (or empty, the default), warn or off.
	Mode string `yaml:"mode"`
	// AllowPaths are protected_paths-style patterns whose findings are ignored, e.g.
	// testdata/** for fixtures that hold fake keys on purpose.
	AllowPaths []string `yaml:"allow_paths"`
}

// security.secret_scan.mode values.
const (
	SecretScanStop = "stop"
	SecretScanWarn = "warn"
	SecretScanOff  = "off"
)

// Enabled reports whether the scan runs at all.
func (s SecretScanConfig) Enabled() bool { return s.Mode != SecretScanOff }

// Stops reports whether a new finding stops the run (the default) rather than warning.
func (s SecretScanConfig) Stops() bool { return s.Mode == "" || s.Mode == SecretScanStop }

// CleanWorktreeRequired reports whether a workflow with implement must start on a clean
// worktree - true unless security.require_clean_worktree is explicitly false.
func (s SecurityConfig) CleanWorktreeRequired() bool {
	return s.RequireCleanWorktree == nil || *s.RequireCleanWorktree
}

// CredentialEnvNames lists the environment variables the configuration names as the
// orchestrator's own credentials, sorted and without duplicates: decision.jev.api_key_env
// whatever the decision provider (the variable may be set either way), and jira.token_env
// and jira.email_env when the jira section is present, and the configured Claude and Codex
// API key variables (their defaults when absent). No child process may inherit them; the
// matching adapter puts its key back for its own process.
func (c Config) CredentialEnvNames() []string {
	names := []string{
		strings.TrimSpace(c.Decision.Jev.APIKeyEnv),
		c.Providers.Claude.KeyEnv(),
	}
	if c.Providers.Codex.Auth != "" || c.Providers.Codex.APIKeyEnv != "" || slices.Contains(ProvidersInUse(c), string(provider.Codex)) {
		names = append(names, c.Providers.Codex.KeyEnv())
	}
	if c.Jira != nil {
		names = append(names, c.Jira.TokenEnv, c.Jira.EmailEnv)
	}
	names = slices.DeleteFunc(names, func(n string) bool { return n == "" })
	slices.Sort(names)
	return slices.Compact(names)
}

// actionProviderModels returns every configured action alongside its pseudo-action
// name, in a fixed order, for validation/dependency-checking iteration.
func (c Config) actionProviderModels() []namedProviderModel {
	return []namedProviderModel{
		{Action: "research", ProviderModel: c.Actions.Research.ProviderModel},
		{Action: "investigate", ProviderModel: c.Actions.Investigate.ProviderModel},
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
		{Action: "investigate", Timeout: c.Actions.Investigate.Timeout},
		{Action: "implement", Timeout: c.Actions.Implement.Timeout},
		{Action: "review", Timeout: c.Actions.Review.Timeout},
	}
}

type namedTimeout struct {
	Action  string
	Timeout time.Duration
}
