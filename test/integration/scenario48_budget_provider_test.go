package integration

import (
	"errors"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 48 (specs/governance/SPEC.md §12, GV01): a budget with actions.review on codex, which
// reports no cost, is a configuration error before any provider call.

func TestScenario48_BudgetWithAProviderThatReportsNoCost(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newStageRun(t, repoDir, func(o *configOptions) {
		o.maxCostUSD = 5
		o.providers.Review = "codex"
	})

	_, _, err := runOrchestrator(t, repoDir, r.cfg, budgetStages...)
	var depErr *apperr.DependencyError
	if !errors.As(err, &depErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Fatalf("expected a DependencyError (exit 2), got %T: %v", err, err)
	}
	assertContains(t, "the error", err.Error(), "limits.max_cost_usd cannot be enforced: codex (actions.review) reports no cost")
	assertNotContains(t, "the error", err.Error(), "(actions.investigate)", "(actions.implement)")

	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("invocations = %v, want none", got)
	}
	if n := len(r.jev.Bodies()); n != 0 {
		t.Errorf("Jev got %d requests, want 0", n)
	}
}
