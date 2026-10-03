// Package provider defines the common AI-provider abstraction. Workflow and action
// code depends only on Client/Request/Result - provider-specific CLI syntax is
// isolated inside the claude/codex/gemini subpackages.
package provider

import (
	"context"
	"io"
	"time"

	"orchestrator/internal/domain"
)

type Request struct {
	Prompt string
	Model  string
	// Effort is the optional reasoning-effort level; "" leaves the CLI's default.
	Effort     string
	WorkingDir string
	// Authority is the set of tools this call may use (specs/permissions/SPEC.md §5). It
	// has no usable zero value: an adapter refuses "" rather than guess.
	Authority domain.Authority
	// DenyRead lists file-name patterns (the projectctx secret deny list) the call must
	// not be able to read, even under an authority that can read files.
	DenyRead []string
	// Stream is an optional live sink for the CLI's combined output as it arrives
	// (--verbose only). Nil means capture-only - no behavior change.
	Stream io.Writer
}

type Result struct {
	Output   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

type Client interface {
	Execute(ctx context.Context, req Request) (Result, error)
	// Supports reports whether the adapter can enforce a, i.e. translate it into CLI
	// flags that were verified against the real executable. An unsupported authority is
	// refused before any AI call (config.ValidateAuthorities) and again in Execute.
	Supports(a domain.Authority) bool
}

// ID identifies one of the fixed, supported provider CLIs.
type ID string

const (
	Claude ID = "claude"
	Codex  ID = "codex"
	Gemini ID = "gemini"
)

var executables = map[ID]string{
	Claude: "claude",
	Codex:  "codex",
	Gemini: "gemini",
}

// SupportedProviders returns the fixed, ordered list of supported provider IDs.
func SupportedProviders() []ID {
	return []ID{Claude, Codex, Gemini}
}

func IsSupported(id string) bool {
	_, ok := executables[ID(id)]
	return ok
}

// ExecutableFor returns the PATH executable name for a supported provider ID.
func ExecutableFor(id string) (string, bool) {
	exe, ok := executables[ID(id)]
	return exe, ok
}
