package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// Authentication schemes, as in jira.auth.
const (
	AuthBasic  = "basic"
	AuthBearer = "bearer"
)

const (
	issuePath = "/rest/api/2/issue/"

	// maxResponseBody bounds how much of a response is read at all. An issue with a long
	// description is tens of KB; anything over this is not an answer to use.
	maxResponseBody = 1 << 20
	// maxErrorBody bounds the response body excerpt carried by a JiraError.
	maxErrorBody = 512
	// baseBackoff is the wait before the first retry when Jira sent no Retry-After; it
	// doubles for each further retry, capped at one attempt's timeout.
	baseBackoff = 250 * time.Millisecond

	defaultUserAgent = "orchestrator"
	redacted         = "[REDACTED]"
)

// systemFields are always requested; configured custom fields follow, in config order.
var systemFields = []string{"summary", "description", "issuetype", "parent", "components"}

// Credentials authenticate the request. It prints as [REDACTED], and the values sit
// behind a pointer: fmt cannot call String on an unexported field (Client.creds), and a
// raw print of one then shows only an address, never a secret.
type Credentials struct {
	s *secretValues
}

type secretValues struct {
	auth, email, token string
}

// NewCredentials builds credentials for auth (AuthBasic: email + API token; AuthBearer:
// a personal access token, email unused). The caller (app) reads the values from the
// environment variables config names.
func NewCredentials(auth, email, token string) Credentials {
	return Credentials{s: &secretValues{auth: auth, email: email, token: token}}
}

func (Credentials) String() string   { return redacted }
func (Credentials) GoString() string { return redacted }

// values returns the secrets, zero for a zero Credentials.
func (c Credentials) values() secretValues {
	if c.s == nil {
		return secretValues{}
	}
	return *c.s
}

// header is the Authorization header value.
func (c Credentials) header() string {
	v := c.values()
	if v.auth == AuthBearer {
		return "Bearer " + v.token
	}
	return "Basic " + v.basic()
}

func (v secretValues) basic() string {
	return base64.StdEncoding.EncodeToString([]byte(v.email + ":" + v.token))
}

