package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "orchestrator.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return path
}

func TestLoad_ValidConfig(t *testing.T) {
	path := writeTempConfig(t, `
version: 2

decision:
  provider: jev
  model: jev-1.13.0
  timeout: 5s
  complexity:
    primary: choice
    shadow: true
  on_failure: ask
  jev:
    base_url: https://api.typesafe.ai
    api_key_env: JEV_API_KEY
    max_retries: 1

translation:
  provider: claude
  model: haiku
  timeout: 60s

policy:
  context_depth:
    project_threshold: 0.5
  clarify:
    threshold: 0.80
  complexity:
    cuts: {medium: 0.50, complex: 1.40}
  stages:
    thresholds: {research: 0.70, development: 0.60}
    force:
      complex: [investigation, review]

context:
  max_files: 20
  max_bytes: 24576

actions:
  research:
    provider: claude
    model: sonnet
  investigate:
    provider: claude
    model: sonnet
  implement:
    provider: claude
    model: opus
  review:
    provider: codex
    model: gpt

test:
  command: "go test ./..."

limits:
  max_steps: 12
  max_fix_iterations: 2

execution:
  timeout: 15m
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Version != 2 {
		t.Errorf("expected version 2, got %d", cfg.Version)
	}
	if cfg.Actions.Implement.Model != "opus" {
		t.Errorf("expected implement model opus, got %q", cfg.Actions.Implement.Model)
	}
	if cfg.Execution.Timeout != 15*time.Minute {
		t.Errorf("expected timeout 15m, got %v", cfg.Execution.Timeout)
	}
	if cfg.Decision.Timeout != 5*time.Second || !cfg.Decision.UsesJev() {
		t.Errorf("decision = %+v", cfg.Decision)
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("the loaded example must be valid: %v", err)
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	path := writeTempConfig(t, "version: 1\nactions: [this is not, valid: yaml structure\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/orchestrator.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "failed to read config file") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_InvalidTimeout(t *testing.T) {
	path := writeTempConfig(t, "version: 1\nexecution:\n  timeout: \"not-a-duration\"\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid timeout")
	}
}

func TestLoad_PerActionTimeout(t *testing.T) {
	path := writeTempConfig(t, `
version: 1

actions:
  investigate:
    provider: claude
    model: sonnet
  implement:
    provider: claude
    model: opus
  review:
    provider: codex
    model: gpt
    timeout: 20m
  answer:
    provider: gemini
    model: pro

execution:
  timeout: 15m
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Actions.Review.Timeout != 20*time.Minute {
		t.Errorf("expected review timeout 20m, got %v", cfg.Actions.Review.Timeout)
	}
	if cfg.Actions.Investigate.Timeout != 0 {
		t.Errorf("expected investigate timeout to stay unset, got %v", cfg.Actions.Investigate.Timeout)
	}
}

func TestLoad_InvalidActionTimeout(t *testing.T) {
	path := writeTempConfig(t, "version: 1\nactions:\n  review:\n    provider: codex\n    model: gpt\n    timeout: \"not-a-duration\"\n")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid actions.review.timeout")
	}
}

func TestLoad_ExampleConfigIsValid(t *testing.T) {
	cfg, err := Load("../../orchestrator.example.yaml")
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("orchestrator.example.yaml must pass Validate: %v", err)
	}
}

func TestLoad_RequireCleanWorktree(t *testing.T) {
	cases := map[string]bool{
		"": true, // no security block
		"security:\n  protected_paths: [go.mod]\n":     true, // block without the key
		"security:\n  require_clean_worktree: true\n":  true,
		"security:\n  require_clean_worktree: false\n": false,
	}
	for extra, want := range cases {
		path := filepath.Join(t.TempDir(), "orchestrator.yaml")
		if err := os.WriteFile(path, []byte("version: 2\n"+extra), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("%q: %v", extra, err)
		}
		if got := cfg.Security.CleanWorktreeRequired(); got != want {
			t.Errorf("%q: CleanWorktreeRequired() = %v, want %v", extra, got, want)
		}
	}
}

// validV2 is a minimal complete version-2 config; extra is appended at the end.
func validV2(extra string) string {
	return `version: 2
decision:
  provider: jev
  model: jev-1.13.0
  timeout: 5s
  complexity: {primary: choice, shadow: true}
  on_failure: ask
  jev: {base_url: "https://api.typesafe.ai", api_key_env: JEV_API_KEY, max_retries: 1}
translation: {provider: claude, model: haiku, timeout: 60s}
policy:
  context_depth: {project_threshold: 0.5}
  clarify: {threshold: 0.80}
  complexity: {cuts: {medium: 0.50, complex: 1.40}}
  stages:
    thresholds: {research: 0.70, development: 0.60}
context: {max_files: 20, max_bytes: 24576}
actions:
  research: {provider: claude, model: sonnet}
  investigate: {provider: claude, model: sonnet}
  implement: {provider: claude, model: opus}
  review: {provider: claude, model: sonnet}
test: {command: "go test ./..."}
limits: {max_steps: 12, max_fix_iterations: 2}
execution: {timeout: 15m}
` + extra
}

