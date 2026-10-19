package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"orchestrator/internal/apperr"
)

// bodyLog keeps the request bodies a handler read.
type bodyLog struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (l *bodyLog) add(b []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bodies = append(l.bodies, b)
}

func (l *bodyLog) all() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([][]byte(nil), l.bodies...)
}

func testIssue() Issue {
	return Issue{
		Project: "PAY", IssueType: "Story", Summary: "RF02 · Expose partial refunds",
		Description: "h2. Goal\nExpose them.", Parent: "PAY-1",
		Labels: []string{"orchestrator"}, Components: []string{"Billing"},
	}
}

// created answers 201 with key and the extra fields Jira adds, and keeps the request body.
func created(log *bodyLog, key string) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, r *http.Request, _ int) {
		b, _ := io.ReadAll(r.Body)
		log.add(b)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":"10002","key":%q,"self":"https://example.atlassian.net/rest/api/2/issue/10002"}`, key)
	}
}

func decodeFields(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var doc map[string]map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, body)
	}
	if len(doc) != 1 || doc["fields"] == nil {
		t.Fatalf("request body = %s, want only fields", body)
	}
	return doc["fields"]
}

func TestCreate_RequestWithParentLink(t *testing.T) {
	var log bodyLog
	srv := newServer(t, created(&log, "PAY-124"))
	c, _ := testClient(testOptions(srv.URL+"/jira/"), basicCreds())

	key, err := c.Create(context.Background(), testIssue())
	if err != nil {
		t.Fatal(err)
	}
	if key != "PAY-124" {
		t.Errorf("key = %q, want PAY-124", key)
	}

	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Method != http.MethodPost || req.URL.Path != "/jira/rest/api/2/issue" || req.URL.RawQuery != "" {
		t.Errorf("request = %s %s?%s, want POST /jira/rest/api/2/issue", req.Method, req.URL.Path, req.URL.RawQuery)
	}
	for header, want := range map[string]string{
		"Authorization": "Basic " + testBasic, "Content-Type": "application/json",
		"Accept": "application/json", "User-Agent": "orchestrator/test",
	} {
		if got := req.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	fields := decodeFields(t, log.all()[0])
	want := map[string]any{
		"project":     map[string]any{"key": "PAY"},
		"issuetype":   map[string]any{"name": "Story"},
		"summary":     "RF02 · Expose partial refunds",
		"description": "h2. Goal\nExpose them.",
		"parent":      map[string]any{"key": "PAY-1"},
		"labels":      []any{"orchestrator"},
		"components":  []any{map[string]any{"name": "Billing"}},
	}
	if len(fields) != len(want) {
		t.Errorf("fields = %v, want exactly %v", fields, want)
	}
	for name, w := range want {
		wantJSON, _ := json.Marshal(w)
		gotJSON, _ := json.Marshal(fields[name])
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("fields.%s = %s, want %s", name, gotJSON, wantJSON)
		}
	}
}

func TestCreate_RequestWithFieldLink(t *testing.T) {
	var log bodyLog
	srv := newServer(t, created(&log, "PAY-124"))
	c, _ := testClient(testOptions(srv.URL), basicCreds())

	issue := testIssue()
	issue.LinkField = "customfield_10014"
	if _, err := c.Create(context.Background(), issue); err != nil {
		t.Fatal(err)
	}
	fields := decodeFields(t, log.all()[0])
	if fields["customfield_10014"] != "PAY-1" {
		t.Errorf("fields.customfield_10014 = %v, want PAY-1", fields["customfield_10014"])
	}
	if _, ok := fields["parent"]; ok {
		t.Errorf("fields.parent is set with link: field: %v", fields["parent"])
	}
}

