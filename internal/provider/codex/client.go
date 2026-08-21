// Package codex adapts the "codex" CLI to the common provider.Client interface.
// CLI syntax follows Codex's documented non-interactive exec mode; the codex
// executable is not installed on the development machine, so this is a best-effort
// mapping isolated here so it is trivially correctable without touching orchestrator
// code (see buildArgs).
package codex

import (
	"context"

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

func (c *Client) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	args := buildArgs(req.Model, req.Prompt)
	res, err := c.Executor.Run(ctx, executable, args, "", req.WorkingDir)
	if err != nil {
		return provider.Result{}, err
	}

	result := provider.Result{
		Output:   res.Stdout,
		ExitCode: res.ExitCode,
		Duration: res.Duration,
	}

	if res.ExitCode != 0 {
		return result, &apperr.ProviderError{
			Provider: string(provider.Codex),
			Model:    req.Model,
			ExitCode: res.ExitCode,
			Stderr:   res.Stderr,
		}
	}

	return result, nil
}
