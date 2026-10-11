package integration

import (
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 34, checks (specs/quality/SPEC.md §5): the operator's vet check fails, the test
// command is not run, implement is told which check failed and that the tests did not run,
// fixes it, and the run goes green. run.json names the failed check and keeps each check's
// result, and the check's output is in its own log.

// vetCheckCommand fails while Add still subtracts, like a linter finding the bug would.
const vetCheckCommand = `if grep -q "a - b" fixture.go; then echo "fixture.go:4: suspicious operator" >&2; exit 1; fi`

func TestScenario34_FailedCheckThenFixed(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) {
		o.checks = []checkOption{{"vet", vetCheckCommand}}
	})
	// The first implement edits the file without fixing it; the second fixes it.
	fixOnCall(t, 2)
	appendOnEveryImplement(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
	run := loadQL(t, repoDir)
	if want := []string{"implement", "test", "implement", "test", "review"}; !equalStrings(run.actions(), want) {
		t.Fatalf("executions = %v, want %v", run.actions(), want)
	}
	if run.Status != "completed" || run.FixIterations != 1 {
		t.Errorf("status = %s, fix_iterations = %d", run.Status, run.FixIterations)
	}

	// The failed check stopped the step: its fields are the top-level ones, and the tests did
	// not run.
	failed := run.Executions[1].TestResult
	if failed == nil || failed.Success || failed.FailedCheck != "vet" || failed.ExitCode != 1 || failed.Command != vetCheckCommand {
		t.Fatalf("the first test result = %+v", failed)
	}
	if len(failed.Checks) != 1 || failed.Checks[0] != (qlCheck{Name: "vet", ExitCode: 1, Success: false}) {
		t.Errorf("the first test result's checks = %+v", failed.Checks)
	}

	// The second step ran the check, which now passes, and then the test command.
	green := run.Executions[3].TestResult
	if green == nil || !green.Success || green.FailedCheck != "" || green.Command != "go test ./..." {
		t.Fatalf("the second test result = %+v", green)
	}
	if len(green.Checks) != 1 || green.Checks[0] != (qlCheck{Name: "vet", ExitCode: 0, Success: true}) {
		t.Errorf("the second test result's checks = %+v", green.Checks)
	}

	retry := r.prompt(t, "implement", 2)
	assertContains(t, "the retried implement prompt", retry,
		`The previous implementation FAILED the check "vet"`,
		"The tests did not run",
		"suspicious operator")
	assertNotContains(t, "the retried implement prompt", retry, "FAILED the test command")

	// The step is run 001 (implement is 000): the check's stderr is in its own log.
	assertContains(t, "the check's log", runDirFile(t, repoDir, "logs/001-test.vet.stderr.log"), "suspicious operator")
}
