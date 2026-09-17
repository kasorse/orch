// Package codex adapts the "codex" CLI to the common provider.Client interface.
// CLI syntax follows Codex's documented non-interactive exec mode; the codex
// executable is not installed on the development machine, so this is a best-effort
// mapping isolated here so it is trivially correctable without touching orchestrator
// code (see buildArgs).
package codex

import (
	"context"
	"errors"
	"fmt"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
)

const executable = "codex"

type Client struct {
	Executor execution.ProcessExecutor
}

func New(executor execution.ProcessExecutor) *Client {
	return &Client{Executor: executor}
}

func buildArgs(model, prompt string) []string {
	return []string{"exec", prompt, "--model", model}
}

// redactedArgs mirrors buildArgs' shape but replaces the prompt with a byte count -
// this is the one place that knows the prompt occupies argv position 1, so
// apperr.ProviderError never has to know or receive the real prompt text.
func redactedArgs(model string, promptLen int) []string {
	return []string{"exec", fmt.Sprintf("<prompt: %d chars>", promptLen), "--model", model}
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
		return result, &apperr.ProviderError{
			Provider:   string(provider.Codex),
			Model:      req.Model,
			Kind:       kind,
			Executable: executable,
			Args:       redactedArgs(req.Model, len(req.Prompt)),
			WorkingDir: req.WorkingDir,
			Duration:   res.Duration,
			Stdout:     res.Stdout,
			Stderr:     res.Stderr,
			Err:        err,
		}
	}

	if res.ExitCode != 0 {
		return result, &apperr.ProviderError{
			Provider:   string(provider.Codex),
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