// execution.rate_limit_wait: absent waits with the default, 0 turns waiting off.
func TestLoad_RateLimitWait(t *testing.T) {
	for yaml, want := range map[string]time.Duration{
		"execution: {timeout: 15m}":                       DefaultRateLimitWait,
		"execution: {timeout: 15m, rate_limit_wait: 0s}":  0,
		"execution: {timeout: 15m, rate_limit_wait: 10m}": 10 * time.Minute,
	} {
		cfg, err := Load(writeTempConfig(t, strings.Replace(validV2(""), "execution: {timeout: 15m}", yaml, 1)))
		if err != nil {
			t.Fatalf("%s: %v", yaml, err)
		}
		if cfg.Execution.RateLimitWait != want {
			t.Errorf("%s: RateLimitWait = %s, want %s", yaml, cfg.Execution.RateLimitWait, want)
		}
	}
}

// The keys specs/clarify removed are still declared, so strict loading does not call them
// unknown: Validate refuses each with its replacement and the spec, never reading it as
// something else.
func TestValidate_RefusesTheKeysAnalysisLeft(t *testing.T) {
	thresholds := "thresholds: {research: 0.70, development: 0.60}"
	for name, tc := range map[string]struct {
		yaml string
		want []string
	}{
		"threshold": {strings.Replace(validV2(""), thresholds, "thresholds: {research: 0.70, analysis: 0.60, development: 0.60}", 1),
			[]string{"policy.stages.thresholds.analysis", "policy.clarify.threshold", "specs/clarify"}},
		"force": {strings.Replace(validV2(""), thresholds, thresholds+"\n    force: {complex: [analysis, review]}", 1),
			[]string{"policy.stages.force.complex", "analysis is not a stage any more", "specs/clarify"}},
		"action": {strings.Replace(validV2(""), "  investigate: {", "  analyze: {provider: claude, model: sonnet}\n  investigate: {", 1),
			[]string{"actions.analyze was removed", "specs/clarify"}},
		"planning threshold": {strings.Replace(validV2(""), thresholds, "thresholds: {research: 0.70, planning: 0.60, development: 0.60}", 1),
			[]string{"policy.stages.thresholds.planning", "investigation", "specs/clarify"}},
		"planning in the template": {strings.Replace(validV2(""), thresholds, thresholds+"\n    force: {complex: [planning, review]}", 1),
			[]string{"policy.stages.force.complex", "planning was renamed investigation", "specs/clarify"}},
		"actions.plan": {strings.Replace(validV2(""), "  investigate: {", "  plan: {provider: claude, model: sonnet}\n  investigate: {", 1),
			[]string{"actions.plan was renamed actions.investigate", "specs/clarify"}},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeTempConfig(t, tc.yaml))
			if err != nil {
				t.Fatalf("Load: the key must be declared, got %v", err)
			}
			err = Validate(cfg)
			if err == nil {
				t.Fatal("Validate accepted a key analysis left")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q:\n%v", want, err)
				}
			}
		})
	}
}

// policy.clarify.threshold is required and in (0, 1]: loaded from a file, a missing key and a 0
// are config errors, and a key inside policy.clarify nobody declared is an unknown one.
func TestLoad_ClarifyThreshold(t *testing.T) {
	const withThreshold = "  clarify: {threshold: 0.80}\n"
	cfg, err := Load(writeTempConfig(t, validV2("")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Policy.Clarify.Threshold == nil || *cfg.Policy.Clarify.Threshold != 0.80 {
		t.Fatalf("threshold = %v, want 0.80", cfg.Policy.Clarify.Threshold)
	}
	if got := cfg.DecisionPolicy().ClarityThreshold; got != 0.80 {
		t.Errorf("DecisionPolicy().ClarityThreshold = %v, want 0.80", got)
	}

	for name, tc := range map[string]struct{ replace, want string }{
		"missing": {"", "policy.clarify.threshold is required"},
		"zero":    {"  clarify: {threshold: 0}\n", "policy.clarify.threshold must be in (0, 1]"},
		"above 1": {"  clarify: {threshold: 1.2}\n", "policy.clarify.threshold must be in (0, 1]"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeTempConfig(t, strings.Replace(validV2(""), withThreshold, tc.replace, 1)))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			err = Validate(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate = %v, want an error containing %q", err, tc.want)
			}
		})
	}

	_, err = Load(writeTempConfig(t, strings.Replace(validV2(""), withThreshold, "  clarify: {threshold: 0.8, treshold: 0.7}\n", 1)))
	if err == nil || !strings.Contains(err.Error(), "treshold") {
		t.Errorf("an undeclared key in policy.clarify must fail to load, got %v", err)
	}
}

