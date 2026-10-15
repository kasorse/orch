// Package apperr defines the typed error categories used throughout the orchestrator.
// Every type follows the same Error()/Unwrap() pattern so callers can use errors.Is/As
// against wrapped causes.
package apperr

import (
	"fmt"
	"strings"
	"time"
)

type ConfigError struct {
	Msg string
	Err error
}

func (e *ConfigError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}
func (e *ConfigError) Unwrap() error { return e.Err }

func NewConfigError(msg string, cause error) *ConfigError {
	return &ConfigError{Msg: msg, Err: cause}
}

type DependencyError struct {
	Msg string
	Err error
}

func (e *DependencyError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}
func (e *DependencyError) Unwrap() error { return e.Err }

func NewDependencyError(msg string, cause error) *DependencyError {
	return &DependencyError{Msg: msg, Err: cause}
}

// FailureKind classifies how a provider CLI invocation failed, so the rendered message
// can say something more actionable than "exit code: 1".
type FailureKind string

const (
	FailureSpawn     FailureKind = "spawn"     // executable not found / not executable
	FailureExit      FailureKind = "exit"      // ran, exited non-zero
	FailureTimeout   FailureKind = "timeout"   // execution.timeout exceeded
	FailureCancelled FailureKind = "cancelled" // SIGINT/SIGTERM
	FailureAuth      FailureKind = "auth"      // the CLI did not use the run's credential, or it was refused
)

var failureDescriptions = map[FailureKind]string{
	FailureSpawn:     "spawn (the CLI could not be started)",
	FailureExit:      "exit (the CLI ran and exited non-zero)",
	FailureTimeout:   "timeout (exceeded execution.timeout)",
	FailureCancelled: "cancelled (interrupted)",
	FailureAuth:      "auth (the CLI did not use the run's credential, or the credential was refused)",
}

// ProviderError carries the actionable context spelled out in the spec: provider,
// action, model, how it failed, the redacted command line, and both output streams -
// never secrets or full environment variables. Args must already have any prompt
// argument redacted by the caller (the provider adapter, which alone knows argv shape);
// this package only renders whatever it is given.
type ProviderError struct {
	Provider   string
	Action     string
	Model      string
	Kind       FailureKind
	Executable string
	Args       []string
	// PromptBytes is the size of a prompt the adapter passed on stdin; the message shows
	// the size, never the text. Zero for a provider that puts the prompt in Args.
	PromptBytes int
	WorkingDir  string
	ExitCode    int
	Duration    time.Duration
	Stdout      string
	Stderr      string
	LogPath     string // run-relative path to the raw logs, when persisted
	Err         error
	// Detail is one sentence the adapter adds to the rendered message, e.g. which credential
	// mode a FailureAuth concerns. The adapter has already redacted it.
	Detail string
	// APIStatus is the HTTP status of the API error the CLI reported as its answer (for
	// claude, "API Error: 400 ..."); 0 when it reported none.
	APIStatus int

	// ConfiguredTimeout is set only when Kind == FailureTimeout and a limit tighter than
	// the run-wide execution.timeout was the one that fired: an action's own
	// actions.<name>.timeout override, or the per-call timeout of a call outside the
	// steps (decision.timeout). Zero means execution.timeout fired instead.
	ConfiguredTimeout time.Duration
	// TimeoutSetting names the config key ConfiguredTimeout came from, e.g.
	// "decision.timeout". Empty means actions.<Action>.timeout.
	TimeoutSetting string
}

const maxStreamInError = 4000

