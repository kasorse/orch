package config

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
	"orchestrator/internal/provider/claude"
	"orchestrator/internal/provider/codex"
	"orchestrator/internal/provider/gemini"
)

// fakeAuthorityChecker supports every authority for the listed providers and nothing for
// the rest. It records each question, so tests can see which routes were checked.
type fakeAuthorityChecker struct {
	supported map[string]bool
	asked     []string
}

func (f *fakeAuthorityChecker) Supports(providerID string, a domain.Authority) bool {
	f.asked = append(f.asked, providerID+":"+string(a))
	return f.supported[providerID]
}

func claudeOnlyConfig() Config {
	cfg := validConfig()
	cfg.Actions.Research.Provider = "claude"
	cfg.Actions.Review.Provider = "claude"
	return cfg
}

func TestValidateAuthorities_AllSupported(t *testing.T) {
	checker := &fakeAuthorityChecker{supported: map[string]bool{"claude": true}}
	if err := ValidateAuthorities(claudeOnlyConfig(), checker); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// TestValidateAuthorities_AsksEveryRoute pins the route -> authority mapping the check
// uses: the fixed action table, and AuthorityNone for the translator and an LLM decision
// provider.
func TestValidateAuthorities_AsksEveryRoute(t *testing.T) {
	cfg := claudeOnlyConfig()
	cfg.Decision.Provider = "claude"
	cfg.Decision.Model = "haiku"
	checker := &fakeAuthorityChecker{supported: map[string]bool{"claude": true}}
	if err := ValidateAuthorities(cfg, checker); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	want := []string{
		"claude:web",  // research
		"claude:read", // plan
		"claude:edit", // implement
		"claude:read", // review
		"claude:none", // translation
		"claude:none", // decision
	}
	if strings.Join(checker.asked, " ") != strings.Join(want, " ") {
		t.Errorf("asked %v, want %v", checker.asked, want)
	}
}

func TestValidateAuthorities_JevDecisionNeedsNoProvider(t *testing.T) {
	checker := &fakeAuthorityChecker{supported: map[string]bool{"claude": true}}
	if err := ValidateAuthorities(claudeOnlyConfig(), checker); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	for _, q := range checker.asked {
		if strings.HasPrefix(q, "jev:") {
			t.Errorf("the Jev decision provider makes no CLI call, but was checked: %v", checker.asked)
		}
	}
}

func TestValidateAuthorities_UnsupportedRoute(t *testing.T) {
	checker := &fakeAuthorityChecker{supported: map[string]bool{"claude": true}}
	err := ValidateAuthorities(validConfig(), checker) // research -> gemini, review -> codex

	var authErr *apperr.AuthorityError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected *apperr.AuthorityError, got %T: %v", err, err)
	}
	msg := err.Error()
	for _, want := range []string{
		`provider "gemini" cannot enforce the "web" authority required by "research"`,
		`provider "codex" cannot enforce the "read" authority required by "review"`,
		"remedy: route review to a provider that supports it: claude (actions.review.provider: claude)",
		"remedy: route research to a provider that supports it: claude (actions.research.provider: claude)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected message to contain %q, got:\n%s", want, msg)
		}
	}
}

func TestValidateAuthorities_UnsupportedTranslationAndDecision(t *testing.T) {
	cfg := claudeOnlyConfig()
	cfg.Translation.Provider = "gemini"
	cfg.Decision.Provider = "codex"
	cfg.Decision.Model = "gpt"
	checker := &fakeAuthorityChecker{supported: map[string]bool{"claude": true}}
	msg := ValidateAuthorities(cfg, checker).Error()
	for _, want := range []string{
		`provider "gemini" cannot enforce the "none" authority required by "translation"`,
		"(translation.provider: claude)",
		`provider "codex" cannot enforce the "none" authority required by "decision"`,
		"(decision.provider: claude)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected message to contain %q, got:\n%s", want, msg)
		}
	}
}

func TestValidateAuthorities_NoProviderCanEnforce(t *testing.T) {
	checker := &fakeAuthorityChecker{supported: map[string]bool{}}
	msg := ValidateAuthorities(claudeOnlyConfig(), checker).Error()
	if !strings.Contains(msg, `no supported provider can enforce "edit" yet`) {
		t.Errorf("unexpected message:\n%s", msg)
	}
}

// TestValidateAuthorities_ExampleConfig runs the example config against the real adapters'
// declarations: it is the config most likely to be copied, so it must pass the check.
func TestValidateAuthorities_ExampleConfig(t *testing.T) {
	cfg, err := Load("../../orchestrator.example.yaml")
	if err != nil {
		t.Fatalf("load example: %v", err)
	}
	exec := &execution.FakeProcessExecutor{}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, claude.New(exec))
	registry.Register(provider.Codex, codex.New(exec))
	registry.Register(provider.Gemini, gemini.New(exec))
	if err := ValidateAuthorities(cfg, registry); err != nil {
		t.Errorf("orchestrator.example.yaml must pass ValidateAuthorities: %v", err)
	}
}

func TestProvidersInUse(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Plan.Provider, cfg.Actions.Implement.Provider = "gemini", "gemini"
	cfg.Translation.Provider = "claude"
	got := ProvidersInUse(cfg)
	if want := []string{"claude", "codex", "gemini"}; !slices.Equal(got, want) {
		t.Errorf("ProvidersInUse = %v, want %v", got, want)
	}
	// A provider routed only for translation still counts.
	only := validConfig()
	only.Actions = ActionsConfig{}
	only.Translation.Provider = "claude"
	if got := ProvidersInUse(only); !slices.Equal(got, []string{"claude"}) {
		t.Errorf("translation only: %v", got)
	}
}