func TestCreate_OmitsEmptyLabelsAndComponents(t *testing.T) {
	var log bodyLog
	srv := newServer(t, created(&log, "PAY-124"))
	c, _ := testClient(testOptions(srv.URL), basicCreds())

	issue := testIssue()
	issue.Labels, issue.Components = nil, nil
	if _, err := c.Create(context.Background(), issue); err != nil {
		t.Fatal(err)
	}
	fields := decodeFields(t, log.all()[0])
	for _, name := range []string{"labels", "components"} {
		if _, ok := fields[name]; ok {
			t.Errorf("fields.%s is sent empty", name)
		}
	}
}

func TestCreate_BearerAuth(t *testing.T) {
	var log bodyLog
	srv := newServer(t, created(&log, "PAY-124"))
	opts := testOptions(srv.URL)
	opts.Auth = AuthBearer
	c, _ := testClient(opts, NewCredentials(AuthBearer, "", testToken))
	if _, err := c.Create(context.Background(), testIssue()); err != nil {
		t.Fatal(err)
	}
	if got := srv.requests()[0].Header.Get("Authorization"); got != "Bearer "+testToken {
		t.Errorf("Authorization = %q, want a bearer token", got)
	}
}

func TestCreate_Rejected(t *testing.T) {
	body := `{"errorMessages":["Issue type is not valid"],"errors":{"parent":"Given parent work item does not exist",` +
		`"components":"Component name 'Billing' is not valid ` + testToken + `","summary":"echo ` + testBasic + ` ` + testEmail + `"}}`
	srv := newServer(t, answer(http.StatusBadRequest, body))
	c, sleeps := testClient(testOptions(srv.URL), basicCreds())

	_, err := c.Create(context.Background(), testIssue())
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraRejected || jerr.StatusCode != 400 || jerr.Op != apperr.JiraOpCreate {
		t.Errorf("error = %+v, want a rejected creation (HTTP 400)", jerr)
	}
	msg := err.Error()
	for _, want := range []string{"Issue type is not valid", "components: Component name 'Billing' is not valid", "parent: Given parent work item does not exist", "[REDACTED]"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
	if len(srv.requests()) != 1 || len(*sleeps) != 0 {
		t.Errorf("%d requests, %d sleeps; a 400 is final", len(srv.requests()), len(*sleeps))
	}
	if MayExist(err) {
		t.Error("MayExist = true after a 400")
	}
}

func TestCreate_RejectedWithoutJSON(t *testing.T) {
	srv := newServer(t, answer(http.StatusBadRequest, "<html>bad "+testToken+"</html>"))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	_, err := c.Create(context.Background(), testIssue())
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraRejected || !strings.Contains(jerr.Body, "bad [REDACTED]") {
		t.Errorf("error = %+v, want a rejected creation with the redacted body", jerr)
	}
}

func TestCreate_Refusals(t *testing.T) {
	cases := []struct {
		status int
		kind   apperr.JiraFailureKind
	}{
		{http.StatusUnauthorized, apperr.JiraAuth},
		{http.StatusForbidden, apperr.JiraNotFound},
		{http.StatusNotFound, apperr.JiraNotFound},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			srv := newServer(t, answer(tc.status, ""))
			c, _ := testClient(testOptions(srv.URL), basicCreds())
			_, err := c.Create(context.Background(), testIssue())
			jerr := asJiraError(t, err)
			if jerr.Kind != tc.kind || len(srv.requests()) != 1 {
				t.Errorf("kind = %s after %d requests, want %s after 1", jerr.Kind, len(srv.requests()), tc.kind)
			}
			if MayExist(err) {
				t.Errorf("MayExist = true after HTTP %d", tc.status)
			}
		})
	}
}

