// Package gitlab is the read-only GitLab API client of orchestrator revise (specs/revise/SPEC.md
// §3). It sends only GET, only to the configured base URL, only for projects under the allowed
// groups, follows no redirect and decodes strictly. It imports only domain, apperr and the
// standard library (imports_test.go). The token goes in the PRIVATE-TOKEN header and nowhere
// else; every error is redacted.
package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

const (
	apiPath         = "/api/v4"
	maxResponseBody = 1 << 20
	maxErrorBody    = 512
	baseBackoff     = 250 * time.Millisecond
	redacted        = "[REDACTED]"

	// DefaultTimeout bounds one attempt; DefaultRetries is how often a transient failure is retried.
	DefaultTimeout = 30 * time.Second
	DefaultRetries = 2
)

// readOnlyScopes are the scopes a token may have (SPEC.md §3, A2): any other one can write.
var readOnlyScopes = []string{"read_api", "read_repository", "read_user"}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// projectPattern is a project path: slash-separated segments of letters, digits, _ . -.
var projectPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)+$`)

// Token is the API token. It prints as [REDACTED], and the value sits behind a pointer so a raw
// print of a Client shows only an address.
type Token struct{ s *string }

// NewToken wraps the value app read from gitlab.api_token_env.
func NewToken(v string) Token { return Token{s: &v} }

func (Token) String() string   { return redacted }
func (Token) GoString() string { return redacted }

func (t Token) value() string {
	if t.s == nil {
		return ""
	}
	return *t.s
}

// Options configures the client from the gitlab section.
type Options struct {
	BaseURL       string
	AllowedGroups []string
	// TokenEnv names the token's variable, for messages only.
	TokenEnv   string
	Timeout    time.Duration
	MaxRetries int
	UserAgent  string
}

// Client reads merge requests from one GitLab.
type Client struct {
	opts  Options
	token Token
	http  *http.Client
	sleep func(ctx context.Context, d time.Duration) error
}

// NewHTTPClient builds the client. Zero Timeout and MaxRetries take the defaults.
func NewHTTPClient(opts Options, token Token) *Client {
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxRetries == 0 {
		opts.MaxRetries = DefaultRetries
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "orchestrator"
	}
	return &Client{
		opts:  opts,
		token: token,
		http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		sleep: sleepCtx,
	}
}

// Allowed reports whether project is under one of groups, matched on whole path segments.
func Allowed(groups []string, project string) bool {
	if !validProject(project) {
		return false
	}
	for _, g := range groups {
		g = strings.Trim(g, "/")
		if g != "" && (project == g || strings.HasPrefix(project, g+"/")) {
			return true
		}
	}
	return false
}

// ParseMergeRequestURL takes a merge request URL apart: it must be https (http only for a
// loopback base), on baseURL's host and under its path, of the form <base>/<project>/-/
// merge_requests/<iid> with nothing after but an optional "/" or a fragment-free query-free end.
func ParseMergeRequestURL(baseURL, raw string) (project string, iid int, ok bool) {
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || base.Host == "" {
		return "", 0, false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != base.Scheme || !strings.EqualFold(u.Host, base.Host) || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" {
		return "", 0, false
	}
	rest, found := strings.CutPrefix(u.Path, base.Path+"/")
	if !found {
		return "", 0, false
	}
	rest = strings.TrimSuffix(rest, "/")
	project, num, found := strings.Cut(rest, "/-/merge_requests/")
	if !found || !validProject(project) {
		return "", 0, false
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 || strconv.Itoa(n) != num {
		return "", 0, false
	}
	return project, n, true
}

// validProject is a project path with no "." or ".." segment.
func validProject(p string) bool {
	return projectPattern.MatchString(p) && !slices.ContainsFunc(strings.Split(p, "/"), func(s string) bool { return s == "." || s == ".." })
}

// MergeRequestURL is the merge request's URL as ParseMergeRequestURL accepts it.
func MergeRequestURL(baseURL, project string, iid int) string {
	return strings.TrimRight(baseURL, "/") + "/" + project + "/-/merge_requests/" + strconv.Itoa(iid)
}

// CheckReadOnly makes the one GET of the token's own record and refuses a token that could write,
// is revoked or is not active (SPEC.md §3, A2).
func (c *Client) CheckReadOnly(ctx context.Context) error {
	const what = "the token's scopes"
	raw, gerr := c.get(ctx, apiPath+"/personal_access_tokens/self", what)
	if gerr != nil {
		return gerr
	}
	var tok struct {
		Active  *bool    `json:"active"`
		Revoked *bool    `json:"revoked"`
		Scopes  []string `json:"scopes"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil || tok.Active == nil || tok.Revoked == nil || tok.Scopes == nil {
		return c.fail(what, apperr.GitLabInvalidResponse, http.StatusOK, "not a token record (active, revoked, scopes)", "", nil)
	}
	var writes []string
	for _, s := range tok.Scopes {
		if !slices.Contains(readOnlyScopes, s) {
			writes = append(writes, s)
		}
	}
	switch {
	case *tok.Revoked || !*tok.Active:
		return c.scope("is revoked or not active")
	case len(tok.Scopes) == 0:
		return c.scope("has no scope")
	case len(writes) > 0:
		return c.scope(fmt.Sprintf("must name a read-only token (scopes: %s; allowed: %s)",
			strings.Join(writes, ", "), strings.Join(readOnlyScopes, ", ")))
	}
	return nil
}

