package jira

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

const (
	testEmail = "operator@example.com"
	testToken = "jira-token-0123456789abcdef"
)

var (
	testBasic   = base64.StdEncoding.EncodeToString([]byte(testEmail + ":" + testToken))
	testFetched = time.Date(2026, 9, 30, 10, 12, 0, 0, time.UTC)
)

// fullIssue is a 200 body with every field this package reads, plus the extras Jira
// always adds.
const fullIssue = `{
  "expand": "renderedFields",
  "id": "10001",
  "self": "https://example.atlassian.net/rest/api/2/issue/10001",
  "key": "PMC-2789",
  "fields": {
    "summary": "FE: disable reason not shown on hover",
    "description": "Precondition:\r\nlocking {{Tags}}\r\n\r\n*Impact*: none",
    "issuetype": {"id": "1", "name": "Bug"},
    "parent": {"id": "9", "key": "PMC-2663", "fields": {"summary": "Tags & Labels", "status": {}}},
    "customfield_10035": "Reason is shown",
    "customfield_10036": null
  }
}`

// server records every request and answers with handler.
type server struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, n int)) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, r.Clone(context.Background()))
		n := len(s.reqs)
		s.mu.Unlock()
		handler(w, r, n)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) requests() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request(nil), s.reqs...)
}

// answer writes status and body; an error body echoes the Authorization header back, so
// every error test also proves redaction.
func answer(status int, body string) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, r *http.Request, _ int) {
		w.WriteHeader(status)
		if status != http.StatusOK && body == "" {
			body = `{"errorMessages":["denied"],"echo":"` + r.Header.Get("Authorization") + `"}`
		}
		fmt.Fprint(w, body)
	}
}

func testOptions(baseURL string) Options {
	return Options{
		BaseURL: baseURL, Auth: AuthBasic, EmailEnv: "JIRA_EMAIL", TokenEnv: "JIRA_API_TOKEN",
		Timeout: 2 * time.Second, MaxRetries: 1, UserAgent: "orchestrator/test",
		Fields: []Field{
			{ID: "customfield_10035", Label: "Acceptance criteria"},
			{ID: "customfield_10036", Label: "Notes"},
		},
	}
}

// testClient points a client at baseURL with instant retries; sleeps records every wait.
func testClient(opts Options, creds Credentials) (*Client, *[]time.Duration) {
	c := NewHTTPClient(opts, creds)
	var sleeps []time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return ctx.Err()
	}
	c.now = func() time.Time { return testFetched }
	return c, &sleeps
}

func basicCreds() Credentials { return NewCredentials(AuthBasic, testEmail, testToken) }

func asJiraError(t *testing.T, err error) *apperr.JiraError {
	t.Helper()
	var jerr *apperr.JiraError
	if !errors.As(err, &jerr) {
		t.Fatalf("expected *apperr.JiraError, got %T: %v", err, err)
	}
	assertNoSecret(t, err.Error())
	return jerr
}

func assertNoSecret(t *testing.T, s string) {
	t.Helper()
	for _, secret := range []string{testToken, testEmail, testBasic} {
		if strings.Contains(s, secret) {
			t.Errorf("text contains the secret %q:\n%s", secret, s)
		}
	}
}

func TestFetch_Request(t *testing.T) {
	cases := []struct {
		name  string
		creds Credentials
		auth  string
		want  string
	}{
		{"basic", basicCreds(), AuthBasic, "Basic " + testBasic},
		{"bearer", NewCredentials(AuthBearer, "", testToken), AuthBearer, "Bearer " + testToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, answer(http.StatusOK, fullIssue))
			opts := testOptions(srv.URL + "/jira/")
			opts.Auth = tc.auth
			c, _ := testClient(opts, tc.creds)
			if _, err := c.Fetch(context.Background(), "pmc-2789"); err != nil {
				t.Fatal(err)
			}

			reqs := srv.requests()
			if len(reqs) != 1 {
				t.Fatalf("got %d requests, want 1", len(reqs))
			}
			r := reqs[0]
			if r.Method != http.MethodGet || r.URL.Path != "/jira/rest/api/2/issue/PMC-2789" {
				t.Errorf("got %s %s", r.Method, r.URL.Path)
			}
			if got, want := r.URL.RawQuery, "fields=summary,description,issuetype,parent,components,status,labels,reporter,customfield_10035,customfield_10036"; got != want {
				t.Errorf("query = %q, want %q", got, want)
			}
			if got := r.Header.Get("Authorization"); got != tc.want {
				t.Errorf("Authorization = %q, want %q", got, tc.want)
			}
			if got := r.Header.Get("Accept"); got != "application/json" {
				t.Errorf("Accept = %q", got)
			}
			if got := r.Header.Get("User-Agent"); got != "orchestrator/test" {
				t.Errorf("User-Agent = %q", got)
			}
		})
	}
}

