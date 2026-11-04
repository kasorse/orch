// Package gemini adapts the "gemini" CLI to the common provider.Client interface.
// CLI syntax follows Gemini CLI's documented non-interactive flags; the gemini
// executable is not installed on the development machine, so this is a best-effort
// mapping isolated here so it is trivially correctable without touching orchestrator
// code (see buildArgs).
package gemini

import (
	"context"
	"errors"
	"fmt"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/provider"
)

const executable = "gemini"

type Client struct {
	Executor execution.ProcessExecutor
}

func New(executor execution.ProcessExecutor) *Client {
	return &Client{Executor: executor}
}

// buildArgs is the unverified best-effort argv. The intended permission translation, for
// whoever installs gemini and verifies it (specs/permissions/SPEC.md §6.4):
// --approval-mode default plus policy-engine deny rules for the shell tools; headless
// policy handling has open issues upstream (google-gemini/gemini-cli#20469), so each
// authority needs a live check before Supports may report it.
func buildArgs(model, prompt string) []string {
	return []string{"-p", prompt, "-m", model}
}

// redactedArgs mirrors buildArgs' shape but replaces the prompt with a byte count -
// this is the one place that knows the prompt occupies argv position 1, so
// apperr.ProviderError never has to know or receive the real prompt text.
func redactedArgs(model string, promptLen int) []string {
	return []string{"-p", fmt.Sprintf("<prompt: %d chars>", promptLen), "-m", model}
}

// Supports is false for every authority. The gemini CLI is not installed on the
// development machine, so no permission translation for it has been verified, and an
// unverified translation must not ship as verified (specs/permissions/SPEC.md §6.4, D13):
// until one is, every route to gemini is refused before any AI call rather than run with
// unknown authority.
func (c *Client) Supports(a domain.Authority) bool {
	return false
}

// Execute refuses every request: gemini supports no authority yet (see Supports). The
// call path below is kept, and tested, for when a translation is verified.
func (c *Client) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	if !c.Supports(req.Authority) {
		return provider.Result{}, &apperr.AuthorityError{
			Provider:  string(provider.Gemini),
			Authority: string(req.Authority),
			Reason:    "No permission translation for gemini has been verified against the real CLI (specs/permissions/SPEC.md §6.4).",
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
			Provider:   string(provider.Gemini),
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
			Provider:   string(provider.Gemini),
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