func TestLoad_StrictRejectsUnknownKeys(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml string
		want []string
	}{
		"top level": {validV2("exectuion: {timeout: 1m}\n"), []string{"line 25: exectuion"}},
		// actions, translation and jira decode through their own UnmarshalYAML: strictness
		// must reach inside those too.
		"inside an action": {strings.Replace(validV2(""), "review: {provider: claude, model: sonnet}",
			"review: {provider: claude, model: sonnet, timout: 20m}", 1), []string{"line 21: timout"}},
		"translation effort": {strings.Replace(validV2(""), "model: haiku,", "model: haiku, effort: high,", 1),
			[]string{"line 9: effort"}},
		"inside jira": {validV2("jira:\n  base_url: https://x.atlassian.net\n  auth: basic\n  email_env: E\n  token_env: T\n  timeuot: 5s\n"),
			[]string{"line 30: timeuot"}},
		"several": {validV2("limit: {max_steps: 1}\nsecurty: {}\n"), []string{"line 25: limit", "line 26: securty"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeTempConfig(t, tc.yaml))
			if err == nil {
				t.Fatal("expected an unknown-key error")
			}
			msg := err.Error()
			for _, want := range append(tc.want, "unknown configuration keys", "orchestrator.example.yaml") {
				if !strings.Contains(msg, want) {
					t.Errorf("error lacks %q:\n%s", want, msg)
				}
			}
			if strings.Contains(msg, "not found in type") || strings.Contains(msg, "struct {") {
				t.Errorf("the error must not leak Go type names:\n%s", msg)
			}
		})
	}
}

func TestLoad_Verify(t *testing.T) {
	onImplement := strings.Replace(validV2(""), "implement: {provider: claude, model: opus}",
		"implement: {provider: claude, model: opus, verify: true}", 1)
	cfg, err := Load(writeTempConfig(t, onImplement))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Actions.Implement.Verify {
		t.Error("actions.implement.verify was not read")
	}
	if cfg.Limits.Verify.MaxCalls() != 8 || cfg.Limits.Verify.Timeout() != 10*time.Minute {
		t.Errorf("limits.verify defaults = %d, %s", cfg.Limits.Verify.MaxCalls(), cfg.Limits.Verify.Timeout())
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate: %v", err)
	}

	limits := strings.Replace(onImplement, "limits: {max_steps: 12, max_fix_iterations: 2}",
		"limits: {max_steps: 12, max_fix_iterations: 2, verify: {max_calls_per_step: 3, call_timeout: 2m}}", 1)
	cfg, err = Load(writeTempConfig(t, limits))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Limits.Verify.MaxCalls() != 3 || cfg.Limits.Verify.Timeout() != 2*time.Minute {
		t.Errorf("limits.verify = %d, %s; want 3, 2m", cfg.Limits.Verify.MaxCalls(), cfg.Limits.Verify.Timeout())
	}

	badTimeout := strings.Replace(limits, "call_timeout: 2m", "call_timeout: soon", 1)
	if _, err := Load(writeTempConfig(t, badTimeout)); err == nil || !strings.Contains(err.Error(), "limits.verify.call_timeout") {
		t.Errorf("a bad call_timeout must name the key, got %v", err)
	}

	onReview := strings.Replace(validV2(""), "review: {provider: claude, model: sonnet}",
		"review: {provider: claude, model: sonnet, verify: true}", 1)
	cfg, err = Load(writeTempConfig(t, onReview))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "actions.review.verify: unknown key") {
		t.Errorf("verify on review must be an unknown key, got %v", err)
	}
}

func TestLoad_ReviewFixMedium(t *testing.T) {
	for name, tc := range map[string]struct {
		extra string
		want  bool
	}{
		"absent defaults to false": {"", false},
		"explicit true":            {"\nreview:\n  fix_medium: true\n", true},
		"explicit false":           {"\nreview:\n  fix_medium: false\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeTempConfig(t, validV2("")+tc.extra))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Review.FixMedium != tc.want {
				t.Errorf("review.fix_medium = %v, want %v", cfg.Review.FixMedium, tc.want)
			}
			if err := Validate(cfg); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}

	for name, extra := range map[string]string{
		"not a boolean": "\nreview:\n  fix_medium: soon\n",
		"unknown key":   "\nreview:\n  fix_low: true\n",
	} {
		if _, err := Load(writeTempConfig(t, validV2("")+extra)); err == nil {
			t.Errorf("%s: Load must refuse it", name)
		}
	}
}