func (c *Client) scope(why string) error {
	return &apperr.GitLabError{What: "the token's scopes", Kind: apperr.GitLabScope,
		Detail: fmt.Sprintf("gitlab.api_token_env (%s) %s", c.opts.TokenEnv, why)}
}

// MergeRequest reads one merge request of project.
func (c *Client) MergeRequest(ctx context.Context, project string, iid int) (domain.MergeRequest, error) {
	what := fmt.Sprintf("merge request %s!%d", project, iid)
	if err := c.checkProject(project, what); err != nil {
		return domain.MergeRequest{}, err
	}
	raw, gerr := c.get(ctx, apiPath+"/projects/"+url.PathEscape(project)+"/merge_requests/"+strconv.Itoa(iid), what)
	if gerr != nil {
		return domain.MergeRequest{}, gerr
	}
	var w wireMR
	if err := json.Unmarshal(raw, &w); err != nil {
		return domain.MergeRequest{}, c.fail(what, apperr.GitLabInvalidResponse, http.StatusOK, "not a merge request: "+err.Error(), "", nil)
	}
	mr, detail := c.toMergeRequest(w, project)
	if detail == "" && mr.IID != iid {
		detail = fmt.Sprintf("iid %d, asked for %d", mr.IID, iid)
	}
	if detail != "" {
		return domain.MergeRequest{}, c.fail(what, apperr.GitLabInvalidResponse, http.StatusOK, detail, "", nil)
	}
	return mr, nil
}

// LatestMergeRequest reads the latest merge request of project whose source branch is branch, in
// any state; ok is false when there is none.
func (c *Client) LatestMergeRequest(ctx context.Context, project, branch string) (mr domain.MergeRequest, ok bool, err error) {
	what := fmt.Sprintf("the merge requests of %s from %s", project, branch)
	if err := c.checkProject(project, what); err != nil {
		return domain.MergeRequest{}, false, err
	}
	q := url.Values{"source_branch": {branch}, "order_by": {"created_at"}, "sort": {"desc"}, "per_page": {"1"}}
	raw, gerr := c.get(ctx, apiPath+"/projects/"+url.PathEscape(project)+"/merge_requests?"+q.Encode(), what)
	if gerr != nil {
		return domain.MergeRequest{}, false, gerr
	}
	var list []wireMR
	if err := json.Unmarshal(raw, &list); err != nil || list == nil || len(list) > 1 {
		return domain.MergeRequest{}, false, c.fail(what, apperr.GitLabInvalidResponse, http.StatusOK, "not a list of at most one merge request", "", nil)
	}
	if len(list) == 0 {
		return domain.MergeRequest{}, false, nil
	}
	mr, detail := c.toMergeRequest(list[0], project)
	if detail == "" && mr.SourceBranch != branch {
		detail = "source_branch " + strconv.Quote(mr.SourceBranch) + ", asked for " + strconv.Quote(branch)
	}
	if detail != "" {
		return domain.MergeRequest{}, false, c.fail(what, apperr.GitLabInvalidResponse, http.StatusOK, detail, "", nil)
	}
	return mr, true, nil
}

