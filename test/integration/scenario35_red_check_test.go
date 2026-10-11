package integration

import (
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 35, checks in the pre-run test (specs/quality/SPEC.md §5.5). The pre-run test runs
// the checks and then test.command, and records each. Scenario 26 covers the refusal for a red
// check and the first implement's prompt; these cover what the quality phase adds: a green
// check does not hide a red suite, a red check does not stop a run that may start red and
// the run ends with the check green, and test.baseline: off skips the whole sequence.

// redLintCheck fails exactly while the fixture's test fails, so the fake implement's fix turns
// it green.
var redLintCheck = checkOption{"lint", `go test ./... >/dev/null 2>&1 || { echo lint-finding; exit 1; }`}

func TestScenario35_GreenCheckDoesNotHideARedSuite(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityPreRunRun(t, repoDir, func(o *configOptions) {
		o.checks = []checkOption{{"format", "true"}}
	})

	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "fix Add in fixture.go")...)
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("expected a WorkflowError (exit 4), got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "tests fail before any change") || strings.Contains(err.Error(), `check "format"`) {
		t.Errorf("the refusal should blame the tests, not the check:\n%v", err)
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("no provider call may happen before the refusal, got %v", got)
	}

	// The check ran first and passed; the failing fields are test.command's.
	pre := loadQL(t, repoDir).PreRunTest
	if pre == nil || pre.Success || pre.FailedCheck != "" || pre.Command != "go test ./..." {
		t.Fatalf("pre_run_test = %+v", pre)
	}
	if len(pre.Checks) != 1 || pre.Checks[0] != (qlCheck{Name: "format", ExitCode: 0, Success: true}) {
		t.Errorf("pre_run_test.checks = %+v", pre.Checks)
	}
}

func TestScenario35_RedCheckAllowedToStartRed(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityPreRunRun(t, repoDir, func(o *configOptions) {
		o.checks = []checkOption{redLintCheck}
	})

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "--allow-red-tests", "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	// The first implement is told which check is red, and that the tests did not run.
	assertContains(t, "the first implement prompt", r.prompt(t, "implement", 1),
		`The check "lint"`, "The tests did not run", "lint-finding")

	run := loadQL(t, repoDir)
	pre := run.PreRunTest
	if pre == nil || pre.Success || pre.FailedCheck != "lint" || pre.ExitCode != 1 {
		t.Fatalf("pre_run_test = %+v", pre)
	}
	if len(pre.Checks) != 1 || pre.Checks[0] != (qlCheck{Name: "lint", ExitCode: 1, Success: false}) {
		t.Errorf("pre_run_test.checks = %+v", pre.Checks)
	}

	// The run ends with the check green and the tests green.
	if run.Status != "completed" || !equalStrings(run.actions(), []string{"implement", "test"}) {
		t.Fatalf("status = %s, executions = %v", run.Status, run.actions())
	}
	final := run.Executions[1].TestResult
	if final == nil || !final.Success || final.FailedCheck != "" {
		t.Fatalf("the test result = %+v", final)
	}
	if len(final.Checks) != 1 || final.Checks[0] != (qlCheck{Name: "lint", ExitCode: 0, Success: true}) {
		t.Errorf("the test result's checks = %+v", final.Checks)
	}
	assertContains(t, "the pre-run check's log", runDirFile(t, repoDir, "logs/pre-run-test.lint.stdout.log"), "lint-finding")
}

func TestScenario35_BaselineOffSkipsTheChecks(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	// newQualityRun keeps the helpers' default, test.baseline: off.
	r := newQualityRun(t, repoDir, func(o *configOptions) {
		o.checks = []checkOption{redLintCheck}
	})

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	run := loadQL(t, repoDir)
	if run.PreRunTest != nil {
		t.Errorf("test.baseline: off ran the pre-run sequence: %+v", run.PreRunTest)
	}
	// The check still gates the test step: here it passed after the fix.
	if got := run.Executions[1].TestResult; got == nil || !got.Success || len(got.Checks) != 1 {
		t.Errorf("the test result = %+v", got)
	}
}