func TestCreate_RetriesRateLimitWithRetryAfter(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var log bodyLog
			ok := created(&log, "PAY-124")
			srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
				if n == 1 {
					io.Copy(io.Discard, r.Body)
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(status)
					return
				}
				ok(w, r, n)
			})
			c, sleeps := testClient(testOptions(srv.URL), basicCreds())

			key, err := c.Create(context.Background(), testIssue())
			if err != nil || key != "PAY-124" {
				t.Fatalf("Create = %q, %v", key, err)
			}
			if len(srv.requests()) != 2 {
				t.Errorf("%d requests, want exactly 2", len(srv.requests()))
			}
			if len(*sleeps) != 1 || (*sleeps)[0] != time.Second {
				t.Errorf("sleeps = %v, want [1s]", *sleeps)
			}
			bodies := log.all()
			if len(bodies) != 1 || len(bodies[0]) == 0 {
				t.Errorf("the retried request has no body: %q", bodies)
			}
		})
	}
}

func TestCreate_DoesNotRetryWithoutUsableRetryAfter(t *testing.T) {
	cases := []struct {
		name   string
		status int
		after  string
	}{
		{"429 without Retry-After", http.StatusTooManyRequests, ""},
		{"503 with Retry-After over the timeout", http.StatusServiceUnavailable, "3"},
		{"429 with Retry-After 0", http.StatusTooManyRequests, "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
				if tc.after != "" {
					w.Header().Set("Retry-After", tc.after)
				}
				w.WriteHeader(tc.status)
			})
			c, sleeps := testClient(testOptions(srv.URL), basicCreds())
			_, err := c.Create(context.Background(), testIssue())
			asJiraError(t, err)
			if len(srv.requests()) != 1 || len(*sleeps) != 0 {
				t.Errorf("%d requests, %d sleeps, want 1 and 0", len(srv.requests()), len(*sleeps))
			}
			if MayExist(err) {
				t.Error("MayExist = true after a refusal")
			}
		})
	}
}

func TestCreate_StopsRetryingAtMaxRetries(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c, _ := testClient(testOptions(srv.URL), basicCreds()) // MaxRetries 1
	_, err := c.Create(context.Background(), testIssue())
	jerr := asJiraError(t, err)
	if len(srv.requests()) != 2 || jerr.Attempts != 2 || jerr.Kind != apperr.JiraRateLimited {
		t.Errorf("%d requests, %d attempts, kind %s; want 2, 2, rate_limited", len(srv.requests()), jerr.Attempts, jerr.Kind)
	}
}

func TestCreate_NeverRetriesAfterTheRequestMayHaveBeenSent(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		handler func(http.ResponseWriter, *http.Request, int)
		kind    apperr.JiraFailureKind
	}{
		{"timeout", 50 * time.Millisecond, func(w http.ResponseWriter, _ *http.Request, _ int) {
			time.Sleep(400 * time.Millisecond)
		}, apperr.JiraTimeout},
		{"connection reset", 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
		}, apperr.JiraConnection},
		{"500", 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusInternalServerError)
		}, apperr.JiraUnavailable},
		{"502", 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(http.StatusBadGateway)
		}, apperr.JiraUnavailable},
		{"504", 0, func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(http.StatusGatewayTimeout)
		}, apperr.JiraUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, tc.handler)
			opts := testOptions(srv.URL)
			if tc.timeout > 0 {
				opts.Timeout = tc.timeout
			}
			opts.MaxRetries = 3
			c, sleeps := testClient(opts, basicCreds())

			_, err := c.Create(context.Background(), testIssue())
			jerr := asJiraError(t, err)
			if jerr.Kind != tc.kind {
				t.Errorf("kind = %s, want %s", jerr.Kind, tc.kind)
			}
			if len(srv.requests()) != 1 || len(*sleeps) != 0 || jerr.Attempts != 1 {
				t.Errorf("%d requests, %d sleeps, %d attempts; want exactly one request", len(srv.requests()), len(*sleeps), jerr.Attempts)
			}
			if !MayExist(err) {
				t.Error("MayExist = false: the issue may have been created")
			}
		})
	}
}