func TestFetch_FullIssue(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, fullIssue))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	got, err := c.Fetch(context.Background(), "PMC-2789")
	if err != nil {
		t.Fatal(err)
	}
	want := domain.Ticket{
		Key: "PMC-2789", URL: srv.URL + "/browse/PMC-2789", Type: "Bug",
		Summary:     "FE: disable reason not shown on hover",
		Description: "Precondition:\nlocking {{Tags}}\n\n*Impact*: none",
		Parent:      &domain.TicketParent{Key: "PMC-2663", Summary: "Tags & Labels"},
		Fields:      []domain.TicketField{{ID: "customfield_10035", Label: "Acceptance criteria", Value: "Reason is shown"}},
		FetchedAt:   testFetched,
	}
	assertTicket(t, got, want)
}

func TestFetch_StatusLabelsReporter(t *testing.T) {
	body := `{"key":"PAY-1","fields":{"summary":"S","issuetype":{"name":"Task"},"status":{"name":"To Do","id":"1"},` +
		`"labels":["ai-run","service:billing"],"reporter":{"accountId":"acc-1","name":"jdoe"}}}`
	cases := []struct {
		name     string
		opts     func(string) Options
		creds    Credentials
		reporter string
	}{
		{"cloud", testOptions, basicCreds(), "acc-1"},
		{"data center", bearerOptions, NewCredentials(AuthBearer, "", testToken), "jdoe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, answer(http.StatusOK, body))
			c, _ := testClient(tc.opts(srv.URL), tc.creds)
			got, err := c.Fetch(context.Background(), "PAY-1")
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "To Do" || !reflect.DeepEqual(got.Labels, []string{"ai-run", "service:billing"}) || got.Reporter != tc.reporter {
				t.Errorf("status %q labels %q reporter %q", got.Status, got.Labels, got.Reporter)
			}
			// None of them is part of the task text.
			text, err := Render(got, "", 32768)
			if err != nil {
				t.Fatal(err)
			}
			for _, leaked := range []string{"To Do", "ai-run", tc.reporter} {
				if strings.Contains(text, leaked) {
					t.Errorf("the rendered ticket holds %q:\n%s", leaked, text)
				}
			}
		})
	}
}

func TestFetch_StatusLabelsReporterInvalid(t *testing.T) {
	cases := []struct{ name, fields, detail string }{
		{"status string", `"status":"To Do"`, "fields.status: not an object"},
		{"status no name", `"status":{}`, "fields.status.name"},
		{"labels string", `"labels":"ai-run"`, "fields.labels: not an array"},
		{"label object", `"labels":[{}]`, "fields.labels[0]: not a string"},
		{"reporter no accountId", `"reporter":{"name":"jdoe"}`, "fields.reporter.accountId: missing or empty"},
		{"reporter string", `"reporter":"x"`, "fields.reporter: not an object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, answer(http.StatusOK, `{"key":"PAY-1","fields":{"summary":"S","issuetype":{"name":"Task"},`+tc.fields+`}}`))
			c, _ := testClient(testOptions(srv.URL), basicCreds())
			_, err := c.Fetch(context.Background(), "PAY-1")
			jerr := asJiraError(t, err)
			if jerr.Kind != apperr.JiraInvalidResponse || !strings.Contains(jerr.Detail, tc.detail) {
				t.Errorf("got %s %q, want invalid_response containing %q", jerr.Kind, jerr.Detail, tc.detail)
			}
		})
	}
}

