package apperr

import (
	"fmt"
	"strings"
)

// JiraFailureKind classifies how fetching an issue for run --jira failed
// (specs/jira/SPEC.md §14). It drives both retry (Transient) and the process exit code
// (exitcode.FromError).
type JiraFailureKind string

const (
	JiraAuth            JiraFailureKind = "auth"             // HTTP 401
	JiraNotFound        JiraFailureKind = "not_found"        // HTTP 404, or 403: Jira does not tell the two apart
	JiraRedirect        JiraFailureKind = "redirect"         // any HTTP 3xx: a proxy or login page, never data
	JiraTimeout         JiraFailureKind = "timeout"          // per-attempt timeout, HTTP 408, or the run's deadline
	JiraConnection      JiraFailureKind = "connection"       // dial, DNS, connection reset
	JiraRateLimited     JiraFailureKind = "rate_limited"     // HTTP 429
	JiraUnavailable     JiraFailureKind = "unavailable"      // HTTP 500, 502, 503, 504
	JiraInvalidResponse JiraFailureKind = "invalid_response" // unexpected status or body
	JiraCancelled       JiraFailureKind = "cancelled"        // SIGINT/SIGTERM
	JiraRejected        JiraFailureKind = "rejected"         // HTTP 400 to a creation: Jira says what is wrong with it
)

// JiraError.Op of a failed creation of a child issue and of the write-back after a merge request
// (specs/delivery/SPEC.md §10): reading the issue's status or its transitions, posting a comment,
// making a transition.
const (
	JiraOpCreate      = "create"
	JiraOpStatus      = "status"
	JiraOpTransitions = "transitions"
	JiraOpComment     = "comment"
	JiraOpTransition  = "transition"
)

var transientJiraFailures = map[JiraFailureKind]bool{
	JiraTimeout:     true,
	JiraConnection:  true,
	JiraRateLimited: true,
	JiraUnavailable: true,
}

// Transient reports whether retrying the same request may succeed.
func (k JiraFailureKind) Transient() bool { return transientJiraFailures[k] }

// JiraError is a failed fetch of one Jira issue, or a failed creation of a child of it. Body
// and Err are already truncated and redacted by the client: this type only renders what it is
// given and never sees a credential.
type JiraError struct {
	// Key is the issue fetched; for a creation, the parent of the child.
	Key string
	// Op is "" for a fetch and JiraOpCreate for a creation.
	Op         string
	Kind       JiraFailureKind
	StatusCode int // 0 when no HTTP response was received
	Attempts   int // total HTTP attempts made, including retries
	// NotSent is set when the request was never handed to the transport, so nothing can have
	// reached Jira.
	NotSent bool
	// Detail completes the message for the kind: the credential variable names (auth,
	// not_found), the redirect's host (redirect), the attempt timeout (timeout), or the
	// JSON path and the problem (invalid_response).
	Detail string
	Body   string
	Err    error
}

func (e *JiraError) Error() string {
	var b strings.Builder
	b.WriteString(e.headline())
	var meta []string
	if e.StatusCode != 0 {
		meta = append(meta, fmt.Sprintf("HTTP %d", e.StatusCode))
	}
	if e.Attempts > 1 {
		meta = append(meta, fmt.Sprintf("after %d attempts", e.Attempts))
	}
	if len(meta) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(meta, " "))
	}
	if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	if e.Body != "" {
		fmt.Fprintf(&b, "\n\nresponse body:\n%s", e.Body)
	}
	return b.String()
}

func (e *JiraError) Unwrap() error { return e.Err }

