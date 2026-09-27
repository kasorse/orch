// Package testexec runs the configured test command and reports its real exit code -
// exit code 0 is the only source of truth for pass/fail (MVP.md §18/§31.4), never LLM text.
package testexec

import (
	"context"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
)

// Runner executes test.command through a shell. This is the one documented exception to
// argv-only process invocation: test.command is trusted operator configuration, not user
// task text, and must support shell syntax like "&&" and pipes.
type Runner struct {
	Executor execution.ProcessExecutor
	// Sandbox, when set (test.sandbox: bwrap), runs the command under bubblewrap.
	Sandbox *Sandbox
}

func New(executor execution.ProcessExecutor) *Runner {
	return &Runner{Executor: executor}
}

func (r *Runner) Run(ctx context.Context, command string, workingDir string) (domain.TestResult, error) {
	// The test command runs code a model may have written, so it must not inherit a
	// parent agent session's IPC channel either (specs/permissions/SPEC.md §7.1).
	spec := execution.Spec{
		Executable: "sh", Args: []string{"-c", command}, WorkingDir: workingDir, Env: execution.ScrubbedEnv(),
	}
	if r.Sandbox != nil {
		spec.Executable, spec.Args, spec.Env = "bwrap", r.Sandbox.args(workingDir, command), r.Sandbox.env()
	}
	result, err := r.Executor.Run(ctx, spec)
	if err != nil {
		return domain.TestResult{}, &apperr.ExecutionError{Msg: "failed to run test command", Err: err}
	}

	return domain.TestResult{
		Command:   command,
		ExitCode:  result.ExitCode,
		Success:   result.ExitCode == 0,
		Stdout:    result.Stdout,
		Stderr:    result.Stderr,
		Duration:  result.Duration,
		Sandboxed: r.Sandbox != nil,
	}, nil
}
