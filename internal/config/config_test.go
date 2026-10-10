package config

import (
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func TestActionConfig_Route(t *testing.T) {
	a := ActionConfig{
		ProviderModel: ProviderModel{Provider: "claude", Model: "sonnet", Effort: "medium"},
		ByComplexity: map[domain.Complexity]ModelEffort{
			domain.ComplexitySimple:  {Model: "haiku", Effort: "low"},
			domain.ComplexityComplex: {Model: "opus", Effort: "high"},
			domain.ComplexityMedium:  {Effort: "high"},
		},
	}
	base := a.ProviderModel
	for _, tc := range []struct {
		name       string
		action     ActionConfig
		complexity domain.Complexity
		manual     bool
		want       ProviderModel
		source     string
	}{
		{"simple", a, domain.ComplexitySimple, false, ProviderModel{Provider: "claude", Model: "haiku", Effort: "low"}, "by_complexity.simple"},
		{"complex", a, domain.ComplexityComplex, false, ProviderModel{Provider: "claude", Model: "opus", Effort: "high"}, "by_complexity.complex"},
		{"effort only", a, domain.ComplexityMedium, false, ProviderModel{Provider: "claude", Model: "sonnet", Effort: "high"}, "by_complexity.medium"},
		{"manual", a, domain.ComplexitySimple, true, base, "base"},
		{"no complexity", a, "", false, base, "base"},
		{"missing level", ActionConfig{ProviderModel: base, ByComplexity: map[domain.Complexity]ModelEffort{domain.ComplexitySimple: {Model: "haiku"}}},
			domain.ComplexityComplex, false, base, "base"},
		{"no table", ActionConfig{ProviderModel: base}, domain.ComplexitySimple, false, base, "base"},
		{"model only keeps the base effort", ActionConfig{ProviderModel: base, ByComplexity: map[domain.Complexity]ModelEffort{domain.ComplexitySimple: {Model: "haiku"}}},
			domain.ComplexitySimple, false, ProviderModel{Provider: "claude", Model: "haiku", Effort: "medium"}, "by_complexity.simple"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, source := tc.action.Route(tc.complexity, tc.manual)
			if got != tc.want || source != tc.source {
				t.Errorf("Route(%q, %v) = %+v, %q; want %+v, %q", tc.complexity, tc.manual, got, source, tc.want, tc.source)
			}
		})
	}
}

func byComplexityYAML(table string) string {
	return strings.Replace(validV2(""), "implement: {provider: claude, model: opus}",
		"implement: {provider: claude, model: opus, by_complexity: "+table+"}", 1)
}

func TestLoad_ByComplexity(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, byComplexityYAML("{simple: {model: haiku, effort: low}, complex: {effort: high}}")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pm, source := cfg.Actions.Implement.Route(domain.ComplexitySimple, false)
	if pm.Model != "haiku" || pm.Effort != "low" || pm.Provider != "claude" || source != "by_complexity.simple" {
		t.Errorf("simple route = %+v, %q", pm, source)
	}
	if pm, _ := cfg.Actions.Implement.Route(domain.ComplexityComplex, false); pm.Model != "opus" || pm.Effort != "high" {
		t.Errorf("complex route = %+v", pm)
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestLoad_ByComplexityRejects(t *testing.T) {
	// A provider inside an entry is an unknown key: the provider is fixed per action.
	_, err := Load(writeTempConfig(t, byComplexityYAML("{simple: {provider: codex}}")))
	if err == nil || !strings.Contains(err.Error(), "unknown configuration keys") || !strings.Contains(err.Error(), "provider") {
		t.Errorf("a provider in an entry must be an unknown key, got %v", err)
	}

	for name, tc := range map[string]struct {
		table string
		want  string
	}{
		"invalid complexity": {"{trivial: {model: haiku}}", `actions.implement.by_complexity.trivial: "trivial" is not a complexity`},
		"empty entry":        {"{simple: {}}", "actions.implement.by_complexity.simple needs a model or an effort"},
		"bad effort":         {"{simple: {effort: turbo}}", "by_complexity.simple.effort"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeTempConfig(t, byComplexityYAML(tc.table)))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestValidate_ByComplexityEffortNeedsClaude(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Review.ByComplexity = map[domain.Complexity]ModelEffort{domain.ComplexitySimple: {Effort: "low"}}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "actions.review.by_complexity.simple.effort is only supported with provider claude") {
		t.Errorf("effort on a codex action must be refused, got %v", err)
	}

	cfg.Actions.Review.ByComplexity = map[domain.Complexity]ModelEffort{domain.ComplexitySimple: {Model: "mini"}}
	if err := Validate(cfg); err != nil {
		t.Errorf("a model on a codex action is valid: %v", err)
	}
}

// by_complexity never names a provider, so the providers the dependency and authority checks
// cover are the same with and without it.
func TestByComplexity_ProviderSetUnchanged(t *testing.T) {
	plain := claudeOnlyConfig()
	routed := claudeOnlyConfig()
	routed.Actions.Implement.ByComplexity = map[domain.Complexity]ModelEffort{
		domain.ComplexitySimple: {Model: "haiku"}, domain.ComplexityComplex: {Model: "opus"},
	}
	if err := Validate(routed); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if a, b := strings.Join(ProvidersInUse(plain), " "), strings.Join(ProvidersInUse(routed), " "); a != b {
		t.Errorf("providers in use changed: %q → %q", a, b)
	}

	var asked [2][]string
	for i, cfg := range []Config{plain, routed} {
		checker := &fakeAuthorityChecker{supported: map[string]bool{"claude": true}}
		if err := ValidateAuthorities(cfg, checker); err != nil {
			t.Fatalf("ValidateAuthorities: %v", err)
		}
		asked[i] = checker.asked
	}
	if strings.Join(asked[0], " ") != strings.Join(asked[1], " ") {
		t.Errorf("authority checks changed: %v → %v", asked[0], asked[1])
	}

	t.Setenv("JEV_API_KEY", "test-key")
	missing := FakeDependencyChecker{Missing: map[string]bool{"claude": true}}
	errPlain := ValidateDependencies(plain, missing, DependencyOptions{})
	errRouted := ValidateDependencies(routed, missing, DependencyOptions{})
	if errPlain == nil || errRouted == nil || errPlain.Error() != errRouted.Error() {
		t.Errorf("dependency checks changed: %v → %v", errPlain, errRouted)
	}
}
