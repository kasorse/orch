// Package claude adapts the "claude" CLI to the common provider.Client interface.
// CLI syntax observed via `claude --help` on the development machine: -p/--print for
// non-interactive output, --model <model>, and --permission-mode bypassPermissions to
// run fully unattended (no interactive approval prompts, since this runs headless).
package claude

import (
	"context"
	"errors"
	"fmt"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
)

const executable = "claude"

type Client struct {
	Executor execution.ProcessExecutor
}

func New(executor execution.ProcessExecutor) *Client {
	return &Client{Executor: executor}
}

func buildArgs(model, prompt string) []string {
	return []string{
		"--print",
		"--permission-mode", "bypassPermissions",
		"--model", model,
		prompt,
	}
}

// redactedArgs mirrors buildArgs' shape but replaces the prompt with a byte count -
// this is the one place that knows the prompt occupies the final argv position, so
// apperr.ProviderError never has to know or receive the real prompt text.
func redactedArgs(model string, promptLen int) []string {
	return []string{
		"--print",
		"--permission-mode", "bypassPermissions",
		"--model", model,
		fmt.Sprintf("<prompt: %d chars>", promptLen),
	}
}

// Supports reports every defined authority as enforceable.
//
// TODO(T02): buildArgs still passes bypassPermissions regardless of req.Authority, so
// today this is a declaration of intent, not enforcement. T02 translates each authority
// into the verified flag sets of specs/permissions/SPEC.md §6.2.
func (c *Client) Supports(a domain.Authority) bool {
	return a.Valid()
}

func (c *Client) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	args := buildArgs(req.Model, req.Prompt)

	spec := execution.Spec{
		Executable: executable,
		Args:       args,
		WorkingDir: req.WorkingDir,
	}
	if req.Stream != nil {
		spec.StdoutSink = req.Stream
		spec.StderrSink = req.Stream
	}

	res, err := c.Executor.Run(ctx, spec)

	result := provider.Result{
		Output:   res.Stdout,
		Stderr:   res.Stderr,
		ExitCode: res.ExitCode,
		Duration: res.Duration,
	}

	if err != nil {
		kind := apperr.FailureSpawn
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			kind = apperr.FailureTimeout
		case errors.Is(err, context.Canceled):
			kind = apperr.FailureCancelled
		}
		// Action is left blank here - the action dispatcher fills it in, since it is
		// the only layer that knows which action invoked this provider call.
		return result, &apperr.ProviderError{
			Provider:   string(provider.Claude),
			Model:      req.Model,
			Kind:       kind,
			Executable: executable,
			Args:       redactedArgs(req.Model, len(req.Prompt)),
			WorkingDir: req.WorkingDir,
			ExitCode:   res.ExitCode,
			Duration:   res.Duration,
			Stdout:     res.Stdout,
			Stderr:     res.Stderr,
			Err:        err,
		}
	}

	if res.ExitCode != 0 {
		return result, &apperr.ProviderError{
			Provider:   string(provider.Claude),
			Model:      req.Model,
			Kind:       apperr.FailureExit,
			Executable: executable,
			Args:       redactedArgs(req.Model, len(req.Prompt)),
			WorkingDir: req.WorkingDir,
			ExitCode:   res.ExitCode,
			Duration:   res.Duration,
			Stdout:     res.Stdout,
			Stderr:     res.Stderr,
		}
	}

	return result, nil
}