func (c *Client) checkProject(project, what string) error {
	if !Allowed(c.opts.AllowedGroups, project) {
		return &apperr.GitLabError{What: what, Kind: apperr.GitLabNotFound,
			Detail: c.opts.TokenEnv, Err: fmt.Errorf("%s is not under gitlab.allowed_groups; nothing was requested", project)}
	}
	return nil
}

// wireMR is the part of GitLab's merge request answer revise uses; the rest is ignored.
type wireMR struct {
	IID             *int    `json:"iid"`
	ProjectID       *int    `json:"project_id"`
	SourceProjectID *int    `json:"source_project_id"`
	State           string  `json:"state"`
	SourceBranch    string  `json:"source_branch"`
	TargetBranch    string  `json:"target_branch"`
	SHA             *string `json:"sha"`
	CreatedAt       string  `json:"created_at"`
	Title           string  `json:"title"`
	Description     *string `json:"description"`
	WebURL          string  `json:"web_url"`
}

// toMergeRequest validates every field revise relies on; detail says what is wrong, "" for none.
func (c *Client) toMergeRequest(w wireMR, project string) (domain.MergeRequest, string) {
	switch {
	case w.IID == nil || *w.IID <= 0:
		return domain.MergeRequest{}, "iid missing"
	case w.ProjectID == nil || w.SourceProjectID == nil:
		return domain.MergeRequest{}, "project_id or source_project_id missing"
	case !slices.Contains([]string{domain.MRStateOpened, domain.MRStateMerged, domain.MRStateClosed, domain.MRStateLocked}, w.State):
		return domain.MergeRequest{}, "state " + strconv.Quote(w.State)
	case !plainRef(w.SourceBranch) || !plainRef(w.TargetBranch):
		return domain.MergeRequest{}, "source_branch or target_branch is not a branch name"
	case w.SHA == nil || !shaPattern.MatchString(*w.SHA):
		return domain.MergeRequest{}, "sha is not a full commit id"
	case !plainText(w.Title, 1024):
		return domain.MergeRequest{}, "title missing or not one line"
	}
	created, err := time.Parse(time.RFC3339, w.CreatedAt)
	if err != nil {
		return domain.MergeRequest{}, "created_at " + strconv.Quote(w.CreatedAt)
	}
	want := MergeRequestURL(c.opts.BaseURL, project, *w.IID)
	if !strings.EqualFold(w.WebURL, want) {
		return domain.MergeRequest{}, fmt.Sprintf("web_url %q is not %s", w.WebURL, want)
	}
	desc := ""
	if w.Description != nil {
		desc = *w.Description
	}
	return domain.MergeRequest{
		Project: project, IID: *w.IID, URL: want, State: w.State,
		SourceBranch: w.SourceBranch, TargetBranch: w.TargetBranch, SHA: *w.SHA,
		CreatedAt: created.UTC(), Title: w.Title, Description: strings.ToValidUTF8(desc, ""),
		Fork: *w.ProjectID != *w.SourceProjectID,
	}, ""
}

// plainRef is a branch name git could have: no space or control character, no leading "-", at
// most 255 bytes. The worktree rules are applied by app.
func plainRef(s string) bool {
	return s != "" && len(s) <= 255 && !strings.HasPrefix(s, "-") &&
		!strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) })
}

func plainText(s string, max int) bool {
	return s != "" && len(s) <= max && !strings.ContainsAny(s, "\r\n\x00")
}

