package jira

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kasorse/orch/internal/apperr"
)

// The shape GitLab's Jira integration gives a merge request link (seen on Jira Cloud, 2026-11).
const remoteLinksAnswer = `[
 {"id":119938,"self":"x","relationship":"merge request","object":{"url":"https://gitlab.example.com/acme/payments/billing/-/merge_requests/1290","title":"billing !1290: PAY-31: Round totals","icon":{}}},
 {"id":119949,"relationship":"merge request","object":{"url":"https://gitlab.example.com/acme/payments/billing/-/merge_requests/1293","title":"billing !1293"}},
 {"id":119956,"relationship":"merge request","object":{"url":"https://gitlab.example.com/acme/payments/billing/-/merge_requests/1294","title":"billing !1294"}}]`

func TestRemoteLinks(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, remoteLinksAnswer))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	links, err := c.RemoteLinks(context.Background(), "PAY-31")
	if err != nil || len(links) != 3 {
		t.Fatalf("links = %+v, err = %v", links, err)
	}
	if links[2].URL != "https://gitlab.example.com/acme/payments/billing/-/merge_requests/1294" || links[2].Relationship != "merge request" {
		t.Fatalf("links[2] = %+v", links[2])
	}
	if r := srv.requests()[0]; r.Method != http.MethodGet || r.URL.Path != "/rest/api/2/issue/PAY-31/remotelink" {
		t.Fatalf("request %s %s", r.Method, r.URL.Path)
	}
	for _, bad := range []string{`{}`, `[{"object":{}}]`, `[{"relationship":"x"}]`} {
		srv := newServer(t, answer(http.StatusOK, bad))
		c, _ := testClient(testOptions(srv.URL), basicCreds())
		if _, err := c.RemoteLinks(context.Background(), "PAY-31"); asJiraError(t, err).Op != apperr.JiraOpLinks {
			t.Fatalf("%s: err = %v", bad, err)
		}
	}
}

func TestDescription(t *testing.T) {
	for body, want := range map[string]string{
		`{"fields":{"description":"Line one\nLine two"}}`: "Line one\nLine two",
		`{"fields":{"description":null}}`:                 "",
		`{"fields":{}}`:                                   "",
	} {
		srv := newServer(t, answer(http.StatusOK, body))
		c, _ := testClient(testOptions(srv.URL), basicCreds())
		got, err := c.Description(context.Background(), "PAY-31")
		if err != nil || got != want {
			t.Fatalf("%s: %q, %v", body, got, err)
		}
		if r := srv.requests()[0]; r.URL.RawQuery != "fields=description" {
			t.Fatalf("query %s", r.URL.RawQuery)
		}
	}
	srv := newServer(t, answer(http.StatusOK, `{"fields":{"description":{"type":"doc"}}}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	if _, err := c.Description(context.Background(), "PAY-31"); err == nil || !strings.Contains(err.Error(), "not a string") {
		t.Fatalf("ADF: err = %v", err)
	}
}

func TestSetDescription(t *testing.T) {
	var log bodyLog
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		b, _ := io.ReadAll(r.Body)
		log.add(b)
		w.WriteHeader(http.StatusNoContent)
	})
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	if err := c.SetDescription(context.Background(), "PAY-31", "Old\n\nUpdate 2026-11-11: new"); err != nil {
		t.Fatal(err)
	}
	r := srv.requests()[0]
	if r.Method != http.MethodPut || r.URL.Path != "/rest/api/2/issue/PAY-31" {
		t.Fatalf("request %s %s", r.Method, r.URL.Path)
	}
	if got, want := string(log.all()[0]), `{"fields":{"description":"Old\n\nUpdate 2026-11-11: new"}}`; got != want {
		t.Fatalf("body %s, want %s", got, want)
	}
}

// A 500 may have written the description: it is returned at once, never sent again.
func TestSetDescription_ServerErrorIsNotRetried(t *testing.T) {
	srv := newServer(t, answer(http.StatusInternalServerError, ""))
	opts := testOptions(srv.URL)
	opts.MaxRetries = 3
	c, sleeps := testClient(opts, basicCreds())
	err := c.SetDescription(context.Background(), "PAY-31", "x")
	jerr := asJiraError(t, err)
	if jerr.Op != apperr.JiraOpDescription || !MayExist(err) || len(srv.requests()) != 1 || len(*sleeps) != 0 {
		t.Fatalf("err = %+v, requests %d", jerr, len(srv.requests()))
	}
}

func TestSetDescription_TooLongIsNotSent(t *testing.T) {
	srv := newServer(t, answer(http.StatusNoContent, ""))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	err := c.SetDescription(context.Background(), "PAY-31", strings.Repeat("я", MaxDescription+1))
	if err == nil || MayExist(err) || len(srv.requests()) != 0 {
		t.Fatalf("err = %v, requests %d", err, len(srv.requests()))
	}
}

func TestAppendUpdate(t *testing.T) {
	cases := []struct{ old, text, want string }{
		{"", "Round half up", "Update 2026-11-11: Round half up"},
		{"Totals are wrong.\n\n", "  Round half up\r\nalso VAT  ", "Totals are wrong.\n\nUpdate 2026-11-11: Round half up\nalso VAT"},
		{"h1. Task\n* one", "- [x] keep *markup*", "h1. Task\n* one\n\nUpdate 2026-11-11: - [x] keep *markup*"},
	}
	for _, tc := range cases {
		if got := AppendUpdate(tc.old, "2026-11-11", tc.text); got != tc.want {
			t.Errorf("AppendUpdate(%q, %q) = %q, want %q", tc.old, tc.text, got, tc.want)
		}
	}
	long := AppendUpdate("", "2026-11-11", strings.Repeat("ж", maxUpdate))
	if !utf8.ValidString(long) || !strings.HasSuffix(long, "…") || len(long) > maxUpdate+64 {
		t.Fatalf("cut: %d bytes, valid %v", len(long), utf8.ValidString(long))
	}
}

func TestMergeRequestComment_Updated(t *testing.T) {
	c := MergeRequestComment{IID: 7, URL: "https://gitlab.example.com/a/b/-/merge_requests/7", Title: "Round half up", RunID: "r", Updated: true}
	if got, want := c.Wiki(), "Merge request [!7|https://gitlab.example.com/a/b/-/merge_requests/7] updated: Round half up\nRun r"; got != want {
		t.Fatalf("Wiki() = %q, want %q", got, want)
	}
}