func TestCreate_DoesNotFollowRedirects(t *testing.T) {
	target := newServer(t, answer(http.StatusCreated, `{"key":"PAY-9"}`))
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Location", target.URL+"/login?state=secret-state")
		w.WriteHeader(http.StatusFound)
	})
	c, _ := testClient(testOptions(srv.URL), basicCreds())

	_, err := c.Create(context.Background(), testIssue())
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraRedirect || jerr.StatusCode != http.StatusFound {
		t.Errorf("error = %+v, want a redirect", jerr)
	}
	if n := len(target.requests()); n != 0 {
		t.Errorf("the redirect target received %d requests", n)
	}
	if len(srv.requests()) != 1 {
		t.Errorf("%d requests to Jira, want 1", len(srv.requests()))
	}
	if strings.Contains(err.Error(), "secret-state") {
		t.Errorf("the redirect's query is shown: %v", err)
	}
	if MayExist(err) {
		t.Error("MayExist = true after a redirect")
	}
}

func TestCreate_RejectsAnInvalidCreatedAnswer(t *testing.T) {
	cases := map[string]string{
		"no key":             `{"id":"1"}`,
		"empty key":          `{"key":""}`,
		"other project":      `{"key":"OTHER-5"}`,
		"longer project":     `{"key":"PAYMENT-5"}`,
		"lower case":         `{"key":"pay-5"}`,
		"no number":          `{"key":"PAY-"}`,
		"leading zero":       `{"key":"PAY-05"}`,
		"not a string":       `{"key":5}`,
		"not json":           `created`,
		"a list":             `[{"key":"PAY-5"}]`,
		"suffix after key":   `{"key":"PAY-5 DROP"}`,
		"key from the title": `{"fields":{"key":"PAY-5"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newServer(t, answer(http.StatusCreated, body))
			c, sleeps := testClient(testOptions(srv.URL), basicCreds())
			key, err := c.Create(context.Background(), testIssue())
			jerr := asJiraError(t, err)
			if key != "" || jerr.Kind != apperr.JiraInvalidResponse || jerr.StatusCode != http.StatusCreated {
				t.Errorf("Create = %q, %+v, want an invalid response", key, jerr)
			}
			if len(srv.requests()) != 1 || len(*sleeps) != 0 {
				t.Errorf("%d requests: an invalid answer is never retried", len(srv.requests()))
			}
			if !MayExist(err) {
				t.Error("MayExist = false: Jira answered 201")
			}
		})
	}
}

func TestCreate_RejectsAnOversizeAnswer(t *testing.T) {
	srv := newServer(t, answer(http.StatusCreated, `{"key":"PAY-5","pad":"`+strings.Repeat("x", maxResponseBody)+`"}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	_, err := c.Create(context.Background(), testIssue())
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraInvalidResponse || !MayExist(err) {
		t.Errorf("error = %+v, MayExist = %v; want an invalid response that may exist", jerr, MayExist(err))
	}
}

func TestCreate_CancelledBeforeTheRequest(t *testing.T) {
	srv := newServer(t, created(&bodyLog{}, "PAY-124"))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Create(ctx, testIssue())
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraCancelled || jerr.Op != apperr.JiraOpCreate {
		t.Errorf("error = %+v, want a cancelled creation", jerr)
	}
	if len(srv.requests()) != 0 {
		t.Errorf("%d requests after the context was cancelled", len(srv.requests()))
	}
	if MayExist(err) {
		t.Error("MayExist = true for a request that was never sent")
	}
}

func TestMayExist(t *testing.T) {
	if !MayExist(errors.New("something else")) {
		t.Error("an error that is not a JiraError must keep the intent")
	}
	if !MayExist(fmt.Errorf("wrapped: %w", &apperr.JiraError{Kind: apperr.JiraTimeout})) {
		t.Error("a wrapped timeout must keep the intent")
	}
	if MayExist(&apperr.JiraError{Kind: apperr.JiraConnection, NotSent: true}) {
		t.Error("a request that was never sent cannot have created anything")
	}
}
