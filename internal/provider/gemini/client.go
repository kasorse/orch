// Package gemini adapts the "gemini" CLI to the common provider.Client interface.
// CLI syntax follows Gemini CLI's documented non-interactive flags; the gemini
// executable is not installed on the development machine, so this is a best-effort
// mapping isolated here so it is trivially correctable without touching orchestrator
// code (see buildArgs).
package gemini

import (
	"context"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
)

const executable = "gemini"

type Client struct {
	Executor execution.ProcessExecutor
}

func New(executor execution.ProcessExecutor) *Client {
	return &Client{Executor: executor}
}

func buildArgs(model, prompt string) []string {
	return []string{"-p", prompt, "-m", model}
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
			Provider: string(provider.Gemini),
			Model:    req.Model,
			ExitCode: res.ExitCode,
			Stderr:   res.Stderr,
		}
	}

	return result, nil
}