func TestFetch_NullReporterAndLabels(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, `{"key":"PAY-1","fields":{"summary":"S","issuetype":{"name":"Task"},"status":null,"labels":null,"reporter":null}}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	got, err := c.Fetch(context.Background(), "PAY-1")
	if err != nil || got.Status != "" || got.Labels != nil || got.Reporter != "" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func assertTicket(t *testing.T, got, want domain.Ticket) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ticket =\n%#v %#v %#v\nwant\n%#v %#v %#v", got, got.Parent, got.Fields, want, want.Parent, want.Fields)
	}
}

func TestFetch_ResponseShapes(t *testing.T) {
	minimal := func(fields string) string {
		return `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Task"}` + fields + `}}`
	}
	cases := []struct {
		name  string
		body  string
		check func(t *testing.T, tk domain.Ticket)
	}{
		{"null description", minimal(`,"description":null`), func(t *testing.T, tk domain.Ticket) {
			if tk.Description != "" {
				t.Errorf("Description = %q", tk.Description)
			}
		}},
		{"no parent", minimal(`,"description":"d"`), func(t *testing.T, tk domain.Ticket) {
			if tk.Parent != nil {
				t.Errorf("Parent = %+v", tk.Parent)
			}
		}},
		{"null parent", minimal(`,"parent":null`), func(t *testing.T, tk domain.Ticket) {
			if tk.Parent != nil {
				t.Errorf("Parent = %+v", tk.Parent)
			}
		}},
		{"custom field absent", minimal(``), func(t *testing.T, tk domain.Ticket) {
			if len(tk.Fields) != 0 {
				t.Errorf("Fields = %+v", tk.Fields)
			}
		}},
		{"custom field null and blank", minimal(`,"customfield_10035":null,"customfield_10036":"  \n"`), func(t *testing.T, tk domain.Ticket) {
			if len(tk.Fields) != 0 {
				t.Errorf("Fields = %+v", tk.Fields)
			}
		}},
		{"custom fields in config order", minimal(`,"customfield_10036":"b","customfield_10035":"a"`), func(t *testing.T, tk domain.Ticket) {
			if len(tk.Fields) != 2 || tk.Fields[0].ID != "customfield_10035" || tk.Fields[1].Label != "Notes" {
				t.Errorf("Fields = %+v", tk.Fields)
			}
		}},
		{"moved issue", `{"key":"NEW-9","fields":{"summary":"S","issuetype":{"name":"Task"}}}`, func(t *testing.T, tk domain.Ticket) {
			if tk.Key != "NEW-9" || tk.RequestedKey != "PMC-1" || !strings.HasSuffix(tk.URL, "/browse/NEW-9") {
				t.Errorf("Key %q RequestedKey %q URL %q", tk.Key, tk.RequestedKey, tk.URL)
			}
		}},
		{"same key", minimal(``), func(t *testing.T, tk domain.Ticket) {
			if tk.RequestedKey != "" {
				t.Errorf("RequestedKey = %q, want empty", tk.RequestedKey)
			}
		}},
		{"components absent", minimal(``), func(t *testing.T, tk domain.Ticket) {
			if tk.Components != nil {
				t.Errorf("Components = %q, want none", tk.Components)
			}
		}},
		{"components null", minimal(`,"components":null`), func(t *testing.T, tk domain.Ticket) {
			if tk.Components != nil {
				t.Errorf("Components = %q, want none", tk.Components)
			}
		}},
		{"components empty", minimal(`,"components":[]`), func(t *testing.T, tk domain.Ticket) {
			if tk.Components != nil {
				t.Errorf("Components = %q, want none", tk.Components)
			}
		}},
		{"components", minimal(`,"components":[{"id":"1","name":"gateway","self":"x"},{"name":"billing"},{"name":"gateway"}]`),
			func(t *testing.T, tk domain.Ticket) {
				if want := []string{"gateway", "billing"}; !reflect.DeepEqual(tk.Components, want) {
					t.Errorf("Components = %q, want %q", tk.Components, want)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, answer(http.StatusOK, tc.body))
			c, _ := testClient(testOptions(srv.URL), basicCreds())
			tk, err := c.Fetch(context.Background(), "PMC-1")
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, tk)
		})
	}
}

