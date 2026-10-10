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

	// verifySet records that the verify key was written, so Validate can name it on an
	// action that does not accept it.
	verifySet bool
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
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	a.Provider = raw.Provider
	a.Model = raw.Model
	a.Effort = raw.Effort
	a.Verify = raw.Verify != nil && *raw.Verify
	a.verifySet = raw.Verify != nil
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

	// Extensions opts provider calls into the operator's MCP servers and Claude user
	// settings (extensions.go). Absent means none, as before.
	Extensions ExtensionsConfig `yaml:"extensions"`

	// AutoCommit commits a completed run's changes without asking, in every mode
	// (MVP.md §42.6): the operator's yes, given in advance. False keeps the question.
	AutoCommit bool `yaml:"auto_commit"`
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
// and jira.email_env when the jira section is present. No child process may inherit them
// (execution.ScrubCredentials, specs/jira/SPEC.md §12).
func (c Config) CredentialEnvNames() []string {
	names := []string{strings.TrimSpace(c.Decision.Jev.APIKeyEnv)}
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
