package config

import (
	"errors"
	"os"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
)

func TestValidateDependencies_AllInstalled(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	checker := FakeDependencyChecker{Missing: map[string]bool{}}
	if err := ValidateDependencies(validConfig(), checker, DependencyOptions{}); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateDependencies_ClaudeMissing(t *testing.T) {
	checker := FakeDependencyChecker{Missing: map[string]bool{"claude": true}}
	err := ValidateDependencies(validConfig(), checker, DependencyOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"claude"`) || !strings.Contains(msg, "was not found in PATH") {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestValidateDependencies_CodexMissing(t *testing.T) {
	checker := FakeDependencyChecker{Missing: map[string]bool{"codex": true}}
	err := ValidateDependencies(validConfig(), checker, DependencyOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `provider "codex" is configured for action "review"`) {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidateDependencies_GeminiMissing(t *testing.T) {
	checker := FakeDependencyChecker{Missing: map[string]bool{"gemini": true}}
	err := ValidateDependencies(validConfig(), checker, DependencyOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `"gemini"`) {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidateDependencies_ProviderUsedByMultipleActions(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Review.Provider = "claude"
	cfg.Actions.Review.Model = "sonnet"
	// claude now backs analyze, plan, implement, review, and translation.

	checker := FakeDependencyChecker{Missing: map[string]bool{"claude": true}}
	err := ValidateDependencies(cfg, checker, DependencyOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `provider "claude" is required by actions [`) {
		t.Errorf("expected aggregated multi-action message, got: %s", msg)
	}
	if !strings.Contains(msg, "review") || !strings.Contains(msg, "analyze") {
		t.Errorf("expected aggregated actions to include review and analyze, got: %s", msg)
	}
}

func TestValidateDependencies_DecisionValid(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	checker := FakeDependencyChecker{Missing: map[string]bool{}}
	if err := ValidateDependencies(validConfig(), checker, DependencyOptions{}); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateDependencies_DecisionAPIKeyEnvNotSet(t *testing.T) {
	// t.Setenv registers cleanup that restores whatever the variable held before this
	// test; os.Unsetenv then removes it for the duration of the test so LookupEnv
	// reports ok=false, which a value of "" would not.
	t.Setenv("JEV_API_KEY", "sentinel")
	os.Unsetenv("JEV_API_KEY")
	checker := FakeDependencyChecker{Missing: map[string]bool{}}

	err := ValidateDependencies(validConfig(), checker, DependencyOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "jev decision provider: JEV_API_KEY is not configured") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidateDependencies_DecisionAPIKeyEnvBlank(t *testing.T) {
	t.Setenv("JEV_API_KEY", "   ")
	checker := FakeDependencyChecker{Missing: map[string]bool{}}

	err := ValidateDependencies(validConfig(), checker, DependencyOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "JEV_API_KEY is not configured") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidateDependencies_DecisionTranslationExecutableMissing(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	cfg := validConfig()
	cfg.Translation.Provider = "gemini"
	checker := FakeDependencyChecker{Missing: map[string]bool{"gemini": true}}

	err := ValidateDependencies(cfg, checker, DependencyOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	// research and translation both use gemini, so they are reported together - the
	// translator is checked even though an English request would never call it.
	if !strings.Contains(err.Error(), `provider "gemini" is required by actions [research, translation]`) {
		t.Errorf("expected aggregated message, got: %s", err.Error())
	}
}

func TestValidateDependencies_DecisionDoesNotCheckAPIKeyWhenEnvNameIsBlank(t *testing.T) {
	// ValidateDecisionConfig (structural) is responsible for flagging a blank
	// api_key_env; ValidateDecisionConfigDependencies must not also report a
	// confusing "" is not configured message for it.
	cfg := validConfig()
	cfg.Decision.Jev.APIKeyEnv = ""
	checker := FakeDependencyChecker{Missing: map[string]bool{}}

	err := ValidateDependencies(cfg, checker, DependencyOptions{})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateDependencies_DecisionMockNeedsNoAPIKey(t *testing.T) {
	t.Setenv("JEV_API_KEY", "sentinel")
	os.Unsetenv("JEV_API_KEY")
	cfg := validConfig()
	cfg.Decision.Jev.Mock = true
	checker := FakeDependencyChecker{Missing: map[string]bool{}}

	if err := ValidateDependencies(cfg, checker, DependencyOptions{}); err != nil {
		t.Fatalf("expected mock mode to pass without an API key, got: %v", err)
	}
}

func TestValidateDependencies_DecisionLLMProvider(t *testing.T) {
	t.Setenv("JEV_API_KEY", "sentinel")
	os.Unsetenv("JEV_API_KEY")
	cfg := validConfig()
	cfg.Decision.Provider = "codex"
	cfg.Decision.Model = "gpt"

	if err := ValidateDependencies(cfg, FakeDependencyChecker{Missing: map[string]bool{}}, DependencyOptions{}); err != nil {
		t.Fatalf("an LLM provider needs no JEV_API_KEY: %v", err)
	}

	err := ValidateDependencies(cfg, FakeDependencyChecker{Missing: map[string]bool{"codex": true}}, DependencyOptions{})
	if err == nil || !strings.Contains(err.Error(), "decision") || !strings.Contains(err.Error(), "codex") {
		t.Errorf("a missing decision executable must fail before any AI call: %v", err)
	}
}

func TestValidateDependencies_Sandbox(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	existing := t.TempDir()

	cfg := validConfig()
	cfg.Test = TestConfig{Command: "go test ./...", Sandbox: "bwrap", ROBinds: []string{existing}}
	if err := ValidateDependencies(cfg, FakeDependencyChecker{Missing: map[string]bool{}}, DependencyOptions{}); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	cfg.Test.RWBinds = []string{existing + "/missing"}
	err := ValidateDependencies(cfg, FakeDependencyChecker{Missing: map[string]bool{"bwrap": true}}, DependencyOptions{})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{`executable "bwrap" was not found in PATH`, existing + "/missing does not exist"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in:\n%v", want, err)
		}
	}
}

// fakeEnv is an injected getenv over a fixed map.
func fakeEnv(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func jiraConfig(auth string) Config {
	cfg := validConfig()
	j := validJira()
	if auth == JiraAuthBearer {
		j.Auth, j.EmailEnv = JiraAuthBearer, ""
	}
	cfg.Jira = &j
	return cfg
}

func TestValidateDependencies_Jira(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	installed := FakeDependencyChecker{Missing: map[string]bool{}}
	both := map[string]string{"JIRA_EMAIL": "me@example.com", "JIRA_API_TOKEN": "secret-token"}

	tests := []struct {
		name string
		cfg  Config
		opts DependencyOptions
		want []string // substrings; nil means no error
	}{
		{"not asked, section absent", validConfig(), DependencyOptions{}, nil},
		{"not asked, variables unset", jiraConfig(JiraAuthBasic), DependencyOptions{getenv: fakeEnv(nil)}, nil},
		{"basic, both set", jiraConfig(JiraAuthBasic), DependencyOptions{Jira: true, getenv: fakeEnv(both)}, nil},
		{"bearer, token set", jiraConfig(JiraAuthBearer),
			DependencyOptions{Jira: true, getenv: fakeEnv(map[string]string{"JIRA_API_TOKEN": "pat"})}, nil},
		{"section missing", validConfig(), DependencyOptions{Jira: true, getenv: fakeEnv(both)},
			[]string{"--jira needs a jira section in the configuration (see orchestrator.example.yaml)"}},
		{"token unset", jiraConfig(JiraAuthBasic),
			DependencyOptions{Jira: true, getenv: fakeEnv(map[string]string{"JIRA_EMAIL": "me@example.com"})},
			[]string{"jira.token_env names JIRA_API_TOKEN, which is not set"}},
		{"token blank", jiraConfig(JiraAuthBearer),
			DependencyOptions{Jira: true, getenv: fakeEnv(map[string]string{"JIRA_API_TOKEN": "  "})},
			[]string{"jira.token_env names JIRA_API_TOKEN, which is not set"}},
		{"email unset", jiraConfig(JiraAuthBasic),
			DependencyOptions{Jira: true, getenv: fakeEnv(map[string]string{"JIRA_API_TOKEN": "secret-token"})},
			[]string{"jira.email_env names JIRA_EMAIL, which is not set"}},
		{"both unset", jiraConfig(JiraAuthBasic), DependencyOptions{Jira: true, getenv: fakeEnv(nil)},
			[]string{"jira.email_env names JIRA_EMAIL, which is not set", "jira.token_env names JIRA_API_TOKEN, which is not set"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDependencies(tt.cfg, installed, tt.opts)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			var depErr *apperr.DependencyError
			if !errors.As(err, &depErr) {
				t.Fatalf("expected *apperr.DependencyError, got %T: %v", err, err)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error lacks %q:\n%v", w, err)
				}
			}
			for _, secret := range []string{"me@example.com", "secret-token"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error contains the value %q", secret)
				}
			}
		})
	}
}

// Jira problems are aggregated with the executable checks into one DependencyError.
func TestValidateDependencies_JiraAggregatesWithExecutables(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	err := ValidateDependencies(jiraConfig(JiraAuthBearer),
		FakeDependencyChecker{Missing: map[string]bool{"codex": true}},
		DependencyOptions{Jira: true, getenv: fakeEnv(nil)})
	if err == nil {
		t.Fatal("expected error")
	}
	for _, w := range []string{`executable "codex" was not found`, "jira.token_env names JIRA_API_TOKEN"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error lacks %q:\n%v", w, err)
		}
	}
}

// The sandbox is the default (specs/hardening/SPEC.md §6.3): a config without test.sandbox
// needs bwrap, and the refusal names both remedies; an explicit none needs nothing.
func TestValidateDependencies_SandboxByDefault(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	cfg := validConfig()
	cfg.Test = TestConfig{Command: "go test ./..."}
	err := ValidateDependencies(cfg, FakeDependencyChecker{Missing: map[string]bool{"bwrap": true}}, DependencyOptions{})
	if err == nil {
		t.Fatal("a config without test.sandbox must need bwrap")
	}
	for _, want := range []string{"test.sandbox: bwrap, the default", "apt install bubblewrap", "set test.sandbox: none to run tests unsandboxed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in:\n%v", want, err)
		}
	}

	cfg.Test.Sandbox = SandboxNone
	if err := ValidateDependencies(cfg, FakeDependencyChecker{Missing: map[string]bool{"bwrap": true}}, DependencyOptions{}); err != nil {
		t.Errorf("test.sandbox: none needs no bwrap, got: %v", err)
	}
}
