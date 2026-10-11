package action

import (
	"context"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/testexec"
	"orchestrator/internal/testreport"
	"orchestrator/internal/workflow"
)

// executeTest makes no AI call - it runs the operator's checks and then the real test
// command, and trusts only their exit codes (MVP.md §18/§31.4).
//
// The commands run code a model may have written, so the same guard as implement checks
// what they changed: a test that plants a hook, CI file or agent setting stops the run like
// an implement that did (specs/permissions/SPEC.md §7.3).
func (d *Dispatcher) executeTest(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	result, checkLogs, err := d.guardedTest(ctx, run, domain.ActionTest)
	if err != nil {
		if result == nil {
			return workflow.StepOutcome{}, err
		}
		return workflow.StepOutcome{TestResult: result, RawStdout: result.Stdout, RawStderr: result.Stderr, CheckLogs: checkLogs}, err
	}
	// The tests ran code a model wrote, which may have written files too.
	if err := d.scanSecrets(ctx, run, domain.ActionTest); err != nil {
		return workflow.StepOutcome{TestResult: result, RawStdout: result.Stdout, RawStderr: result.Stderr, CheckLogs: checkLogs}, err
	}

	d.applyTest(*result)
	path := d.saveArtifact(run, domain.ActionTest, *result)

	outcome := workflow.OutcomeSuccess
	if !result.Success {
		outcome = workflow.OutcomeFailure
	}

	return workflow.StepOutcome{
		Outcome:      outcome,
		ArtifactPath: path,
		TestResult:   result,
		RawStdout:    result.Stdout,
		RawStderr:    result.Stderr,
		CheckLogs:    checkLogs,
	}, nil
}

// PreRunTest runs the checks and the test command once before the first step of a workflow
// that implements and tests (specs/hardening/SPEC.md §8, specs/quality/SPEC.md §5.5), under
// the same runner, sandbox and guard as the test step. It is not a step: nothing is
// recorded in the run's executions and no artifact is saved. A red result is remembered for
// the first implement prompt, which only matters when the run goes on anyway (run
// --allow-red-tests); the caller decides that.
// The result and the checks' output are returned with a guard violation too, so they can be
// logged.
func (d *Dispatcher) PreRunTest(ctx context.Context, run *domain.Run) (*domain.TestResult, []domain.CheckOutput, error) {
	result, checkLogs, err := d.guardedTest(ctx, run, preRunTestStep)
	if err != nil {
		return result, checkLogs, err
	}
	d.applyPreRunTest(*result)
	return result, checkLogs, nil
}

// preRunTestStep names the pre-run test in a guard violation.
const preRunTestStep domain.ActionType = "pre-run test"

// guardedTest runs every check in order, then the test command, between one guard snapshot
// and a comparison after each command (specs/quality/SPEC.md §5.2): a violation stops the
// sequence before the next command runs on a tree with a planted hook, and so does the first
// non-zero exit. A nil result means a command never ran. The second value is the raw output
// of each check that ran, for the logs.
//
// A failed check makes the result's top-level fields the check's, names it in FailedCheck and
// leaves the test command unrun; with every check green they are the test command's.
func (d *Dispatcher) guardedTest(ctx context.Context, run *domain.Run, step domain.ActionType) (*domain.TestResult, []domain.CheckOutput, error) {
	if d.Guard == nil {
		return nil, nil, &apperr.WorkflowError{Msg: "running the test command requires a guard over the repository, and none is configured"}
	}
	before, err := d.Guard.Snapshot(ctx)
	if err != nil {
		return nil, nil, &apperr.WorkflowError{Msg: "failed to snapshot protected paths before the test command", Err: err}
	}

	// Set for every command, test.command included, so they all see the same environment;
	// empty when the restore point could not be recorded.
	opts := testexec.RunOptions{Env: []string{"ORCH_BASE_REF=" + run.PreRunRef}}

	var (
		checks  []domain.CheckResult
		outputs []domain.CheckOutput
		total   time.Duration
	)
	// finish makes res the result's top-level fields, with the checks that ran so far. A red
	// run also names the failing tests its output shows; the exit code stays the verdict.
	finish := func(res domain.TestResult, failedCheck string) *domain.TestResult {
		res.Checks = checks
		res.FailedCheck = failedCheck
		res.Duration = total
		if !res.Success {
			report := testreport.Parse(res.Stdout, res.Stderr)
			res.FailedTests, res.ReportFormat = report.Failed, report.Format
		}
		return &res
	}

	// The checks, then the test command, which ends the sequence.
	for i := 0; ; i++ {
		name, command := "", d.Config.Test.Command
		isCheck := i < len(d.Config.Checks)
		if isCheck {
			name, command = d.Config.Checks[i].Name, d.Config.Checks[i].Command
		}

		res, err := d.Runner.Run(ctx, command, d.Repo.Dir, opts)
		if err != nil {
			return nil, nil, err
		}
		total += res.Duration
		failedCheck := ""
		if isCheck {
			checks = append(checks, domain.CheckResult{Name: name, ExitCode: res.ExitCode, Success: res.Success, Duration: res.Duration})
			outputs = append(outputs, domain.CheckOutput{Name: name, Stdout: res.Stdout, Stderr: res.Stderr})
			if !res.Success {
				failedCheck = name
			}
		}

		what := "the test command"
		if isCheck {
			what = "the check " + name
		}
		changed, err := d.Guard.Changed(ctx, before)
		if err != nil {
			return finish(res, failedCheck), outputs, &apperr.WorkflowError{Msg: "failed to check protected paths after " + what, Err: err}
		}
		if len(changed) > 0 {
			return finish(res, failedCheck), outputs, &apperr.WorkflowError{Msg: protectedViolation(step, changed, run.PreRunRef)}
		}

		if !isCheck || !res.Success {
			return finish(res, failedCheck), outputs, nil
		}
	}
}