func TestFetch_InvalidResponse(t *testing.T) {
	cases := []struct {
		name, body, detail string
	}{
		{"not json", `<html>login</html>`, "the body is not an issue"},
		{"array", `[]`, "the body is not an issue"},
		{"no key", `{"fields":{}}`, "key: missing"},
		{"numeric key", `{"key":7,"fields":{}}`, "the body is not an issue"},
		{"bad key", `{"key":"pmc-1","fields":{}}`, `key "pmc-1" is not an issue key`},
		{"no fields", `{"key":"PMC-1"}`, "fields: missing"},
		{"no summary", `{"key":"PMC-1","fields":{"issuetype":{"name":"Bug"}}}`, "fields.summary: missing or empty"},
		{"blank summary", `{"key":"PMC-1","fields":{"summary":" ","issuetype":{"name":"Bug"}}}`, "fields.summary: missing or empty"},
		{"summary object", `{"key":"PMC-1","fields":{"summary":{},"issuetype":{"name":"Bug"}}}`, "fields.summary: not a string"},
		{"description adf", `{"key":"PMC-1","fields":{"summary":"S","description":{"type":"doc"},"issuetype":{"name":"Bug"}}}`,
			"fields.description: not a string or null"},
		{"no issuetype", `{"key":"PMC-1","fields":{"summary":"S"}}`, "fields.issuetype: missing"},
		{"issuetype string", `{"key":"PMC-1","fields":{"summary":"S","issuetype":"Bug"}}`, "fields.issuetype: not an object"},
		{"issuetype no name", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{}}}`, "fields.issuetype.name: missing or empty"},
		{"parent string", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Bug"},"parent":"PMC-2"}}`, "fields.parent: not an object"},
		{"parent bad key", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Bug"},"parent":{"key":"x","fields":{"summary":"P"}}}}`,
			"fields.parent.key"},
		{"parent no summary", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Bug"},"parent":{"key":"PMC-2","fields":{}}}}`,
			"fields.parent.fields.summary"},
		{"custom field object", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Bug"},"customfield_10035":{"value":"x"}}}`,
			"fields.customfield_10035: not a text field; remove it from jira.fields"},
		{"custom field number", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Bug"},"customfield_10036":3}}`,
			"fields.customfield_10036: not a text field"},
		{"custom field array", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Bug"},"customfield_10036":["a"]}}`,
			"fields.customfield_10036: not a text field"},
		{"components object", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Bug"},"components":{"name":"a"}}}`,
			"fields.components: not an array"},
		{"component string", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Bug"},"components":["gateway"]}}`,
			"fields.components[0]: not an object"},
		{"component no name", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Bug"},"components":[{"name":"a"},{"id":"2"}]}}`,
			"fields.components[1].name: missing or empty"},
		{"component empty name", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Bug"},"components":[{"name":" "}]}}`,
			"fields.components[0].name: missing or empty"},
		{"component name number", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Bug"},"components":[{"name":7}]}}`,
			"fields.components[0].name: missing or empty"},
		{"component name two lines", `{"key":"PMC-1","fields":{"summary":"S","issuetype":{"name":"Bug"},"components":[{"name":"a\nb"}]}}`,
			"fields.components[0].name: not one line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, answer(http.StatusOK, tc.body))
			c, _ := testClient(testOptions(srv.URL), basicCreds())
			_, err := c.Fetch(context.Background(), "PMC-1")
			jerr := asJiraError(t, err)
			if jerr.Kind != apperr.JiraInvalidResponse || !strings.Contains(jerr.Detail, tc.detail) {
				t.Errorf("got %s %q, want invalid_response containing %q", jerr.Kind, jerr.Detail, tc.detail)
			}
			if jerr.Attempts != 1 {
				t.Errorf("Attempts = %d, want 1 (not retried)", jerr.Attempts)
			}
		})
	}
}