func (e *ProviderError) Error() string {
	var b strings.Builder
	b.WriteString("provider execution failed\n\n")
	fmt.Fprintf(&b, "provider: %s\n", e.Provider)
	fmt.Fprintf(&b, "action: %s\n", e.Action)
	fmt.Fprintf(&b, "model: %s\n", e.Model)
	if e.Kind == FailureTimeout && e.ConfiguredTimeout > 0 {
		setting := e.TimeoutSetting
		if setting == "" {
			setting = "actions." + e.Action + ".timeout"
		}
		fmt.Fprintf(&b, "failure: timeout (exceeded %s = %s)\n", setting, e.ConfiguredTimeout)
	} else if e.Kind != "" {
		fmt.Fprintf(&b, "failure: %s\n", failureDescription(e.Kind))
	}
	if e.Detail != "" {
		fmt.Fprintf(&b, "detail: %s\n", e.Detail)
	}
	if e.Executable != "" {
		fmt.Fprintf(&b, "command: %s\n", commandLine(e.Executable, e.Args))
	}
	if e.PromptBytes > 0 {
		fmt.Fprintf(&b, "prompt: %d bytes on stdin\n", e.PromptBytes)
	}
	if e.WorkingDir != "" {
		fmt.Fprintf(&b, "working dir: %s\n", e.WorkingDir)
	}
	fmt.Fprintf(&b, "exit code: %d\n", e.ExitCode)
	if e.Duration > 0 {
		fmt.Fprintf(&b, "duration: %s\n", e.Duration.Round(time.Millisecond*100))
	}
	b.WriteString("\nstdout:\n")
	b.WriteString(truncateTail(e.Stdout))
	b.WriteString("\n\nstderr:\n")
	b.WriteString(truncateTail(e.Stderr))
	if e.LogPath != "" {
		fmt.Fprintf(&b, "\n\nfull output: %s\n", e.LogPath)
	}
	return b.String()
}
func (e *ProviderError) Unwrap() error { return e.Err }

func failureDescription(kind FailureKind) string {
	if desc, ok := failureDescriptions[kind]; ok {
		return desc
	}
	return string(kind)
}

// commandLine renders argv so it can be pasted into a shell: an argument that is empty or
// holds anything but plain characters (a --settings JSON document, --setting-sources "")
// is single-quoted, since joining it bare would lose or split it.
func commandLine(executable string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, executable)
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// shellQuote leaves the adapters' "<prompt: N chars>" placeholder alone: it stands in for
// the redacted prompt and is not meant to be pasted.
func shellQuote(a string) string {
	if strings.HasPrefix(a, "<prompt: ") && strings.HasSuffix(a, " chars>") {
		return a
	}
	if a != "" && strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.,/:=+@%<>") == "" {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
}

// truncateTail renders an empty stream explicitly (rather than a blank, easily-missed
// line) and keeps only the last maxStreamInError bytes of a long one, since the failing
// line in a provider's output is almost always near the end.
func truncateTail(s string) string {
	if s == "" {
		return "(empty)"
	}
	if len(s) <= maxStreamInError {
		return s
	}
	return fmt.Sprintf("... [truncated, showing last %d bytes]\n%s", maxStreamInError, s[len(s)-maxStreamInError:])
}

type WorkflowError struct {
	Msg string
	Err error
}

func (e *WorkflowError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}
func (e *WorkflowError) Unwrap() error { return e.Err }

type RepositoryError struct {
	Msg string
	Err error
}

func (e *RepositoryError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}
func (e *RepositoryError) Unwrap() error { return e.Err }

type ExecutionError struct {
	Msg string
	Err error
}

func (e *ExecutionError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}
func (e *ExecutionError) Unwrap() error { return e.Err }

// LimitExceededError signals that a workflow limit (max steps / max fix iterations) was
// reached. Reason is pre-formatted for direct CLI display.
type LimitExceededError struct {
	Reason string
}

func (e *LimitExceededError) Error() string { return e.Reason }

// AuthorityError means a provider cannot enforce the authority a call requires
// (specs/permissions/SPEC.md §6). It is raised before any AI call by
// config.ValidateAuthorities, and again by an adapter's Execute as a backstop. It is a
// misconfiguration - the wrong provider for the job - so it maps to exit code 2.
type AuthorityError struct {
	Provider  string
	Route     string // the action or pseudo-action (translation, decision); "" if unknown
	Authority string
	Reason    string
	Remedy    string
}

func (e *AuthorityError) Error() string {
	route := "this call"
	if e.Route != "" {
		route = fmt.Sprintf("%q", e.Route)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "provider %q cannot enforce the %q authority required by %s", e.Provider, e.Authority, route)
	if e.Reason != "" {
		fmt.Fprintf(&b, "\n\n%s", e.Reason)
	}
	if e.Remedy != "" {
		fmt.Fprintf(&b, "\n\nremedy: %s", e.Remedy)
	}
	return b.String()
}
