package apperr

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProviderError_Message(t *testing.T) {
	err := &ProviderError{
		Provider: "claude",
		Action:   "implement",
		Model:    "opus",
		ExitCode: 1,
		Stderr:   "panic: something broke",
	}

	msg := err.Error()
	for _, want := range []string{
		"provider execution failed",
		"provider: claude",
		"action: implement",
		"model: opus",
		"exit code: 1",
		"stderr:",
		"panic: something broke",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error message to contain %q, got: %s", want, msg)
		}
	}
}

func TestProviderError_TruncatesLongStderr(t *testing.T) {
	longStderr := strings.Repeat("x", maxStreamInError+1000)
	err := &ProviderError{Stderr: longStderr}
	msg := err.Error()
	if len(msg) > maxStreamInError+1000 {
		t.Errorf("expected stderr to be truncated, message length was %d", len(msg))
	}
}

func TestProviderError_RendersFailureKindAndCommand(t *testing.T) {
	err := &ProviderError{
		Provider:   "claude",
		Action:     "implement",
		Model:      "opus",
		Kind:       FailureExit,
		Executable: "claude",
		Args:       []string{"--print", "<prompt: 42 chars>"},
		WorkingDir: "/repo",
		ExitCode:   1,
		Stdout:     "API Error: Connection error.",
		LogPath:    ".orchestrator/runs/abc/logs/000-implement.stdout.log",
	}
	msg := err.Error()
	for _, want := range []string{
		"failure: exit (the CLI ran and exited non-zero)",
		"command: claude --print <prompt: 42 chars>",
		"working dir: /repo",
		"stdout:\nAPI Error: Connection error.",
		"stderr:\n(empty)",
		"full output: .orchestrator/runs/abc/logs/000-implement.stdout.log",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error message to contain %q, got: %s", want, msg)
		}
	}
}

func TestProviderError_TimeoutMentionsActionOverrideWhenSet(t *testing.T) {
	err := &ProviderError{
		Provider:          "codex",
		Action:            "review",
		Model:             "gpt",
		Kind:              FailureTimeout,
		ConfiguredTimeout: 10 * time.Minute,
	}
	msg := err.Error()
	if !strings.Contains(msg, "failure: timeout (exceeded actions.review.timeout = 10m0s)") {
		t.Errorf("expected message to name the action-specific override, got: %s", msg)
	}
}

func TestProviderError_TimeoutFallsBackToExecutionTimeoutMessageWhenNoOverride(t *testing.T) {
	err := &ProviderError{
		Provider: "codex",
		Action:   "review",
		Model:    "gpt",
		Kind:     FailureTimeout,
	}
	msg := err.Error()
	if !strings.Contains(msg, "failure: timeout (exceeded execution.timeout)") {
		t.Errorf("expected message to fall back to the run-wide timeout wording, got: %s", msg)
	}
}

func TestConfigError_Unwrap(t *testing.T) {
	cause := errors.New("root cause")
	err := NewConfigError("bad config", cause)

	if !errors.Is(err, cause) {
		t.Error("expected errors.Is to find wrapped cause")
	}

	var target *ConfigError
	if !errors.As(err, &target) {
		t.Error("expected errors.As to match *ConfigError")
	}
}
