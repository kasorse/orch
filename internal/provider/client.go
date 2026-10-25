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
	// Activity, if set, is told what the model is doing as it happens ("Read
	// src/app.ts"), for a progress display. Adapters that cannot tell never call it.
	Activity func(string)
	// Extensions are the operator's opt-in MCP servers and user settings for this call
	// (config.ExtensionsConfig, resolved for the action). Zero means none.
	Extensions Extensions
}

// Extensions is what one call may use beyond its authority's built-in tools.
type Extensions struct {
	// MCPConfig is the --mcp-config JSON document ({"mcpServers": {...}}), "" for none.
	MCPConfig string
	// MCPTools are the fully qualified MCP tool names the model may call
	// ("mcp__graphify__query_graph", or "mcp__graphify" for every tool of a server).
	MCPTools []string
	// UserSettings loads the operator's user-level settings: plugins, skills, hooks.
	UserSettings bool
}

// VersionVerifier is implemented by adapters whose permission flags were verified on a
// range of CLI versions (specs/hardening/SPEC.md §7).
type VersionVerifier interface {
	// CLIVersion runs the CLI's version command and returns the version string and whether
	// it is inside the verified range. A version that cannot be parsed is not verified.
	CLIVersion(ctx context.Context) (version string, verified bool, err error)
	// VerifiedRange describes the range for messages, e.g. ">=2.1.283 <2.2.0".
	VerifiedRange() string
}

type Result struct {
	Output   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	// Usage is the call's token/cost/rate-limit report, nil when the CLI gives none.
	Usage *domain.Usage
	// Call is what the CLI reported about the models and tools the call used, nil when it
	// gives nothing; the caller adds the prompt's size.
	Call *domain.CallReport
}

// UsageObserver is implemented by progress observers that want each call's Usage. Call
// sites check for it with a type assertion, so observers that don't care need nothing.
type UsageObserver interface {
	ProviderUsage(u domain.Usage)
}

// ActivityObserver is implemented by progress observers that show live model activity.
type ActivityObserver interface {
	ProviderActivity(what string)
}

// ActivityHook returns observer's ProviderActivity, or nil when it has none.
func ActivityHook(observer any) func(string) {
	if a, ok := observer.(ActivityObserver); ok {
		return a.ProviderActivity
	}
	return nil
}

// ReportUsage hands res.Usage to observer when both exist.
func ReportUsage(observer any, res Result) {
	if u, ok := observer.(UsageObserver); ok && res.Usage != nil {
		u.ProviderUsage(*res.Usage)
	}
}

type Client interface {
	Execute(ctx context.Context, req Request) (Result, error)
	// Supports reports whether the adapter can enforce a, i.e. translate it into CLI
	// flags that were verified against the real executable. An unsupported authority is
	// refused before any AI call (config.ValidateAuthorities) and again in Execute.
	Supports(a domain.Authority) bool
}

// CostReporter is implemented by adapters whose CLI reports a call's cost in USD. A budget
// (limits.max_cost_usd) is refused for a route whose adapter does not implement it.
type CostReporter interface {
	ReportsCost() bool
}

// MCPSupporter is implemented by adapters that pass Extensions.MCPConfig to their CLI. A call
// to any other adapter gets no MCP server, so the orchestrator's own tools are not offered to
// it.
type MCPSupporter interface {
	SupportsMCP() bool
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
