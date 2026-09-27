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
