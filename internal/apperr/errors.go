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
)

var failureDescriptions = map[FailureKind]string{
	FailureSpawn:     "spawn (the CLI could not be started)",
	FailureExit:      "exit (the CLI ran and exited non-zero)",
	FailureTimeout:   "timeout (exceeded execution.timeout)",
	FailureCancelled: "cancelled (interrupted)",
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
	WorkingDir string
	ExitCode   int
	Duration   time.Duration
	Stdout     string
	Stderr     string
	LogPath    string // run-relative path to the raw logs, when persisted
	Err        error

	// ConfiguredTimeout is set only when Kind == FailureTimeout and the failing action
	// had its own actions.<name>.timeout override in effect. Zero means the run-wide
	// execution.timeout fired instead (or the action has no override at all).
	ConfiguredTimeout time.Duration
}

const maxStreamInError = 4000

func (e *ProviderError) Error() string {
	var b strings.Builder
	b.WriteString("provider execution failed\n\n")
	fmt.Fprintf(&b, "provider: %s\n", e.Provider)
	fmt.Fprintf(&b, "action: %s\n", e.Action)
	fmt.Fprintf(&b, "model: %s\n", e.Model)
	if e.Kind == FailureTimeout && e.ConfiguredTimeout > 0 {
		fmt.Fprintf(&b, "failure: timeout (exceeded actions.%s.timeout = %s)\n", e.Action, e.ConfiguredTimeout)
	} else if e.Kind != "" {
		fmt.Fprintf(&b, "failure: %s\n", failureDescription(e.Kind))
	}
	if e.Executable != "" {
		fmt.Fprintf(&b, "command: %s\n", commandLine(e.Executable, e.Args))
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

func commandLine(executable string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, executable)
	parts = append(parts, args...)
	return strings.Join(parts, " ")
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
