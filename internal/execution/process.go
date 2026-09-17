// Package execution provides a single injectable abstraction around running external
// processes so callers never shell out via os/exec directly (and so provider/test
// execution is unit-testable without spawning real processes).
package execution

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"syscall"
	"time"
)

type ProcessResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// Spec describes one process invocation. StdoutSink/StderrSink are optional live tees
// (--verbose streaming) - when set, output is written to them as it arrives, in
// addition to being captured into ProcessResult as before.
type Spec struct {
	Executable string
	Args       []string
	Stdin      string
	WorkingDir string
	StdoutSink io.Writer
	StderrSink io.Writer
}

// ProcessExecutor runs an external command. Implementations must use argv-only
// invocation (never "sh -c" with interpolated user text) except where explicitly
// documented otherwise (testexec.Runner's trusted test.command is the one exception).
type ProcessExecutor interface {
	Run(ctx context.Context, spec Spec) (ProcessResult, error)
}

// OSProcessExecutor runs real processes via exec.CommandContext.
type OSProcessExecutor struct{}

func NewOSProcessExecutor() *OSProcessExecutor { return &OSProcessExecutor{} }

func (e *OSProcessExecutor) Run(ctx context.Context, spec Spec) (ProcessResult, error) {
	cmd := exec.CommandContext(ctx, spec.Executable, spec.Args...)
	cmd.Dir = spec.WorkingDir
	if spec.Stdin != "" {
		cmd.Stdin = bytes.NewBufferString(spec.Stdin)
	}

	// Run the child in its own process group and kill the whole group on
	// cancellation/timeout, not just the direct child - a provider CLI that is (or
	// wraps) a shell script may fork grandchildren that inherit the stdout/stderr
	// pipes, and killing only the direct child leaves those orphaned, holding the
	// pipes open and defeating the timeout (cmd.Wait would block until they exit on
	// their own). WaitDelay is a backstop: if closing the pipes still doesn't unblock
	// Wait shortly after the group is killed, give up on draining them rather than
	// hang past the caller's timeout indefinitely.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second

	var stdout, stderr bytes.Buffer
	if spec.StdoutSink != nil {
		cmd.Stdout = io.MultiWriter(&stdout, spec.StdoutSink)
	} else {
		cmd.Stdout = &stdout
	}
	if spec.StderrSink != nil {
		cmd.Stderr = io.MultiWriter(&stderr, spec.StderrSink)
	} else {
		cmd.Stderr = &stderr
	}

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
