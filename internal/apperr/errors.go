// Package apperr defines the typed error categories used throughout the orchestrator.
// Every type follows the same Error()/Unwrap() pattern so callers can use errors.Is/As
// against wrapped causes.
package apperr

import "fmt"

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

// ProviderError carries the actionable context spelled out in the spec: provider,
// action, model, exit code, and stderr - never secrets or full environment variables.
type ProviderError struct {
	Provider string
	Action   string
	Model    string
	ExitCode int
	Stderr   string
	Err      error
}

const maxStderrInError = 4000

func (e *ProviderError) Error() string {
	stderr := e.Stderr
	if len(stderr) > maxStderrInError {
		stderr = stderr[len(stderr)-maxStderrInError:]
	}
	return fmt.Sprintf(
		"provider execution failed\n\nprovider: %s\naction: %s\nmodel: %s\nexit code: %d\n\nstderr:\n%s",
		e.Provider, e.Action, e.Model, e.ExitCode, stderr,
	)
}
func (e *ProviderError) Unwrap() error { return e.Err }

type ClassificationError struct {
	Raw string
	Err error
}

func (e *ClassificationError) Error() string {
	return fmt.Sprintf("classification failed: %v\n\nraw output:\n%s", e.Err, e.Raw)
}
func (e *ClassificationError) Unwrap() error { return e.Err }

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
