package config

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/apperr"
)

// worktree.branch (specs/delivery/SPEC.md §4.3): a template and the route of the call that reads
// the repository's instructions.

const branchYAML = `worktree:
  branch:
    template: "feature/{key}-{slug}"
    from_instructions:
      provider: claude
      model: haiku
      timeout: 60s
`

func branchConfig(from *BranchInstructionsConfig, template string) Config {
	cfg := validConfig()
	cfg.Worktree.Branch = BranchConfig{Template: template, FromInstructions: from}
	return cfg
}

func TestLoad_WorktreeBranch(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validV2(branchYAML)))
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Worktree.Branch
	if b.Template != "feature/{key}-{slug}" || b.FromInstructions == nil {
		t.Fatalf("branch = %+v", b)
	}
	fi := b.FromInstructions
	if fi.Provider != "claude" || fi.Model != "haiku" || fi.Timeout != 60*time.Second {
		t.Errorf("from_instructions = %+v", fi)
	}
	if problems := branchProblems(b); len(problems) > 0 {
		t.Errorf("branchProblems() = %v", problems)
	}

	// Absent means the instructions are not read.
	cfg, err = Load(writeTempConfig(t, validV2("worktree: {branch: {template: \"{id}\"}}\n")))
	if err != nil || cfg.Worktree.Branch.FromInstructions != nil || cfg.Worktree.Branch.Template != "{id}" {
		t.Errorf("template only: %+v, %v", cfg.Worktree.Branch, err)
	}
}

