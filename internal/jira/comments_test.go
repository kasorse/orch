package jira

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
)

func commentJSON(id, author, created, body string) string {
	return fmt.Sprintf(`{"id":%q,"self":"x","author":{"accountId":%q,"name":%q,"displayName":"Someone"},"body":%q,"created":%q,"updated":%q}`,
		id, author, "u-"+author, body, created, created)
}

func TestComments_PagesInOrder(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		switch r.URL.Query().Get("startAt") {
		case "0":
			fmt.Fprint(w, `{"startAt":0,"maxResults":50,"total":3,"comments":[`+
				commentJSON("10", "bot", "2026-10-29T10:02:00.000+0000", "[orchestrator] question")+`,`+
				commentJSON("11", "ann", "2026-10-29T11:40:00.000+0200", "an answer\r\nsecond line")+`]}`)
		case "2":
			fmt.Fprint(w, `{"startAt":2,"maxResults":50,"total":3,"comments":[`+
				commentJSON("12", "bob", "2026-10-29T12:00:00.000Z", "")+`]}`)
		default:
			t.Errorf("unexpected startAt in %s", r.URL)
		}
	})
	c, _ := testClient(testOptions(srv.URL+"/jira"), basicCreds())
	got, err := c.Comments(context.Background(), "pay-12")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "10" || got[1].ID != "11" || got[2].ID != "12" {
		t.Fatalf("comments = %+v", got)
	}
	if got[0].Author != "bot" || got[1].Body != "an answer\nsecond line" || got[2].Body != "" {
		t.Errorf("comments = %+v", got)
	}
	if want := time.Date(2026, 10, 29, 9, 40, 0, 0, time.UTC); !got[1].Created.Equal(want) {
		t.Errorf("created = %s, want %s", got[1].Created, want)
	}

	reqs := srv.requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d requests, want 2", len(reqs))
	}
	if reqs[0].Method != http.MethodGet || reqs[0].URL.Path != "/jira/rest/api/2/issue/PAY-12/comment" ||
		reqs[0].URL.Query().Get("orderBy") != "created" {
		t.Errorf("got %s %s", reqs[0].Method, reqs[0].URL)
	}
}

func TestComments_AtMostAHundred(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		var list []string
		for i := range 50 {
			list = append(list, commentJSON(fmt.Sprint(n*100+i), "ann", "2026-10-29T10:02:00.000+0000", "b"))
		}
		fmt.Fprint(w, `{"total":500,"comments":[`+strings.Join(list, ",")+`]}`)
	})
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	got, err := c.Comments(context.Background(), "PAY-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 100 || len(srv.requests()) != 2 {
		t.Errorf("%d comments after %d requests, want 100 after 2", len(got), len(srv.requests()))
	}
}