func TestLoad_StopWithoutProgress(t *testing.T) {
	const limits = "limits: {max_steps: 12, max_fix_iterations: 2}"
	for name, tc := range map[string]struct {
		limits string
		want   bool
	}{
		"absent defaults to true": {limits, true},
		"explicit true":           {"limits: {max_steps: 12, max_fix_iterations: 2, stop_without_progress: true}", true},
		"explicit false":          {"limits: {max_steps: 12, max_fix_iterations: 2, stop_without_progress: false}", false},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeTempConfig(t, strings.Replace(validV2(""), limits, tc.limits, 1)))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.Limits.StopsWithoutProgress(); got != tc.want {
				t.Errorf("StopsWithoutProgress = %v, want %v", got, tc.want)
			}
			if err := Validate(cfg); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}

	notABool := strings.Replace(validV2(""), limits, "limits: {max_steps: 12, max_fix_iterations: 2, stop_without_progress: soon}", 1)
	if _, err := Load(writeTempConfig(t, notABool)); err == nil {
		t.Error("a stop_without_progress that is not a boolean must be refused")
	}
}

func TestLoad_MaxCostUSD(t *testing.T) {
	const limits = "limits: {max_steps: 12, max_fix_iterations: 2}"
	cfg, err := Load(writeTempConfig(t, strings.Replace(validV2(""), limits,
		"limits: {max_steps: 12, max_fix_iterations: 2, max_cost_usd: 1.5}", 1)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Limits.MaxCostUSD == nil || *cfg.Limits.MaxCostUSD != 1.5 {
		t.Errorf("MaxCostUSD = %v, want 1.5", cfg.Limits.MaxCostUSD)
	}

	cfg, err = Load(writeTempConfig(t, validV2("")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Limits.MaxCostUSD != nil {
		t.Errorf("an absent max_cost_usd must stay nil, got %v", *cfg.Limits.MaxCostUSD)
	}
}

func TestLoad_StrictKeepsTheDedicatedMessages(t *testing.T) {
	// A literal secret is declared by the schema so Validate can explain the rejection.
	cfg, err := Load(writeTempConfig(t, validV2("jira:\n  base_url: https://x.atlassian.net\n  auth: bearer\n  token_env: T\n  token: abc\n")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("Validate must reject the literal jira token, got %v", err)
	}
	cfg, err = Load(writeTempConfig(t, strings.Replace(validV2(""), "api_key_env: JEV_API_KEY,", "api_key_env: JEV_API_KEY, api_key: abc,", 1)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Errorf("Validate must reject the literal jev api_key, got %v", err)
	}
}

func TestLoad_OldVersionIsLenient(t *testing.T) {
	// Version 1 has keys version 2 removed; Validate, not Load, explains the change.
	cfg, err := Load(writeTempConfig(t, "version: 1\nclassifier: {provider: claude, model: haiku}\nactions:\n  answer: {provider: claude, model: sonnet}\n"))
	if err != nil {
		t.Fatalf("Load must accept an old file for Validate to explain: %v", err)
	}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "unsupported config version 1") {
		t.Errorf("Validate = %v", err)
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, ""))
	if err != nil {
		t.Fatalf("an empty file is a validation problem, not a parse error: %v", err)
	}
	if err := Validate(cfg); err == nil {
		t.Error("an empty config must fail Validate")
	}
}

func TestLoad_PromptLimits(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, strings.Replace(validV2(""),
		"limits: {max_steps: 12, max_fix_iterations: 2}",
		"limits:\n  max_steps: 12\n  max_fix_iterations: 2\n  prompt:\n    max_test_output_bytes: 8192\n    diff_summarize: [\"*.pb.go\"]", 1)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Limits.Prompt
	if p.TestOutputBytes() != 8192 || p.DiffBytes() != DefaultMaxDiffBytes || len(p.DiffSummarize) != 1 {
		t.Errorf("limits.prompt = %+v (test output %d, diff %d)", p, p.TestOutputBytes(), p.DiffBytes())
	}

	_, err = Load(writeTempConfig(t, strings.Replace(validV2(""),
		"limits: {max_steps: 12, max_fix_iterations: 2}",
		"limits: {max_steps: 12, max_fix_iterations: 2, prompt: {max_diff: 1}}", 1)))
	if err == nil || !strings.Contains(err.Error(), "max_diff") {
		t.Errorf("an unknown key under limits.prompt must be rejected, got %v", err)
	}
}