func (e *JiraError) headline() string {
	key := e.Key
	if key == "" {
		key = "the issue"
	}
	switch e.Op {
	case JiraOpCreate:
		if h, ok := e.createHeadline(key); ok {
			return h
		}
	case JiraOpStatus, JiraOpTransitions, JiraOpComment, JiraOpTransition:
		if h, ok := e.writeHeadline(key); ok {
			return h
		}
	}
	switch e.Kind {
	case JiraAuth:
		if e.Detail != "" {
			return "Jira rejected the credentials in " + e.Detail
		}
		return "Jira rejected the credentials"
	case JiraNotFound:
		if e.Detail != "" {
			return fmt.Sprintf("%s does not exist, or the account behind %s cannot see it", key, e.Detail)
		}
		return fmt.Sprintf("%s does not exist, or this account cannot see it", key)
	case JiraRedirect:
		if e.Detail != "" {
			return fmt.Sprintf("Jira answered with a redirect to %s; check jira.base_url", e.Detail)
		}
		return "Jira answered with a redirect; check jira.base_url"
	case JiraTimeout:
		if e.Detail != "" {
			return "Jira did not answer within " + e.Detail
		}
		return "Jira did not answer in time"
	case JiraConnection:
		return "Jira could not be reached"
	case JiraRateLimited:
		return "Jira is rate limiting requests"
	case JiraUnavailable:
		return "Jira is unavailable"
	case JiraInvalidResponse:
		if e.Detail != "" {
			return fmt.Sprintf("Jira answered %s with an unexpected response: %s", key, e.Detail)
		}
		return fmt.Sprintf("Jira answered %s with an unexpected response", key)
	case JiraCancelled:
		return fmt.Sprintf("fetching %s from Jira was cancelled", key)
	}
	return fmt.Sprintf("fetching %s from Jira failed: %s", key, e.Kind)
}

// writeHeadline words the failures of the write-back to an issue (reading its status or
// transitions, a comment, a transition); the others read the same as for a fetch.
func (e *JiraError) writeHeadline(key string) (string, bool) {
	what := map[string]string{
		JiraOpStatus:      "reading the status of " + key,
		JiraOpTransitions: "reading the transitions of " + key,
		JiraOpComment:     "commenting on " + key,
		JiraOpTransition:  "moving " + key,
	}[e.Op]
	switch e.Kind {
	case JiraNotFound:
		who := "this account"
		if e.Detail != "" {
			who = "the account behind " + e.Detail
		}
		return fmt.Sprintf("%s does not exist, or %s may not %s it", key, who, e.opVerb()), true
	case JiraRejected:
		if e.Detail != "" {
			return fmt.Sprintf("Jira rejected %s: %s", what, e.Detail), true
		}
		return "Jira rejected " + what, true
	case JiraInvalidResponse:
		if e.Detail != "" {
			return fmt.Sprintf("Jira answered %s with an unexpected response: %s", what, e.Detail), true
		}
		return fmt.Sprintf("Jira answered %s with an unexpected response", what), true
	case JiraCancelled:
		return what + " was cancelled", true
	}
	return "", false
}

// opVerb is what a write-back operation does to the issue, for "may not <verb> it".
func (e *JiraError) opVerb() string {
	switch e.Op {
	case JiraOpComment:
		return "comment on"
	case JiraOpTransition:
		return "move"
	}
	return "read"
}

// createHeadline words the failures that mean something else for a creation than for a fetch;
// the others read the same and fall through to headline.
func (e *JiraError) createHeadline(parent string) (string, bool) {
	switch e.Kind {
	case JiraNotFound:
		who := "this account"
		if e.Detail != "" {
			who = "the account behind " + e.Detail
		}
		return fmt.Sprintf("Jira did not let %s create a child of %s: the project, the issue type or the parent does not exist, or the account may not create issues there", who, parent), true
	case JiraRejected:
		if e.Detail != "" {
			return fmt.Sprintf("Jira rejected the child issue of %s: %s", parent, e.Detail), true
		}
		return fmt.Sprintf("Jira rejected the child issue of %s", parent), true
	case JiraInvalidResponse:
		if e.Detail != "" {
			return fmt.Sprintf("Jira answered the creation of a child of %s with an unexpected response: %s", parent, e.Detail), true
		}
		return fmt.Sprintf("Jira answered the creation of a child of %s with an unexpected response", parent), true
	case JiraCancelled:
		return fmt.Sprintf("creating a child issue of %s was cancelled", parent), true
	}
	return "", false
}