func TestComments_DataCenterAuthorIsName(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, `{"total":1,"comments":[`+commentJSON("10", "bot", "2026-10-29T10:02:00.000+0000", "x")+`]}`))
	c, _ := testClient(bearerOptions(srv.URL), NewCredentials(AuthBearer, "", testToken))
	got, err := c.Comments(context.Background(), "PAY-1")
	if err != nil || len(got) != 1 || got[0].Author != "u-bot" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestComments_Errors(t *testing.T) {
	good := commentJSON("10", "ann", "2026-10-29T10:02:00.000+0000", "b")
	cases := []struct {
		name   string
		status int
		body   string
		kind   apperr.JiraFailureKind
		detail string
	}{
		{"not found", 404, "", apperr.JiraNotFound, "JIRA_EMAIL"},
		{"redirect", 302, "", apperr.JiraRedirect, ""},
		{"over 1 MiB", 200, strings.Repeat("x", maxResponseBody+1), apperr.JiraInvalidResponse, "larger than 1 MiB"},
		{"no comments", 200, `{"total":0}`, apperr.JiraInvalidResponse, "comments: missing"},
		{"not json", 200, `[]`, apperr.JiraInvalidResponse, "not a list of comments"},
		{"no id", 200, `{"comments":[{"author":{"accountId":"a"},"created":"2026-10-29T10:02:00.000+0000","body":"b"}]}`,
			apperr.JiraInvalidResponse, "comments[0].id"},
		{"no author", 200, `{"comments":[{"id":"1","created":"2026-10-29T10:02:00.000+0000","body":"b"}]}`,
			apperr.JiraInvalidResponse, "comments[0].author: missing"},
		{"author without accountId", 200, `{"comments":[` + good + `,{"id":"1","author":{"name":"x"},"created":"2026-10-29T10:02:00.000+0000","body":"b"}]}`,
			apperr.JiraInvalidResponse, "comments[1].author.accountId: missing or empty"},
		{"no created", 200, `{"comments":[{"id":"1","author":{"accountId":"a"},"body":"b"}]}`,
			apperr.JiraInvalidResponse, "comments[0].created"},
		{"bad created", 200, `{"comments":[{"id":"1","author":{"accountId":"a"},"created":"yesterday","body":"b"}]}`,
			apperr.JiraInvalidResponse, "comments[0].created: not a timestamp"},
		{"body object", 200, `{"comments":[{"id":"1","author":{"accountId":"a"},"created":"2026-10-29T10:02:00.000+0000","body":{"type":"doc"}}]}`,
			apperr.JiraInvalidResponse, "comments[0].body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, answer(tc.status, tc.body))
			c, _ := testClient(testOptions(srv.URL), basicCreds())
			got, err := c.Comments(context.Background(), "PAY-1")
			jerr := asJiraError(t, err)
			if got != nil {
				t.Errorf("comments = %+v on an error", got)
			}
			if jerr.Kind != tc.kind || !strings.Contains(jerr.Detail, tc.detail) {
				t.Errorf("got %s %q, want %s containing %q", jerr.Kind, jerr.Detail, tc.kind, tc.detail)
			}
		})
	}
}

func TestMyself(t *testing.T) {
	cases := []struct {
		name string
		opts func(string) Options
		cred Credentials
		want string
	}{
		{"cloud", testOptions, basicCreds(), "acc-1"},
		{"data center", bearerOptions, NewCredentials(AuthBearer, "", testToken), "jdoe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, answer(http.StatusOK, `{"self":"x","accountId":"acc-1","name":"jdoe","displayName":"J","active":true}`))
			c, _ := testClient(tc.opts(srv.URL+"/jira"), tc.cred)
			got, err := c.Myself(context.Background())
			if err != nil || got != tc.want {
				t.Errorf("got %q, %v, want %q", got, err, tc.want)
			}
			if r := srv.requests()[0]; r.Method != http.MethodGet || r.URL.Path != "/jira/rest/api/2/myself" {
				t.Errorf("got %s %s", r.Method, r.URL.Path)
			}
		})
	}
}

func TestMyself_Errors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		opts   func(string) Options
		cred   Credentials
		kind   apperr.JiraFailureKind
		detail string
	}{
		{"cloud without accountId", 200, `{"name":"jdoe"}`, testOptions, basicCreds(), apperr.JiraInvalidResponse, "myself.accountId: missing or empty"},
		{"data center without name", 200, `{"accountId":"a"}`, bearerOptions, NewCredentials(AuthBearer, "", testToken), apperr.JiraInvalidResponse, "myself.name: missing or empty"},
		{"accountId number", 200, `{"accountId":7}`, testOptions, basicCreds(), apperr.JiraInvalidResponse, "myself.accountId"},
		{"not an object", 200, `[]`, testOptions, basicCreds(), apperr.JiraInvalidResponse, "myself: not an object"},
		{"unauthorized", 401, "", testOptions, basicCreds(), apperr.JiraAuth, "JIRA_EMAIL/JIRA_API_TOKEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, answer(tc.status, tc.body))
			c, _ := testClient(tc.opts(srv.URL), tc.cred)
			got, err := c.Myself(context.Background())
			jerr := asJiraError(t, err)
			if got != "" || jerr.Kind != tc.kind || !strings.Contains(jerr.Detail, tc.detail) {
				t.Errorf("got %q, %s %q, want %s containing %q", got, jerr.Kind, jerr.Detail, tc.kind, tc.detail)
			}
		})
	}
}
