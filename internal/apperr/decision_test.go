package apperr

import (
	"errors"
	"strings"
	"testing"
)

func TestDecisionFailureKind_Transient(t *testing.T) {
	cases := map[DecisionFailureKind]bool{
		DecisionTimeout:         true,
		DecisionConnection:      true,
		DecisionRateLimited:     true,
		DecisionUnavailable:     true,
		DecisionAuth:            false,
		DecisionBadRequest:      false,
		DecisionInvalidResponse: false,
		DecisionCancelled:       false,
	}
	for kind, want := range cases {
		if got := kind.Transient(); got != want {
			t.Errorf("%s.Transient() = %v, want %v", kind, got, want)
		}
	}
}

func TestDecisionError_Message(t *testing.T) {
	cause := errors.New("overloaded")
	err := &DecisionError{
		Provider:   "jev",
		Kind:       DecisionUnavailable,
		StatusCode: 529,
		Attempts:   2,
		Body:       `{"error":"overloaded"}`,
		Err:        cause,
	}
	msg := err.Error()
	for _, want := range []string{
		"decision provider jev failed",
		"unavailable",
		"HTTP 529",
		"after 2 attempts",
		"overloaded",
		`response body:` + "\n" + `{"error":"overloaded"}`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected message to contain %q, got: %s", want, msg)
		}
	}
	if !errors.Is(err, cause) {
		t.Error("expected DecisionError to unwrap to its cause")
	}
}

func TestDecisionError_OmitsEmptyParts(t *testing.T) {
	msg := (&DecisionError{Provider: "jev", Kind: DecisionConnection, Attempts: 1}).Error()
	for _, unwanted := range []string{"HTTP", "attempts", "response body"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("expected message not to contain %q, got: %s", unwanted, msg)
		}
	}
}
