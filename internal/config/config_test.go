package config

import (
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
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
	// An escalate names a model and an effort only: no provider, no escalate of its own.
	for _, table := range []string{"{complex: {escalate: {provider: codex}}}", "{complex: {escalate: {model: opus, escalate: {model: x}}}}"} {
		if _, err := Load(writeTempConfig(t, byComplexityYAML(table))); err == nil || !strings.Contains(err.Error(), "unknown configuration keys") {
			t.Errorf("%s must be an unknown key, got %v", table, err)
		}
	}

	for name, tc := range map[string]struct {
		table string
		want  string
	}{
		"invalid complexity":  {"{trivial: {model: haiku}}", `actions.implement.by_complexity.trivial: "trivial" is not a complexity`},
		"empty entry":         {"{simple: {}}", "actions.implement.by_complexity.simple needs a model or an effort"},
		"bad effort":          {"{simple: {effort: turbo}}", "by_complexity.simple.effort"},
		"empty escalate":      {"{complex: {model: sonnet, escalate: {}}}", "actions.implement.by_complexity.complex.escalate needs a model or an effort"},
		"bad escalate effort": {"{complex: {model: sonnet, escalate: {effort: turbo}}}", "by_complexity.complex.escalate.effort"},
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

func reviewModelsYAML(models string) string {
	return strings.Replace(validV2(""), "review: {provider: claude, model: sonnet}",
		"review: {provider: claude, model: sonnet, models: "+models+"}", 1)
}

func TestLoad_ReviewModels(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, reviewModelsYAML("[opus, haiku]")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := strings.Join(cfg.Actions.Review.Models, ","); got != "opus,haiku" {
		t.Errorf("models = %q", got)
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate: %v", err)
	}

	if cfg, err := Load(writeTempConfig(t, validV2(""))); err != nil || Validate(cfg) != nil || len(cfg.Actions.Review.Models) != 0 {
		t.Errorf("no models is the default and valid: %v", err)
	}
}

func TestValidate_ReviewModels(t *testing.T) {
	for name, tc := range map[string]struct {
		models string
		want   string
	}{
		"too many":         {"[a, b, c, d, e]", "at most 4"},
		"empty name":       {`["opus", " "]`, "actions.review.models[1] must not be empty"},
		"the base model":   {"[sonnet]", `actions.review.models[0] "sonnet" repeats the base model`},
		"a repeated entry": {"[opus, opus]", `actions.review.models[1] "opus" repeats`},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeTempConfig(t, reviewModelsYAML(tc.models)))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate = %v, want it to contain %q", err, tc.want)
			}
		})
	}

	// yaml.v3 would drop a null entry of a string list without a word; it is refused at load.
	for _, models := range []string{"[~]", "[opus, null]"} {
		if _, err := Load(writeTempConfig(t, reviewModelsYAML(models))); err == nil || !strings.Contains(err.Error(), "must not be null or empty") {
			t.Errorf("Load with models %s = %v, want the null entry refused", models, err)
		}
	}

	// A by_complexity model is a base reviewer too, once its complexity is decided.
	byc := strings.Replace(reviewModelsYAML("[opus, big]"), "model: sonnet, models",
		"model: sonnet, by_complexity: {complex: {model: big}}, models", 1)
	cfg, err := Load(writeTempConfig(t, byc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), `actions.review.models[1] "big" repeats the base model, a by_complexity model`) {
		t.Errorf("Validate = %v, want the by_complexity model refused as an extra reviewer", err)
	}

	// Four is the most; the key belongs to review alone.
	if cfg, err := Load(writeTempConfig(t, reviewModelsYAML("[a, b, c, d]"))); err != nil || Validate(cfg) != nil {
		t.Errorf("four extra reviewers are valid: %v", err)
	}
	for _, action := range []string{"research", "investigate", "implement"} {
		y := strings.Replace(validV2(""), action+": {provider: claude, model:", action+": {models: [opus], provider: claude, model:", 1)
		cfg, err := Load(writeTempConfig(t, y))
		if err != nil {
			t.Fatalf("%s: Load: %v", action, err)
		}
		if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "actions."+action+".models: unknown key") {
			t.Errorf("%s: Validate = %v, want models refused", action, err)
		}
	}
}