func TestFetch_StatusKinds(t *testing.T) {
	cases := []struct {
		status   int
		kind     apperr.JiraFailureKind
		attempts int // with MaxRetries 1
		message  string
	}{
		{401, apperr.JiraAuth, 1, "Jira rejected the credentials in JIRA_EMAIL/JIRA_API_TOKEN"},
		{403, apperr.JiraNotFound, 1, "PMC-1 does not exist, or the account behind JIRA_EMAIL cannot see it"},
		{404, apperr.JiraNotFound, 1, "PMC-1 does not exist, or the account behind JIRA_EMAIL cannot see it"},
		{408, apperr.JiraTimeout, 2, "Jira did not answer in time (HTTP 408 after 2 attempts)"},
		{429, apperr.JiraRateLimited, 2, "Jira is rate limiting requests"},
		{500, apperr.JiraUnavailable, 2, "Jira is unavailable (HTTP 500 after 2 attempts)"},
		{502, apperr.JiraUnavailable, 2, "Jira is unavailable"},
		{503, apperr.JiraUnavailable, 2, "Jira is unavailable (HTTP 503 after 2 attempts)"},
		{504, apperr.JiraUnavailable, 2, "Jira is unavailable"},
		{400, apperr.JiraInvalidResponse, 1, "unexpected status 400"},
		{410, apperr.JiraInvalidResponse, 1, "unexpected status 410"},
		{501, apperr.JiraInvalidResponse, 1, "unexpected status 501"},
		{201, apperr.JiraInvalidResponse, 1, "unexpected status 201"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			srv := newServer(t, answer(tc.status, ""))
			c, _ := testClient(testOptions(srv.URL), basicCreds())
			_, err := c.Fetch(context.Background(), "PMC-1")
			jerr := asJiraError(t, err)
			if jerr.Kind != tc.kind || jerr.StatusCode != tc.status || jerr.Attempts != tc.attempts {
				t.Errorf("got %s HTTP %d after %d, want %s HTTP %d after %d",
					jerr.Kind, jerr.StatusCode, jerr.Attempts, tc.kind, tc.status, tc.attempts)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("message %q lacks %q", err, tc.message)
			}
			if len(srv.requests()) != tc.attempts {
				t.Errorf("server got %d requests, want %d", len(srv.requests()), tc.attempts)
			}
			// The server echoed the Authorization header; the body excerpt must not.
			if !strings.Contains(jerr.Body, redacted) {
				t.Errorf("Body = %q, want the echoed header redacted", jerr.Body)
			}
		})
	}
}

func TestFetch_BearerMessagesNameTheToken(t *testing.T) {
	srv := newServer(t, answer(http.StatusUnauthorized, ""))
	opts := testOptions(srv.URL)
	opts.Auth, opts.EmailEnv, opts.TokenEnv = AuthBearer, "", "JIRA_PAT"
	c, _ := testClient(opts, NewCredentials(AuthBearer, "", testToken))
	_, err := c.Fetch(context.Background(), "PMC-1")
	if jerr := asJiraError(t, err); jerr.Detail != "JIRA_PAT" {
		t.Errorf("Detail = %q, want JIRA_PAT", jerr.Detail)
	}
}

func TestFetch_RetryThenSuccess(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, fullIssue)
	})
	c, sleeps := testClient(testOptions(srv.URL), basicCreds())
	if _, err := c.Fetch(context.Background(), "PMC-2789"); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.requests()); n != 2 {
		t.Errorf("got %d requests, want 2", n)
	}
	if len(*sleeps) != 1 || (*sleeps)[0] != baseBackoff {
		t.Errorf("sleeps = %v, want [%s]", *sleeps, baseBackoff)
	}
}

func TestFetch_NoRetries(t *testing.T) {
	srv := newServer(t, answer(http.StatusServiceUnavailable, ""))
	opts := testOptions(srv.URL)
	opts.MaxRetries = 0
	c, sleeps := testClient(opts, basicCreds())
	_, err := c.Fetch(context.Background(), "PMC-1")
	if jerr := asJiraError(t, err); jerr.Kind != apperr.JiraUnavailable || jerr.Attempts != 1 {
		t.Errorf("got %s after %d attempts", jerr.Kind, jerr.Attempts)
	}
	if len(srv.requests()) != 1 || len(*sleeps) != 0 {
		t.Errorf("requests %d, sleeps %v", len(srv.requests()), *sleeps)
	}
}

func TestFetch_BackoffDoublesAndIsCapped(t *testing.T) {
	srv := newServer(t, answer(http.StatusBadGateway, ""))
	opts := testOptions(srv.URL)
	opts.MaxRetries, opts.Timeout = 3, 600*time.Millisecond
	c, sleeps := testClient(opts, basicCreds())
	_, err := c.Fetch(context.Background(), "PMC-1")
	if jerr := asJiraError(t, err); jerr.Attempts != 4 {
		t.Errorf("Attempts = %d, want 4", jerr.Attempts)
	}
	want := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 600 * time.Millisecond}
	if fmt.Sprint(*sleeps) != fmt.Sprint(want) {
		t.Errorf("sleeps = %v, want %v", *sleeps, want)
	}
}