// get is the client's one way to send a request, and it is a GET (SPEC.md §3: only GET; a test
// checks the package builds no other method). Transient failures are retried with Retry-After
// or a doubling backoff.
func (c *Client) get(ctx context.Context, path, what string) ([]byte, *apperr.GitLabError) {
	for attempt := 1; ; attempt++ {
		raw, retryAfter, gerr := c.once(ctx, c.opts.BaseURL+path, what)
		if gerr == nil {
			return raw, nil
		}
		gerr.Attempts = attempt
		if !gerr.Kind.Transient() || attempt > c.opts.MaxRetries || ctx.Err() != nil {
			return nil, gerr
		}
		wait := min(baseBackoff<<(attempt-1), c.opts.Timeout)
		if retryAfter > 0 {
			if retryAfter > c.opts.Timeout {
				return nil, gerr
			}
			wait = retryAfter
		}
		if err := c.sleep(ctx, wait); err != nil {
			return nil, c.fail(what, apperr.GitLabCancelled, 0, "", "", ctx.Err())
		}
	}
}

func (c *Client) once(ctx context.Context, target, what string) ([]byte, time.Duration, *apperr.GitLabError) {
	actx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, c.fail(what, apperr.GitLabInvalidResponse, 0, "", "", fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.opts.UserAgent)
	req.Header.Set("PRIVATE-TOKEN", c.token.value())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, c.transportError(ctx, actx, what, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return nil, 0, c.transportError(ctx, actx, what, fmt.Errorf("read response: %w", err))
	}
	if len(raw) > maxResponseBody {
		return nil, 0, c.fail(what, apperr.GitLabInvalidResponse, resp.StatusCode, "the response body is larger than 1 MiB", "", nil)
	}
	status := resp.StatusCode
	if status >= 300 && status < 400 {
		host := ""
		if loc, err := resp.Location(); err == nil {
			host = loc.Host
		}
		return nil, 0, c.fail(what, apperr.GitLabRedirect, status, host, "", nil)
	}
	if status == http.StatusOK {
		return raw, 0, nil
	}
	kind, detail := apperr.GitLabInvalidResponse, "unexpected status "+strconv.Itoa(status)
	switch status {
	case http.StatusUnauthorized:
		kind, detail = apperr.GitLabAuth, c.opts.TokenEnv
	case http.StatusForbidden, http.StatusNotFound:
		kind, detail = apperr.GitLabNotFound, c.opts.TokenEnv
	case http.StatusRequestTimeout:
		kind = apperr.GitLabTimeout
	case http.StatusTooManyRequests:
		kind = apperr.GitLabRateLimited
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		kind = apperr.GitLabUnavailable
	}
	return nil, parseRetryAfter(resp.Header.Get("Retry-After")), c.fail(what, kind, status, detail, string(raw), nil)
}

func (c *Client) transportError(ctx, actx context.Context, what string, err error) *apperr.GitLabError {
	if ctx.Err() != nil {
		return c.fail(what, apperr.GitLabCancelled, 0, "", "", ctx.Err())
	}
	var netErr net.Error
	if errors.Is(actx.Err(), context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return c.fail(what, apperr.GitLabTimeout, 0, "", "", nil)
	}
	return c.fail(what, apperr.GitLabConnection, 0, "", "", err)
}

// fail builds the error with the token scrubbed from every text, before any cut.
func (c *Client) fail(what string, kind apperr.GitLabFailureKind, status int, detail, body string, err error) *apperr.GitLabError {
	if err != nil {
		if msg := err.Error(); c.redact(msg) != msg {
			err = errors.New(c.redact(msg))
		}
	}
	body = c.redact(body)
	if len(body) > maxErrorBody {
		body = strings.ToValidUTF8(body[:maxErrorBody], "") + "... [truncated]"
	}
	return &apperr.GitLabError{What: what, Kind: kind, StatusCode: status, Detail: c.redact(detail), Body: body, Err: err}
}

func (c *Client) redact(s string) string {
	if t := c.token.value(); t != "" {
		s = strings.ReplaceAll(s, t, redacted)
	}
	return s
}

func parseRetryAfter(v string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
