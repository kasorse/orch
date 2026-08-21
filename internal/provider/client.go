// Package provider defines the common AI-provider abstraction. Workflow and action
// code depends only on Client/Request/Result - provider-specific CLI syntax is
// isolated inside the claude/codex/gemini subpackages.
package provider

import (
	"context"
	"time"
)

type Request struct {
	Prompt     string
	Model      string
	WorkingDir string
}

type Result struct {
	Output   string
	ExitCode int
	Duration time.Duration
}

type Client interface {
	Execute(ctx context.Context, req Request) (Result, error)
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
