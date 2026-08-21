package apperr

import (
	"errors"
	"strings"
	"testing"
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
	longStderr := strings.Repeat("x", maxStderrInError+1000)
	err := &ProviderError{Stderr: longStderr}
	msg := err.Error()
	if len(msg) > maxStderrInError+500 {
		t.Errorf("expected stderr to be truncated, message length was %d", len(msg))
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

func TestClassificationError_Message(t *testing.T) {
	err := &ClassificationError{Raw: "not json", Err: errors.New("invalid character")}
	msg := err.Error()
	if !strings.Contains(msg, "not json") || !strings.Contains(msg, "invalid character") {
		t.Errorf("unexpected message: %s", msg)
	}
}
