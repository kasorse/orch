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
	"orchestrator/internal/domain"
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

// buildArgs is the unverified best-effort argv. The intended permission translation, for
// whoever installs codex and verifies it (specs/permissions/SPEC.md §6.4):
//
//	read:  exec --sandbox read-only --ask-for-approval never
//	edit:  exec --sandbox workspace-write --ask-for-approval never  (network off by default)
//
// Caveat before declaring read supported: codex's read-only sandbox can read the whole
// disk (~/.ssh, ~/.aws) through its shell, and what it reads goes to the model provider,
// so it is weaker than claude's read and needs its own analysis.
func buildArgs(model, prompt string) []string {
	return []string{"exec", prompt, "--model", model}
}

// redactedArgs mirrors buildArgs' shape but replaces the prompt with a byte count -
// this is the one place that knows the prompt occupies argv position 1, so
// apperr.ProviderError never has to know or receive the real prompt text.
func redactedArgs(model string, promptLen int) []string {
	return []string{"exec", fmt.Sprintf("<prompt: %d chars>", promptLen), "--model", model}
}

// Supports is false for every authority. The codex CLI is not installed on the
// development machine, so no permission translation for it has been verified, and an
// unverified translation must not ship as verified (specs/permissions/SPEC.md §6.4, D13):
// until one is, every route to codex is refused before any AI call rather than run with
// unknown authority.
func (c *Client) Supports(a domain.Authority) bool {
	return false
}

// Execute refuses every request: codex supports no authority yet (see Supports). The
// call path below is kept, and tested, for when a translation is verified.
func (c *Client) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	if !c.Supports(req.Authority) {
		return provider.Result{}, &apperr.AuthorityError{
			Provider:  string(provider.Codex),
			Authority: string(req.Authority),
			Reason:    "No permission translation for codex has been verified against the real CLI (specs/permissions/SPEC.md §6.4).",
		}
	}
	return c.run(ctx, req)
}

func (c *Client) run(ctx context.Context, req provider.Request) (provider.Result, error) {
	args := buildArgs(req.Model, req.Prompt)

	spec := execution.Spec{
		Executable: executable,
		Args:       args,
		WorkingDir: req.WorkingDir,
		Env:        execution.ScrubbedEnv(),
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
			ExitCode:   res.ExitCode,
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