func TestValidate_ByComplexityEffortNeedsClaude(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Review.Provider = "gemini"
	cfg.Actions.Review.ByComplexity = map[domain.Complexity]ModelEffort{domain.ComplexitySimple: {Effort: "low"}}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "actions.review.by_complexity.simple.effort is only supported with provider claude or codex") {
		t.Errorf("effort on a gemini action must be refused, got %v", err)
	}

	cfg.Actions.Review.ByComplexity = map[domain.Complexity]ModelEffort{domain.ComplexitySimple: {Model: "mini"}}
	if err := Validate(cfg); err != nil {
		t.Errorf("a model on a gemini action is valid: %v", err)
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

func TestActionsConfig_EscalatedRoute(t *testing.T) {
	base := ProviderModel{Provider: "claude", Model: "sonnet"}
	table := map[domain.Complexity]ModelEffort{
		domain.ComplexityMedium:  {Effort: "high"},
		domain.ComplexityComplex: {Model: "opus"},
	}
	decided := func(c domain.Complexity) *domain.Decision {
		return &domain.Decision{Source: domain.DecisionSourceLLM, Complexity: domain.ComplexityOutcome{Selected: c}}
	}
	on := ActionsConfig{Implement: ActionConfig{ProviderModel: base, ByComplexity: table, Escalate: true}}
	off := ActionsConfig{Implement: ActionConfig{ProviderModel: base, ByComplexity: table}}
	ownTable := map[domain.Complexity]ModelEffort{
		domain.ComplexityMedium:  {Effort: "medium", Escalate: &Escalation{Model: "opus"}},
		domain.ComplexityComplex: {Effort: "high", Escalate: &Escalation{Model: "opus"}},
	}
	own := ActionsConfig{Implement: ActionConfig{ProviderModel: base, ByComplexity: ownTable, Escalate: true}}
	for _, tc := range []struct {
		name    string
		actions ActionsConfig
		action  domain.ActionType
		d       *domain.Decision
		want    ProviderModel
		source  string
		ok      bool
	}{
		{"simple climbs to medium", on, domain.ActionImplement, decided(domain.ComplexitySimple), ProviderModel{Provider: "claude", Model: "sonnet", Effort: "high"}, "escalated.medium", true},
		{"medium climbs to complex", on, domain.ActionImplement, decided(domain.ComplexityMedium), ProviderModel{Provider: "claude", Model: "opus"}, "escalated.complex", true},
		{"complex has nothing above", on, domain.ActionImplement, decided(domain.ComplexityComplex), ProviderModel{}, "", false},
		{"off", off, domain.ActionImplement, decided(domain.ComplexitySimple), ProviderModel{}, "", false},
		{"manual", on, domain.ActionImplement, &domain.Decision{Source: domain.DecisionSourceManual}, ProviderModel{}, "", false},
		{"no decision", on, domain.ActionImplement, nil, ProviderModel{}, "", false},
		{"another action", on, domain.ActionReview, decided(domain.ComplexitySimple), ProviderModel{}, "", false},
		{"level above without an entry", ActionsConfig{Implement: ActionConfig{ProviderModel: base, Escalate: true,
			ByComplexity: map[domain.Complexity]ModelEffort{domain.ComplexityComplex: {Model: "opus"}}}},
			domain.ActionImplement, decided(domain.ComplexitySimple), ProviderModel{}, "", false},
		{"complex with its own escalate", own, domain.ActionImplement, decided(domain.ComplexityComplex),
			ProviderModel{Provider: "claude", Model: "opus", Effort: "high"}, "by_complexity.complex.escalate", true},
		{"own escalate keeps the entry's effort", own, domain.ActionImplement, decided(domain.ComplexityMedium),
			ProviderModel{Provider: "claude", Model: "opus", Effort: "medium"}, "by_complexity.medium.escalate", true},
		{"a level without its own escalate climbs", own, domain.ActionImplement, decided(domain.ComplexitySimple),
			ProviderModel{Provider: "claude", Model: "sonnet", Effort: "medium"}, "escalated.medium", true},
		{"own escalate without escalate: true", ActionsConfig{Implement: ActionConfig{ProviderModel: base, ByComplexity: ownTable}},
			domain.ActionImplement, decided(domain.ComplexityComplex), ProviderModel{Provider: "claude", Model: "opus", Effort: "high"}, "by_complexity.complex.escalate", true},
		{"own escalate, manual", own, domain.ActionImplement, &domain.Decision{Source: domain.DecisionSourceManual}, ProviderModel{}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, source, ok := tc.actions.EscalatedRoute(tc.action, tc.d)
			if got != tc.want || source != tc.source || ok != tc.ok {
				t.Errorf("EscalatedRoute = %+v, %q, %v; want %+v, %q, %v", got, source, ok, tc.want, tc.source, tc.ok)
			}
		})
	}
}

func TestLoad_Escalate(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, strings.Replace(byComplexityYAML("{complex: {model: big}}"), "by_complexity:", "escalate: true, by_complexity:", 1)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Validate(cfg); err != nil || !cfg.Actions.Implement.Escalate {
		t.Fatalf("escalate with by_complexity: Validate = %v, Escalate = %v", err, cfg.Actions.Implement.Escalate)
	}
	// An entry's own escalate needs no escalate: true.
	cfg, err = Load(writeTempConfig(t, byComplexityYAML("{complex: {model: sonnet, effort: high, escalate: {model: big}}}")))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("an entry's escalate: Validate = %v", err)
	}
	if e := cfg.Actions.Implement.ByComplexity[domain.ComplexityComplex].Escalate; e == nil || e.Model != "big" || e.Effort != "" {
		t.Errorf("complex escalate = %+v, want {Model: big}", e)
	}
	for name, tc := range map[string]struct{ yaml, want string }{
		"without by_complexity": {strings.Replace(validV2(""), "implement: {provider: claude, model: opus}",
			"implement: {provider: claude, model: opus, escalate: true}", 1), "actions.implement.escalate needs actions.implement.by_complexity"},
		"on review": {strings.Replace(validV2(""), "review: {provider: claude, model: sonnet}",
			"review: {provider: claude, model: sonnet, escalate: false}", 1), "actions.review.escalate: unknown key"},
		"in a review entry": {strings.Replace(validV2(""), "review: {provider: claude, model: sonnet}",
			"review: {provider: claude, model: sonnet, by_complexity: {complex: {model: opus, escalate: {effort: high}}}}", 1),
			"actions.review.by_complexity.complex.escalate: unknown key"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeTempConfig(t, tc.yaml))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate = %v, want %q", err, tc.want)
			}
		})
	}
}
