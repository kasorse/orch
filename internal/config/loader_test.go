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
  complexity:
    complex: 0.40
    medium: 0.50
  stages:
    thresholds: {research: 0.70, analysis: 0.60, planning: 0.65, development: 0.60, testing: 0.60, review: 0.70}
    force:
      complex: [analysis, planning, review]

context:
  max_files: 20
  max_bytes: 24576

actions:
  research:
    provider: claude
    model: sonnet
  analyze:
    provider: claude
    model: sonnet
  plan:
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
  analyze:
    provider: claude
    model: sonnet
  plan:
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
	if cfg.Actions.Analyze.Timeout != 0 {
		t.Errorf("expected analyze timeout to stay unset, got %v", cfg.Actions.Analyze.Timeout)
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
  complexity: {complex: 0.40, medium: 0.50}
  stages:
    thresholds: {research: 0.70, analysis: 0.60, planning: 0.65, development: 0.60, testing: 0.60, review: 0.70}
context: {max_files: 20, max_bytes: 24576}
actions:
  research: {provider: claude, model: sonnet}
  analyze: {provider: claude, model: sonnet}
  plan: {provider: claude, model: sonnet}
  implement: {provider: claude, model: opus}
  review: {provider: claude, model: sonnet}
test: {command: "go test ./..."}
limits: {max_steps: 12, max_fix_iterations: 2}
execution: {timeout: 15m}
` + extra
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
