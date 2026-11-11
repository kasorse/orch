package apperr

import (
	"fmt"
	"strings"
)

// GitLabFailureKind classifies how a read of the GitLab API failed (specs/revise/SPEC.md §3). It
// drives retry (Transient) and the exit code (exitcode.FromError).
type GitLabFailureKind string

const (
	GitLabAuth            GitLabFailureKind = "auth"             // HTTP 401
	GitLabNotFound        GitLabFailureKind = "not_found"        // HTTP 404, or 403
	GitLabRedirect        GitLabFailureKind = "redirect"         // any HTTP 3xx
	GitLabScope           GitLabFailureKind = "scope"            // the token is not read-only, or not active
	GitLabTimeout         GitLabFailureKind = "timeout"          // per-attempt timeout, HTTP 408
	GitLabConnection      GitLabFailureKind = "connection"       // dial, DNS, connection reset
	GitLabRateLimited     GitLabFailureKind = "rate_limited"     // HTTP 429
	GitLabUnavailable     GitLabFailureKind = "unavailable"      // HTTP 500, 502, 503, 504
	GitLabInvalidResponse GitLabFailureKind = "invalid_response" // unexpected status or body
	GitLabCancelled       GitLabFailureKind = "cancelled"        // SIGINT/SIGTERM
)

// Transient reports whether retrying the same GET may succeed.
func (k GitLabFailureKind) Transient() bool {
	switch k {
	case GitLabTimeout, GitLabConnection, GitLabRateLimited, GitLabUnavailable:
		return true
	}
	return false
}

// GitLabError is a failed GET of the GitLab API. Detail, Body and Err are already redacted and
// truncated by the client: this type never sees the token.
type GitLabError struct {
	// What names the request: "the token's scopes", "merge request group/project!12".
	What       string
	Kind       GitLabFailureKind
	StatusCode int
	Attempts   int
	// Detail completes the message: the token variable (auth, scope), the redirect's host, the
	// problem with the body.
	Detail string
	Body   string
	Err    error
}

func (e *GitLabError) Error() string {
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

func (e *GitLabError) Unwrap() error { return e.Err }

func (e *GitLabError) headline() string {
	what := e.What
	if what == "" {
		what = "the request"
	}
	switch e.Kind {
	case GitLabAuth:
		return "GitLab rejected the token in " + e.Detail
	case GitLabScope:
		return e.Detail
	case GitLabNotFound:
		return fmt.Sprintf("GitLab: %s does not exist, or the token in %s cannot see it", what, e.Detail)
	case GitLabRedirect:
		return fmt.Sprintf("GitLab answered %s with a redirect to %s; check gitlab.base_url", what, e.Detail)
	case GitLabTimeout:
		return "GitLab did not answer in time reading " + what
	case GitLabConnection:
		return "GitLab could not be reached reading " + what
	case GitLabRateLimited:
		return "GitLab is rate limiting requests reading " + what
	case GitLabUnavailable:
		return "GitLab is unavailable reading " + what
	case GitLabCancelled:
		return "reading " + what + " from GitLab was cancelled"
	}
	if e.Detail != "" {
		return fmt.Sprintf("GitLab answered %s with an unexpected response: %s", what, e.Detail)
	}
	return fmt.Sprintf("GitLab answered %s with an unexpected response", what)
}
