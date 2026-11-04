package jira

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

// searchIssue is one issue of a search answer; fields is spliced in after the always-present ones.
func searchIssue(key, fields string) string {
	return `{"id":"1","self":"x","key":"` + key + `","fields":{"summary":"S ` + key + `","issuetype":{"name":"Task"},` +
		`"status":{"name":"To Do"},"labels":["ai-run"],"reporter":{"accountId":"acc-1","name":"jdoe"}` + fields + `}}`
}

func bearerOptions(baseURL string) Options {
	opts := testOptions(baseURL)
	opts.Auth = AuthBearer
	return opts
}

func TestSearch_CloudPagesByToken(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		switch r.URL.Query().Get("nextPageToken") {
		case "":
			fmt.Fprint(w, `{"issues":[`+searchIssue("PAY-1", "")+`,`+searchIssue("PAY-2", `,"components":[{"name":"billing"}]`)+`],"nextPageToken":"T2"}`)
		case "T2":
			fmt.Fprint(w, `{"issues":[`+searchIssue("PAY-3", "")+`],"isLast":true}`)
		default:
			t.Errorf("unexpected token in %s", r.URL)
		}
	})
	c, _ := testClient(testOptions(srv.URL+"/jira"), basicCreds())
	got, err := c.Search(context.Background(), `project in ("PAY")`, 10)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{got[0].Key, got[1].Key, got[2].Key}
	if want := []string{"PAY-1", "PAY-2", "PAY-3"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	want := domain.TicketHead{
		Key: "PAY-2", Summary: "S PAY-2", Status: "To Do", Type: "Task",
		Labels: []string{"ai-run"}, Components: []string{"billing"}, Reporter: "acc-1",
	}
	if !reflect.DeepEqual(got[1], want) {
		t.Errorf("head = %+v, want %+v", got[1], want)
	}

	reqs := srv.requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want 2", len(reqs))
	}
	for _, r := range reqs {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/jira/rest/api/2/search/jql" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if q.Get("jql") != `project in ("PAY")` || q.Get("fields") != "summary,status,labels,components,reporter,issuetype" || q.Get("maxResults") != "50" {
			t.Errorf("query = %v", q)
		}
		if r.Header.Get("Authorization") != "Basic "+testBasic {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
	}
	if reqs[1].URL.Query().Get("nextPageToken") != "T2" {
		t.Errorf("second page query = %v", reqs[1].URL.Query())
	}
}

func TestSearch_DataCenterPagesByStartAt(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		switch r.URL.Query().Get("startAt") {
		case "0":
			fmt.Fprint(w, `{"startAt":0,"total":3,"issues":[`+searchIssue("PAY-1", "")+`,`+searchIssue("PAY-2", "")+`]}`)
		case "2":
			fmt.Fprint(w, `{"startAt":2,"total":3,"issues":[`+searchIssue("PAY-3", "")+`]}`)
		default:
			t.Errorf("unexpected startAt in %s", r.URL)
		}
	})
	c, _ := testClient(bearerOptions(srv.URL), NewCredentials(AuthBearer, "", testToken))
	got, err := c.Search(context.Background(), "jql", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2].Key != "PAY-3" || got[0].Reporter != "jdoe" {
		t.Errorf("heads = %+v", got)
	}
	reqs := srv.requests()
	if len(reqs) != 2 || reqs[0].URL.Path != "/rest/api/2/search" {
		t.Fatalf("requests = %d, first %v", len(reqs), reqs[0].URL)
	}
	if reqs[0].Header.Get("Authorization") != "Bearer "+testToken {
		t.Errorf("Authorization = %q", reqs[0].Header.Get("Authorization"))
	}
	if reqs[0].URL.Query().Has("nextPageToken") {
		t.Errorf("Data Center is paged by startAt: %v", reqs[0].URL)
	}
}

func TestSearch_StopsAtMax(t *testing.T) {
	for _, auth := range []string{AuthBasic, AuthBearer} {
		t.Run(auth, func(t *testing.T) {
			srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
				// Every page has two issues and says there is more, without end.
				a, b := fmt.Sprintf("PAY-%d", 2*n-1), fmt.Sprintf("PAY-%d", 2*n)
				fmt.Fprint(w, `{"total":1000,"nextPageToken":"more","issues":[`+searchIssue(a, "")+`,`+searchIssue(b, "")+`]}`)
			})
			opts := testOptions(srv.URL)
			opts.Auth = auth
			c, _ := testClient(opts, NewCredentials(auth, testEmail, testToken))
			got, err := c.Search(context.Background(), "jql", 3)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 3 || got[2].Key != "PAY-3" {
				t.Errorf("heads = %+v", got)
			}
			if n := len(srv.requests()); n != 2 {
				t.Errorf("%d requests, want 2", n)
			}
		})
	}
}

func TestSearch_NoIssues(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, `{"issues":[],"isLast":true}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	got, err := c.Search(context.Background(), "jql", 10)
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestSearch_RetriesATransientFailure(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"issues":[`+searchIssue("PAY-1", "")+`],"isLast":true}`)
	})
	c, sleeps := testClient(testOptions(srv.URL), basicCreds())
	got, err := c.Search(context.Background(), "jql", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if len(*sleeps) != 1 {
		t.Errorf("sleeps = %v, want one backoff", *sleeps)
	}
}