func TestFetch_RetryAfter(t *testing.T) {
	date := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	cases := []struct {
		name, header string
		timeout      time.Duration
		requests     int
		check        func(t *testing.T, sleeps []time.Duration)
	}{
		{"seconds", "1", 2 * time.Second, 2, func(t *testing.T, s []time.Duration) {
			if len(s) != 1 || s[0] != time.Second {
				t.Errorf("sleeps = %v, want [1s]", s)
			}
		}},
		{"http date", date, 2 * time.Minute, 2, func(t *testing.T, s []time.Duration) {
			if len(s) != 1 || s[0] < 80*time.Second || s[0] > 90*time.Second {
				t.Errorf("sleeps = %v, want about 90s", s)
			}
		}},
		{"longer than timeout", "30", 2 * time.Second, 1, func(t *testing.T, s []time.Duration) {
			if len(s) != 0 {
				t.Errorf("sleeps = %v, want none", s)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
				w.Header().Set("Retry-After", tc.header)
				w.WriteHeader(http.StatusTooManyRequests)
			})
			opts := testOptions(srv.URL)
			opts.Timeout = tc.timeout
			c, sleeps := testClient(opts, basicCreds())
			_, err := c.Fetch(context.Background(), "PMC-1")
			if jerr := asJiraError(t, err); jerr.Kind != apperr.JiraRateLimited {
				t.Errorf("Kind = %s", jerr.Kind)
			}
			if n := len(srv.requests()); n != tc.requests {
				t.Errorf("got %d requests, want %d", n, tc.requests)
			}
			tc.check(t, *sleeps)
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"": 0, "0": 0, "-3": 0, "5": 5 * time.Second, "soon": 0,
		now.Add(10 * time.Second).Format(http.TimeFormat): 10 * time.Second,
		now.Add(-time.Minute).Format(http.TimeFormat):     0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestFetch_CancelledDuringBackoff(t *testing.T) {
	srv := newServer(t, answer(http.StatusServiceUnavailable, ""))
	opts := testOptions(srv.URL)
	opts.MaxRetries = 3
	c, _ := testClient(opts, basicCreds())
	ctx, cancel := context.WithCancel(context.Background())
	c.sleep = func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}
	_, err := c.Fetch(ctx, "PMC-1")
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraCancelled || jerr.Attempts != 1 {
		t.Errorf("got %s after %d attempts, want cancelled after 1", jerr.Kind, jerr.Attempts)
	}
	if n := len(srv.requests()); n != 1 {
		t.Errorf("got %d requests, want 1", n)
	}
}

func TestFetch_CancelledBeforeRequest(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, fullIssue))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Fetch(ctx, "PMC-1")
	if jerr := asJiraError(t, err); jerr.Kind != apperr.JiraCancelled {
		t.Errorf("Kind = %s, want cancelled", jerr.Kind)
	}
	if n := len(srv.requests()); n != 0 {
		t.Errorf("got %d requests, want 0", n)
	}
}

func TestFetch_AttemptTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	opts := testOptions(srv.URL)
	opts.Timeout, opts.MaxRetries = 50*time.Millisecond, 1
	c, _ := testClient(opts, basicCreds())
	_, err := c.Fetch(context.Background(), "PMC-1")
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraTimeout || jerr.Attempts != 2 {
		t.Errorf("got %s after %d attempts, want timeout after 2", jerr.Kind, jerr.Attempts)
	}
	if !strings.Contains(err.Error(), "Jira did not answer within 50ms") {
		t.Errorf("message = %q", err)
	}
}

func TestFetch_RunDeadlineNotRetried(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) { <-r.Context().Done() })
	opts := testOptions(srv.URL)
	opts.Timeout, opts.MaxRetries = 5*time.Second, 3
	c, _ := testClient(opts, basicCreds())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Fetch(ctx, "PMC-1")
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraTimeout || jerr.Attempts != 1 {
		t.Errorf("got %s after %d attempts, want timeout after 1", jerr.Kind, jerr.Attempts)
	}
	if !strings.Contains(err.Error(), "execution.timeout") {
		t.Errorf("message = %q", err)
	}
}

func TestFetch_ConnectionRefused(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, fullIssue))
	url := srv.URL
	srv.Close()
	c, sleeps := testClient(testOptions(url), basicCreds())
	_, err := c.Fetch(context.Background(), "PMC-1")
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraConnection || jerr.Attempts != 2 || len(*sleeps) != 1 {
		t.Errorf("got %s after %d attempts, sleeps %v", jerr.Kind, jerr.Attempts, *sleeps)
	}
}

