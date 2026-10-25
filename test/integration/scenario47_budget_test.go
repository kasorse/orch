package integration

import (
	"errors"
	"os"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 47 (specs/governance/SPEC.md §12, GV01): every provider call of a fake claude reports
// total_cost_usd 3 and limits.max_cost_usd is 5. The run stops before the step that follows the
// call that crossed the budget; a resume under max_cost_usd 10 continues it.

var budgetStages = []string{"run", "--stage", "investigation", "--stage", "development", "--stage", "testing", "--stage", "review",
	"fix Add in fixture.go"}

// costPerCall makes the fake claude answer investigate, implement and review with a stream-json
// result that reports cost.
func costPerCall(t *testing.T, cost float64) {
	t.Helper()
	dir := os.Getenv("FAKE_RESPONSES_DIR")
	for name, body := range map[string]string{"investigate": readOnlyResponse, "implement": implementResponse, "review": reviewPassResponse} {
		writeResponse(t, dir, name, streamResult(t, body, cost))
	}
}

func TestScenario47_BudgetStopsTheRunAndAResumeContinuesIt(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newStageRun(t, repoDir, func(o *configOptions) { o.maxCostUSD = 5 })
	costPerCall(t, 3)

	_, _, err := runOrchestrator(t, repoDir, r.cfg, budgetStages...)
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("expected a LimitExceededError (exit 4), got %T: %v", err, err)
	}
	assertContains(t, "the error", err.Error(),
		"Run status: stopped", "Reason: budget exceeded: $6.00 of $5.00", "Raise limits.max_cost_usd to continue it.")

	// investigate cost 3 (under 5), implement cost 3 (6 in all): the test step never started.
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"investigate", "implement"}) {
		t.Errorf("invocations = %v, want [investigate implement]", got)
	}
	run := loadQL(t, repoDir)
	if run.Status != "stopped" || run.FailureReason != "budget exceeded: $6.00 of $5.00" ||
		!equalStrings(run.actions(), []string{"investigate", "implement"}) {
		t.Fatalf("status %s, reason %q, executions %v", run.Status, run.FailureReason, run.actions())
	}

	// The operator raises the budget; the run continues with the cost it already has.
	raised := jevConfig(t, r.jev, func(o *configOptions) { o.maxCostUSD = 10 })
	stdout, stderr, err := resume(t, repoDir, raised, run.ID)
	mustSucceed(t, stdout, stderr, err)

	resumed := loadQL(t, repoDir)
	if resumed.Status != "completed" || resumed.FailureReason != "" ||
		!equalStrings(resumed.actions(), []string{"investigate", "implement", "test", "review"}) {
		t.Errorf("after the resume: status %s, reason %q, executions %v", resumed.Status, resumed.FailureReason, resumed.actions())
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"investigate", "implement", "review"}) {
		t.Errorf("invocations = %v, want only the review added", got)
	}
}
