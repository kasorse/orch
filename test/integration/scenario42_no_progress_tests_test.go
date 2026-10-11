package integration

import (
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 42, no progress: the tests (specs/quality/SPEC.md §8.1). Implement changes the tree
// each time, but the same test fails with the same exit code twice, so the run stops after the
// second failed test, not after the limit.

func TestScenario42_TheSameTestsFailTwice(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) { o.stopWithoutProgress = true })
	// Each implement adds a line, so the tree changes, but Add stays wrong.
	neverFix(t)
	appendOnEveryImplement(t, repoDir)

	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "fix Add in fixture.go")...)
	run := stoppedForNoProgress(t, err, repoDir, "no progress: the same tests fail")

	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "implement"}) {
		t.Errorf("invocations = %v", got)
	}
	if want := []string{"implement", "test", "implement", "test"}; !equalStrings(run.actions(), want) {
		t.Fatalf("executions = %v, want %v", run.actions(), want)
	}
	// The trees differ, so it is the tests that stopped the run, and the failing test is named.
	if a, b := run.Executions[0].TreeAfter, run.Executions[2].TreeAfter; a == "" || a == b {
		t.Errorf("tree_after = %q then %q, want two different trees", a, b)
	}
	for _, i := range []int{1, 3} {
		res := run.Executions[i].TestResult
		if res == nil || res.Success || res.ExitCode != 1 || len(res.FailedTests) != 1 || res.FailedTests[0] != "fixture.TestAdd" {
			t.Errorf("executions[%d].test_result = %+v", i, res)
		}
	}
	if run.FixIterations != 2 {
		t.Errorf("fix_iterations = %d, want 2", run.FixIterations)
	}
}