// A redirect is never followed, to the same host or another: the target never sees a
// request, let alone the Authorization header.
func TestFetch_RedirectRefused(t *testing.T) {
	target := newServer(t, answer(http.StatusOK, fullIssue))
	targetHost := strings.TrimPrefix(target.URL, "http://")

	cases := []struct {
		name, location, host string
	}{
		{"other host", target.URL + "/login?token=abc", targetHost},
		{"same host", "/login", ""}, // resolved against the Jira server itself
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
				if strings.HasPrefix(r.URL.Path, "/login") {
					t.Error("the redirect was followed")
					return
				}
				http.Redirect(w, r, tc.location, http.StatusFound)
			})
			c, _ := testClient(testOptions(srv.URL), basicCreds())
			_, err := c.Fetch(context.Background(), "PMC-1")
			jerr := asJiraError(t, err)
			wantHost := tc.host
			if wantHost == "" {
				wantHost = strings.TrimPrefix(srv.URL, "http://")
			}
			if jerr.Kind != apperr.JiraRedirect || jerr.StatusCode != http.StatusFound || jerr.Detail != wantHost {
				t.Errorf("got %s HTTP %d detail %q, want redirect 302 %q", jerr.Kind, jerr.StatusCode, jerr.Detail, wantHost)
			}
			if strings.Contains(err.Error(), "token=abc") || strings.Contains(err.Error(), "/login") {
				t.Errorf("message shows more than the host: %q", err)
			}
			if n := len(srv.requests()); n != 1 {
				t.Errorf("Jira got %d requests, want 1", n)
			}
		})
	}
	if n := len(target.requests()); n != 0 {
		t.Errorf("the redirect target got %d requests, want 0", n)
	}
}

func TestFetch_BodyOverOneMiB(t *testing.T) {
	big := `{"key":"PMC-1","fields":{"summary":"` + strings.Repeat("x", maxResponseBody) + `"}}`
	srv := newServer(t, answer(http.StatusOK, big))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	_, err := c.Fetch(context.Background(), "PMC-1")
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraInvalidResponse || !strings.Contains(jerr.Detail, "larger than 1 MiB") {
		t.Errorf("got %s %q", jerr.Kind, jerr.Detail)
	}
}

func TestFetch_BodyExactlyOneMiB(t *testing.T) {
	prefix, suffix := `{"key":"PMC-1","fields":{"summary":"`, `","issuetype":{"name":"Bug"}}}`
	body := prefix + strings.Repeat("x", maxResponseBody-len(prefix)-len(suffix)) + suffix
	srv := newServer(t, answer(http.StatusOK, body))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	if _, err := c.Fetch(context.Background(), "PMC-1"); err != nil {
		t.Fatalf("a 1 MiB body must be accepted: %v", err)
	}
}

// Every credential form is redacted from the body excerpt before it is truncated, so a
// secret straddling the cut cannot survive half-visible.
func TestFetch_RedactsBeforeTruncating(t *testing.T) {
	body := strings.Repeat("a", maxErrorBody-5) + testToken + " " + testEmail + " " + testBasic
	srv := newServer(t, answer(http.StatusInternalServerError, body))
	opts := testOptions(srv.URL)
	opts.MaxRetries = 0
	c, _ := testClient(opts, basicCreds())
	_, err := c.Fetch(context.Background(), "PMC-1")
	jerr := asJiraError(t, err)
	if strings.Contains(jerr.Body, testToken[:5]) {
		t.Errorf("Body keeps part of the token: %q", jerr.Body)
	}
	if !strings.Contains(jerr.Body, "truncated") {
		t.Errorf("Body = %q, want a truncation marker", jerr.Body)
	}
}

func TestCredentials_NeverPrinted(t *testing.T) {
	for _, c := range []Credentials{basicCreds(), NewCredentials(AuthBearer, "", testToken)} {
		out := fmt.Sprintf("%v %+v %#v %s", c, c, c, c)
		out += fmt.Sprintf("%v %+v %#v", struct{ C Credentials }{c}, &c, struct{ c Credentials }{c})
		out += fmt.Sprintf("%v %+v", NewHTTPClient(Options{}, c), *NewHTTPClient(Options{}, c))
		assertNoSecret(t, out)
	}
}

func TestCredentials_Secrets(t *testing.T) {
	got := basicCreds().secrets()
	if len(got) != 3 || got[0] != testBasic || got[1] != testToken || got[2] != testEmail {
		t.Errorf("basic secrets = %q", got)
	}
	if got := NewCredentials(AuthBearer, "", testToken).secrets(); len(got) != 1 || got[0] != testToken {
		t.Errorf("bearer secrets = %q", got)
	}
}
