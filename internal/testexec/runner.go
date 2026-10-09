// Package testexec runs the configured test command and reports its real exit code -
// exit code 0 is the only source of truth for pass/fail (MVP.md §18/§31.4), never LLM text.
package testexec

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
)

// Runner executes test.command through a shell. This is the one documented exception to
// argv-only process invocation: test.command is trusted operator configuration, not user
// task text, and must support shell syntax like "&&" and pipes. The checks (specs/quality/
// SPEC.md §5) are run the same way.
type Runner struct {
	Executor execution.ProcessExecutor
	// Sandbox, when set (test.sandbox: bwrap), runs the command under bubblewrap.
	Sandbox *Sandbox
}

func New(executor execution.ProcessExecutor) *Runner {
	return &Runner{Executor: executor}
}

// RunOptions are the per-run additions to what a command gets.
type RunOptions struct {
	// Env is extra KEY=VALUE pairs for the command, on top of the test allowlist. Go sets
	// them (ORCH_BASE_REF); the names are restricted to ORCH_* so nothing else widens the
	// environment, with or without the sandbox.
	Env []string
}

// extraEnvRE is what an extra environment pair must look like.
var extraEnvRE = regexp.MustCompile(`^ORCH_[A-Z0-9_]+=`)

func (o RunOptions) validate() error {
	for _, kv := range o.Env {
		if !extraEnvRE.MatchString(kv) {
			name, _, _ := strings.Cut(kv, "=")
			return &apperr.ExecutionError{Msg: fmt.Sprintf("refusing to pass %q to the test command: only ORCH_* variables are added to its environment", name)}
		}
	}
	return nil
}

func (r *Runner) Run(ctx context.Context, command string, workingDir string, opts RunOptions) (domain.TestResult, error) {
	if err := opts.validate(); err != nil {
		return domain.TestResult{}, err
	}
	// The test command runs code a model may have written, so it gets only the allowlisted
	// environment: no credential, no parent agent session's IPC channel
	// (specs/hardening/SPEC.md §6.1, specs/permissions/SPEC.md §7.1).
	spec := execution.Spec{
		Executable: "sh", Args: []string{"-c", command}, WorkingDir: workingDir,
		Env: append(execution.TestEnv(), opts.Env...),
	}
	if r.Sandbox != nil {
		spec.Executable, spec.Args, spec.Env = "bwrap", r.Sandbox.args(workingDir, command, opts.Env), r.Sandbox.env()
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
