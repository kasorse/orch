package apperr

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/kasorse/orch/internal/domain"
)

// DecisionFailureKind classifies how a call to the decision provider (Jev) failed. It
// drives both retry (Transient) and the process exit code (exitcode.FromError).
type DecisionFailureKind string

const (
	DecisionTimeout         DecisionFailureKind = "timeout"          // per-request timeout or HTTP 408
	DecisionConnection      DecisionFailureKind = "connection"       // dial, DNS, connection reset
	DecisionRateLimited     DecisionFailureKind = "rate_limited"     // HTTP 429
	DecisionUnavailable     DecisionFailureKind = "unavailable"      // HTTP 5xx, 529 (overloaded)
	DecisionAuth            DecisionFailureKind = "auth"             // HTTP 401/403
	DecisionBadRequest      DecisionFailureKind = "bad_request"      // HTTP 422 and other 4xx: our bug
	DecisionInvalidResponse DecisionFailureKind = "invalid_response" // not JSON, or not the expected shape
	DecisionCancelled       DecisionFailureKind = "cancelled"        // SIGINT/SIGTERM
)

var decisionFailureDescriptions = map[DecisionFailureKind]string{
	DecisionTimeout:         "timeout (the request did not complete in time)",
	DecisionConnection:      "connection (the service could not be reached)",
	DecisionRateLimited:     "rate limited (HTTP 429)",
	DecisionUnavailable:     "unavailable (the service is overloaded or failing)",
	DecisionAuth:            "auth (the API key is missing, invalid, or not permitted)",
	DecisionBadRequest:      "bad request (the service rejected the request body)",
	DecisionInvalidResponse: "invalid response (the service answered with an unexpected body)",
	DecisionCancelled:       "cancelled (interrupted)",
}

var transientDecisionFailures = map[DecisionFailureKind]bool{
	DecisionTimeout:     true,
	DecisionConnection:  true,
	DecisionRateLimited: true,
	DecisionUnavailable: true,
}

// DecisionKindForStatus classifies an HTTP error status, whether Jev returned it or a
// decision CLI reported it from its own API call.
func DecisionKindForStatus(status int) DecisionFailureKind {
	switch {
	case status == http.StatusRequestTimeout:
		return DecisionTimeout
	case status == http.StatusTooManyRequests:
		return DecisionRateLimited
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return DecisionAuth
	case status >= 400 && status < 500:
		return DecisionBadRequest
	case status >= 500: // includes 529 "overloaded"
		return DecisionUnavailable
	default: // 1xx/3xx never carry an answer
		return DecisionInvalidResponse
	}
}

// Transient reports whether retrying the same request may succeed.
func (k DecisionFailureKind) Transient() bool { return transientDecisionFailures[k] }

// DecisionError is a failed call to the decision provider. Body is a bounded excerpt
// of the response body, already truncated and redacted by the client: this type only
// renders what it is given and never sees the API key.
type DecisionError struct {
	Provider   string
	Kind       DecisionFailureKind
	StatusCode int // 0 when no HTTP response was received
	Attempts   int // total HTTP attempts made, including retries
	Body       string
	Err        error
	// Usage is what the failed call cost when the provider reported it before failing
	// (an answer that did not parse was still paid for); nil otherwise.
	Usage *domain.Usage
}

func (e *DecisionError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "decision provider %s failed: %s", e.Provider, decisionFailureDescription(e.Kind))
	if e.StatusCode != 0 {
		fmt.Fprintf(&b, ", HTTP %d", e.StatusCode)
	}
	if e.Attempts > 1 {
		fmt.Fprintf(&b, ", after %d attempts", e.Attempts)
	}
	if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	if e.Body != "" {
		fmt.Fprintf(&b, "\n\nresponse body:\n%s", e.Body)
	}
	return b.String()
}
func (e *DecisionError) Unwrap() error { return e.Err }

func decisionFailureDescription(kind DecisionFailureKind) string {
	if desc, ok := decisionFailureDescriptions[kind]; ok {
		return desc
	}
	return string(kind)
}