func TestLoad_WorktreeBranchIsStrict(t *testing.T) {
	for _, tc := range []struct{ name, yaml, want string }{
		{"under branch", "worktree: {branch: {templat: x}}\n", "templat"},
		{"under from_instructions", "worktree: {branch: {from_instructions: {provider: claude, model: haiku, timeuot: 5s}}}\n", "timeuot"},
		{"a bad timeout", "worktree: {branch: {from_instructions: {provider: claude, model: haiku, timeout: soon}}}\n", "soon"},
	} {
		if _, err := Load(writeTempConfig(t, validV2(tc.yaml))); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
}

func TestValidate_WorktreeBranch(t *testing.T) {
	route := func(mutate func(*BranchInstructionsConfig)) *BranchInstructionsConfig {
		fi := &BranchInstructionsConfig{ProviderModel: ProviderModel{Provider: "claude", Model: "haiku"}, Timeout: time.Minute}
		if mutate != nil {
			mutate(fi)
		}
		return fi
	}
	tests := []struct {
		name string
		cfg  Config
		want string // "" = valid
	}{
		{"nothing set", validConfig(), ""},
		{"a template", branchConfig(nil, "feature/{key}-{slug}"), ""},
		{"every placeholder", branchConfig(nil, "{date}/{id}/{key}/{slug}"), ""},
		{"a template without placeholders", branchConfig(nil, "work/queue"), ""},
		{"the instructions", branchConfig(route(nil), ""), ""},
		{"an unknown placeholder", branchConfig(nil, "feature/{type}-{slug}"), "worktree.branch.template"},
		{"{type} is not offered", branchConfig(nil, "{type}/{slug}"), "unknown placeholder {type}"},
		{"a stray brace", branchConfig(nil, "feature/{slug"), "brace"},
		{"no provider", branchConfig(route(func(fi *BranchInstructionsConfig) { fi.Provider = "" }), ""), "worktree.branch.from_instructions.provider is required"},
		{"an unsupported provider", branchConfig(route(func(fi *BranchInstructionsConfig) { fi.Provider = "gpt" }), ""), "worktree.branch.from_instructions.provider"},
		{"no model", branchConfig(route(func(fi *BranchInstructionsConfig) { fi.Model = "" }), ""), "worktree.branch.from_instructions.model is required"},
		{"no timeout", branchConfig(route(func(fi *BranchInstructionsConfig) { fi.Timeout = 0 }), ""), "worktree.branch.from_instructions.timeout must be a positive duration"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.cfg)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("Validate() = %v, want nil", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("Validate() = %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

// The route goes through the same fail-fast chain as the translator: its executable must exist, its
// provider must enforce authority none, its model must be allowed, and its provider counts for the
// CLI version and credential checks.
func TestWorktreeBranch_FailFastChain(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	fromGemini := &BranchInstructionsConfig{ProviderModel: ProviderModel{Provider: "gemini", Model: "flash"}, Timeout: time.Minute}
	cfg := branchConfig(fromGemini, "")
	cfg.Actions.Research.Provider = "claude" // gemini now backs the branch-name call alone
	cfg.Actions.Review.Provider = "claude"

	err := ValidateDependencies(cfg, FakeDependencyChecker{Missing: map[string]bool{"gemini": true}}, DependencyOptions{})
	var depErr *apperr.DependencyError
	if !errors.As(err, &depErr) || !strings.Contains(err.Error(), `provider "gemini" is configured for action "worktree.branch.from_instructions"`) {
		t.Errorf("a missing executable: got %v", err)
	}
	if err := ValidateDependencies(cfg, FakeDependencyChecker{}, DependencyOptions{}); err != nil {
		t.Errorf("all installed: %v", err)
	}

	checker := &fakeAuthorityChecker{supported: map[string]bool{"claude": true}}
	err = ValidateAuthorities(cfg, checker)
	var authErr *apperr.AuthorityError
	if !errors.As(err, &authErr) || authErr.Route != "worktree.branch.from_instructions" || authErr.Authority != "none" {
		t.Errorf("an unsupported provider: got %v", err)
	}
	if !strings.Contains(err.Error(), "worktree.branch.from_instructions.provider") {
		t.Errorf("the remedy must name the key to change: %v", err)
	}

	// The CLI version and credential checks walk ProvidersInUse.
	if !slices.Contains(ProvidersInUse(cfg), "gemini") {
		t.Errorf("ProvidersInUse() = %v, want gemini for the branch-name call", ProvidersInUse(cfg))
	}

	// The org file's allowed_models covers the call's model.
	cfg = branchConfig(&BranchInstructionsConfig{ProviderModel: ProviderModel{Provider: "claude", Model: "mystery"}, Timeout: time.Minute}, "")
	cfg.AllowedModels, cfg.AllowedModelsFrom = []string{"haiku", "sonnet", "opus", "gpt", "pro"}, "/etc/orchestrator/org.yaml"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "worktree.branch.from_instructions: model mystery is not in allowed_models") {
		t.Errorf("an unlisted model: got %v", err)
	}
	cfg.Worktree.Branch.FromInstructions.Model = "haiku"
	if err := Validate(cfg); err != nil {
		t.Errorf("a listed model: %v", err)
	}
}

type costChecker map[string]bool

func (c costChecker) ReportsCost(providerID string) bool { return c[providerID] }

// A budget needs every call to report its cost, the branch-name call too.
func TestWorktreeBranch_BudgetNeedsCostReporting(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	budget := 1.0
	cfg := branchConfig(&BranchInstructionsConfig{ProviderModel: ProviderModel{Provider: "gemini", Model: "flash"}, Timeout: time.Minute}, "")
	cfg.Actions.Research.Provider = "claude"
	cfg.Limits.MaxCostUSD = &budget

	err := ValidateDependencies(cfg, FakeDependencyChecker{}, DependencyOptions{Cost: costChecker{"claude": true, "codex": true}})
	if err == nil || !strings.Contains(err.Error(), "limits.max_cost_usd cannot be enforced: gemini (worktree.branch.from_instructions) reports no cost") {
		t.Errorf("got %v", err)
	}
	if err := ValidateDependencies(cfg, FakeDependencyChecker{}, DependencyOptions{Cost: costChecker{"claude": true, "codex": true, "gemini": true}}); err != nil {
		t.Errorf("every call reports its cost: %v", err)
	}
}