func TestSearch_Errors(t *testing.T) {
	big := strings.Repeat("x", maxResponseBody+10)
	cases := []struct {
		name    string
		status  int
		body    string
		kind    apperr.JiraFailureKind
		detail  string
		retries bool
	}{
		{"page over 1 MiB", 200, big, apperr.JiraInvalidResponse, "larger than 1 MiB", false},
		{"redirect", 302, "", apperr.JiraRedirect, "", false},
		{"unauthorized", 401, "", apperr.JiraAuth, "JIRA_EMAIL/JIRA_API_TOKEN", false},
		{"bad jql", 400, "", apperr.JiraInvalidResponse, "unexpected status 400", false},
		{"unavailable", 503, "", apperr.JiraUnavailable, "", true},
		{"not json", 200, `<html>`, apperr.JiraInvalidResponse, "not a search result", false},
		{"no issues", 200, `{}`, apperr.JiraInvalidResponse, "issues: missing", false},
		{"issues object", 200, `{"issues":{}}`, apperr.JiraInvalidResponse, "not a search result", false},
		{"no key", 200, `{"issues":[{"fields":{}}]}`, apperr.JiraInvalidResponse, "issues[0].key: missing", false},
		{"bad key", 200, `{"issues":[{"key":"x","fields":{}}]}`, apperr.JiraInvalidResponse, `issues[0].key "x" is not an issue key`, false},
		{"labels string", 200, `{"issues":[` + searchIssue("PAY-1", "") + `,` + strings.Replace(searchIssue("PAY-2", ""), `"labels":["ai-run"]`, `"labels":"ai-run"`, 1) + `]}`,
			apperr.JiraInvalidResponse, "issues[1].fields.labels: not an array", false},
		{"label number", 200, `{"issues":[` + strings.Replace(searchIssue("PAY-2", ""), `"labels":["ai-run"]`, `"labels":["a",3]`, 1) + `]}`,
			apperr.JiraInvalidResponse, "issues[0].fields.labels[1]: not a string", false},
		{"too many labels", 200, `{"issues":[` + strings.Replace(searchIssue("PAY-2", ""), `"labels":["ai-run"]`, `"labels":[`+strings.TrimSuffix(strings.Repeat(`"a",`, 101), ",")+`]`, 1) + `]}`,
			apperr.JiraInvalidResponse, "labels: more than 100 entries", false},
		{"no status", 200, `{"issues":[{"key":"PAY-1","fields":{"summary":"S","issuetype":{"name":"Task"}}}]}`,
			apperr.JiraInvalidResponse, "issues[0].fields.status: missing", false},
		{"status string", 200, `{"issues":[` + strings.Replace(searchIssue("PAY-2", ""), `"status":{"name":"To Do"}`, `"status":"To Do"`, 1) + `]}`,
			apperr.JiraInvalidResponse, "fields.status: not an object", false},
		{"no summary", 200, `{"issues":[{"key":"PAY-1","fields":{"issuetype":{"name":"Task"}}}]}`,
			apperr.JiraInvalidResponse, "fields.summary: missing or empty", false},
		{"no issuetype", 200, `{"issues":[{"key":"PAY-1","fields":{"summary":"S"}}]}`,
			apperr.JiraInvalidResponse, "fields.issuetype: missing", false},
		{"reporter without accountId", 200, `{"issues":[` + strings.Replace(searchIssue("PAY-2", ""), `"accountId":"acc-1"`, `"displayName":"x"`, 1) + `]}`,
			apperr.JiraInvalidResponse, "fields.reporter.accountId: missing or empty", false},
		{"reporter string", 200, `{"issues":[` + strings.Replace(searchIssue("PAY-2", ""), `"reporter":{"accountId":"acc-1","name":"jdoe"}`, `"reporter":"jdoe"`, 1) + `]}`,
			apperr.JiraInvalidResponse, "fields.reporter: not an object", false},
		{"components object", 200, `{"issues":[` + searchIssue("PAY-1", `,"components":{}`) + `]}`,
			apperr.JiraInvalidResponse, "fields.components: not an array", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, answer(tc.status, tc.body))
			c, _ := testClient(testOptions(srv.URL), basicCreds())
			got, err := c.Search(context.Background(), "jql", 10)
			jerr := asJiraError(t, err)
			if got != nil {
				t.Errorf("heads = %+v on an error", got)
			}
			if jerr.Kind != tc.kind || !strings.Contains(jerr.Detail, tc.detail) {
				t.Errorf("got %s %q, want %s containing %q", jerr.Kind, jerr.Detail, tc.kind, tc.detail)
			}
			if want := 1; !tc.retries && jerr.Attempts != want {
				t.Errorf("Attempts = %d, want %d (not retried)", jerr.Attempts, want)
			}
			if tc.retries && jerr.Attempts != 2 {
				t.Errorf("Attempts = %d, want 2 (one retry)", jerr.Attempts)
			}
		})
	}
}

func TestSearch_DataCenterReporterIsName(t *testing.T) {
	body := `{"total":1,"issues":[` + strings.Replace(searchIssue("PAY-1", ""), `"name":"jdoe"`, `"displayName":"J"`, 1) + `]}`
	srv := newServer(t, answer(http.StatusOK, body))
	c, _ := testClient(bearerOptions(srv.URL), NewCredentials(AuthBearer, "", testToken))
	_, err := c.Search(context.Background(), "jql", 10)
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraInvalidResponse || !strings.Contains(jerr.Detail, "fields.reporter.name: missing or empty") {
		t.Errorf("got %s %q", jerr.Kind, jerr.Detail)
	}
}

func TestSearch_NothingForMaxZero(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, `{"issues":[]}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	got, err := c.Search(context.Background(), "jql", 0)
	if err != nil || got != nil || len(srv.requests()) != 0 {
		t.Errorf("got %+v, %v after %d requests", got, err, len(srv.requests()))
	}
}