// secrets lists every form a credential could take in a response or an error text.
func (c Credentials) secrets() []string {
	v := c.values()
	var out []string
	if v.auth != AuthBearer && (v.email != "" || v.token != "") {
		out = append(out, v.basic())
	}
	for _, s := range []string{v.token, v.email} {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Field is a configured text custom field (config.JiraField).
type Field struct {
	ID    string
	Label string
}

// Options configures the client. It mirrors config.JiraConfig's transport fields; the
// credentials are passed separately so they never sit in a value that could be logged.
type Options struct {
	BaseURL string
	Auth    string
	// EmailEnv and TokenEnv name the credential variables. They are used only in error
	// messages ("Jira rejected the credentials in JIRA_EMAIL/JIRA_API_TOKEN").
	EmailEnv, TokenEnv string
	// Timeout bounds one HTTP attempt. Retries get a fresh Timeout each, and a
	// Retry-After longer than Timeout is not waited for.
	Timeout time.Duration
	// MaxRetries is how many times a transient failure is retried (0 = no retry).
	MaxRetries int
	Fields     []Field
	UserAgent  string
}

// Client fetches issues from one Jira instance and creates the child issues of a story.
type Client struct {
	opts  Options
	creds Credentials
	http  *http.Client
	// sleep waits between retries and now stamps FetchedAt; tests replace both.
	sleep func(ctx context.Context, d time.Duration) error
	now   func() time.Time
}

// NewHTTPClient builds the client. It follows no redirect: a 3xx from the REST API is a
// proxy or a login page, and following one could send the credentials somewhere else
// (SPEC.md F3).
func NewHTTPClient(opts Options, creds Credentials) *Client {
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")
	if opts.UserAgent == "" {
		opts.UserAgent = defaultUserAgent
	}
	return &Client{
		opts:  opts,
		creds: creds,
		http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		sleep: sleepCtx,
		now:   time.Now,
	}
}

// Fetch makes the one GET for key (already validated by ParseRef) and returns the
// validated issue, or a *apperr.JiraError. Transient failures are retried up to
// MaxRetries times; the wait is Jira's Retry-After when it sent one, else an exponential
// backoff capped at one attempt's timeout. A Retry-After longer than that timeout is not
// waited for. Cancellation and an expired parent deadline are never retried.
func (c *Client) Fetch(ctx context.Context, key string) (domain.Ticket, error) {
	key = strings.ToUpper(key)
	for attempt := 1; ; attempt++ {
		ticket, retryAfter, jerr := c.attempt(ctx, key)
		if jerr == nil {
			return ticket, nil
		}
		jerr.Attempts = attempt
		if !jerr.Kind.Transient() || attempt > c.opts.MaxRetries || ctx.Err() != nil {
			return domain.Ticket{}, jerr
		}
		wait := min(baseBackoff<<(attempt-1), c.opts.Timeout)
		if retryAfter > 0 {
			if retryAfter > c.opts.Timeout {
				return domain.Ticket{}, jerr
			}
			wait = retryAfter
		}
		if err := c.sleep(ctx, wait); err != nil {
			return domain.Ticket{}, c.contextError(ctx, key, attempt)
		}
	}
}

// requestURL is built only from BaseURL and the key, never from anything the operator
// pasted (SPEC.md §6.1).
func (c *Client) requestURL(key string) string {
	fields := append([]string(nil), systemFields...)
	for _, f := range c.opts.Fields {
		fields = append(fields, f.ID)
	}
	return c.opts.BaseURL + issuePath + url.PathEscape(key) + "?fields=" + strings.Join(fields, ",")
}

// response is what one round trip answered with, other than a redirect. retryAfter is the
// parsed Retry-After header, or zero.
type response struct {
	status     int
	raw        []byte
	retryAfter time.Duration
}

// do performs one HTTP round trip: method on target with body (nil for none), under one
// attempt's timeout. Every failure that is not an answer to act on - a request that cannot be
// built, a transport error, a body over the limit, a 3xx - is a *JiraError for key.
func (c *Client) do(ctx context.Context, method, target string, body []byte, key string) (response, *apperr.JiraError) {
	actx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(actx, method, target, reader)
	if err != nil {
		jerr := c.fail(key, apperr.JiraInvalidResponse, 0, "", "", fmt.Errorf("build request: %w", err))
		jerr.NotSent = true
		return response{}, jerr
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.opts.UserAgent)
	req.Header.Set("Authorization", c.creds.header())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return response{}, c.transportError(ctx, actx, key, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return response{}, c.transportError(ctx, actx, key, fmt.Errorf("read response: %w", err))
	}
	if len(raw) > maxResponseBody {
		return response{}, c.fail(key, apperr.JiraInvalidResponse, resp.StatusCode,
			"the response body is larger than 1 MiB", "", nil)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return response{}, c.fail(key, apperr.JiraRedirect, resp.StatusCode, redirectHost(resp), "", nil)
	}
	return response{status: resp.StatusCode, raw: raw, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}, nil
}

// attempt performs one GET of key. retryAfter is the parsed Retry-After header of a failed
// response, or zero.
func (c *Client) attempt(ctx context.Context, key string) (domain.Ticket, time.Duration, *apperr.JiraError) {
	res, jerr := c.do(ctx, http.MethodGet, c.requestURL(key), nil, key)
	if jerr != nil {
		return domain.Ticket{}, 0, jerr
	}
	if res.status == http.StatusOK {
		ticket, detail := toTicket(res.raw, key, c.opts, c.now())
		if detail != "" {
			return domain.Ticket{}, 0, c.fail(key, apperr.JiraInvalidResponse, res.status, detail, "", nil)
		}
		return ticket, 0, nil
	}
	return domain.Ticket{}, res.retryAfter, c.statusError(key, res)
}

// statusError is the JiraError of a response that is not the success the caller expects.
func (c *Client) statusError(key string, res response) *apperr.JiraError {
	status := res.status
	kind, detail := apperr.JiraInvalidResponse, ""
	switch status {
	case http.StatusUnauthorized:
		kind, detail = apperr.JiraAuth, c.credentialNames()
	case http.StatusForbidden, http.StatusNotFound:
		kind, detail = apperr.JiraNotFound, c.accountName()
	case http.StatusRequestTimeout:
		kind = apperr.JiraTimeout
	case http.StatusTooManyRequests:
		kind = apperr.JiraRateLimited
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		kind = apperr.JiraUnavailable
	default:
		detail = "unexpected status " + strconv.Itoa(status)
	}
	return c.fail(key, kind, status, detail, string(res.raw), nil)
}

// redirectHost is the host a 3xx points at, or "" when it has no usable Location. Only
// the host is shown: the rest of the URL can carry tokens (a login page's state).
func redirectHost(resp *http.Response) string {
	loc, err := resp.Location()
	if err != nil {
		return ""
	}
	return loc.Host
}

// credentialNames names the variables the credentials came from, for an auth failure.
func (c *Client) credentialNames() string {
	var names []string
	if c.opts.Auth != AuthBearer && c.opts.EmailEnv != "" {
		names = append(names, c.opts.EmailEnv)
	}
	if c.opts.TokenEnv != "" {
		names = append(names, c.opts.TokenEnv)
	}
	return strings.Join(names, "/")
}

// accountName names the variable that identifies the account, for a 403/404.
func (c *Client) accountName() string {
	if c.opts.Auth != AuthBearer && c.opts.EmailEnv != "" {
		return c.opts.EmailEnv
	}
	return c.opts.TokenEnv
}

// transportError classifies a failure that produced no HTTP response. ctx is the
// caller's context, actx the per-attempt one derived from it.
func (c *Client) transportError(ctx, actx context.Context, key string, err error) *apperr.JiraError {
	if ctx.Err() != nil {
		return c.contextError(ctx, key, 0)
	}
	var netErr net.Error
	if errors.Is(actx.Err(), context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return c.fail(key, apperr.JiraTimeout, 0, c.opts.Timeout.String(), "", nil)
	}
	return c.fail(key, apperr.JiraConnection, 0, "", "", err)
}

// contextError reports the caller's own context ending: Ctrl-C is a cancellation, an
// expired run deadline a (non-retried) timeout.
func (c *Client) contextError(ctx context.Context, key string, attempts int) *apperr.JiraError {
	var jerr *apperr.JiraError
	if errors.Is(ctx.Err(), context.Canceled) {
		jerr = c.fail(key, apperr.JiraCancelled, 0, "", "", ctx.Err())
	} else {
		jerr = c.fail(key, apperr.JiraTimeout, 0, "the run's execution.timeout", "", nil)
	}
	jerr.Attempts = attempts
	return jerr
}

// fail builds a JiraError with every credential form scrubbed from the detail, the body
// excerpt and the cause. None of them should ever contain one, but a proxy or server
// echoing request headers back must not be able to leak it into the terminal or logs.
// The body is redacted before it is truncated, so a cut can never leave half a secret.
func (c *Client) fail(key string, kind apperr.JiraFailureKind, status int, detail, body string, err error) *apperr.JiraError {
	if err != nil {
		if msg := err.Error(); c.redact(msg) != msg {
			err = errors.New(c.redact(msg))
		}
	}
	return &apperr.JiraError{
		Key:        key,
		Kind:       kind,
		StatusCode: status,
		Detail:     c.redact(detail),
		Body:       truncate(c.redact(body), maxErrorBody),
		Err:        err,
	}
}

func (c *Client) redact(s string) string {
	for _, secret := range c.creds.secrets() {
		s = strings.ReplaceAll(s, secret, redacted)
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + fmt.Sprintf("... [truncated, %d of %d bytes shown]", n, len(s))
}

// parseRetryAfter accepts both forms RFC 9110 allows: delay-seconds and an HTTP-date.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
