// Package claude adapts the "claude" CLI to the common provider.Client interface.
// CLI syntax observed via `claude --help` on the development machine: -p/--print for
// non-interactive output, --model <model>, and --permission-mode bypassPermissions to
// run fully unattended (no interactive approval prompts, since this runs headless).
package claude

import (
	"context"

	"orchestrator/internal/apperr"
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
		// Action is left blank here - the action dispatcher fills it in, since it is
		// the only layer that knows which action invoked this provider call.
		return result, &apperr.ProviderError{
			Provider: string(provider.Claude),
			Model:    req.Model,
			ExitCode: res.ExitCode,
			Stderr:   res.Stderr,
		}
	}

	return result, nil
}
