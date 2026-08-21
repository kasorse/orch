// Package execution provides a single injectable abstraction around running external
// processes so callers never shell out via os/exec directly (and so provider/test
// execution is unit-testable without spawning real processes).
package execution

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

type ProcessResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// ProcessExecutor runs an external command. Implementations must use argv-only
// invocation (never "sh -c" with interpolated user text) except where explicitly
// documented otherwise (testexec.Runner's trusted test.command is the one exception).
type ProcessExecutor interface {
	Run(ctx context.Context, executable string, args []string, stdin string, workingDir string) (ProcessResult, error)
}

// OSProcessExecutor runs real processes via exec.CommandContext.
type OSProcessExecutor struct{}

func NewOSProcessExecutor() *OSProcessExecutor { return &OSProcessExecutor{} }

func (e *OSProcessExecutor) Run(ctx context.Context, executable string, args []string, stdin string, workingDir string) (ProcessResult, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = workingDir
	if stdin != "" {
		cmd.Stdin = bytes.NewBufferString(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	result := ProcessResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	// Context cancellation/timeout takes precedence even if the killed process's exit
	// status also looks like a normal *exec.ExitError - the caller must be able to
	// distinguish "ran and exited nonzero" from "never got to finish."
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, ctxErr
	}

	if err == nil {
		result.ExitCode = 0
		return result, nil
	}

	var exitErr *exec.ExitError
	if ok := isExitError(err, &exitErr); ok {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}

	// Spawn failure or another non-exit error: the caller cannot treat this as "process
	// ran with a nonzero code," so it's surfaced as a real error rather than encoded
	// in ProcessResult.
	return result, err
}

func isExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}
