package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
)

// testToken is assembled at run time: a token-shaped literal must not sit in the repository.
var testToken = "glpat" + "-" + strings.Repeat("x7", 10)

func server(t *testing.T, h http.HandlerFunc) (*Client, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewHTTPClient(Options{BaseURL: srv.URL, AllowedGroups: []string{"acme/payments"}, TokenEnv: "GITLAB_READ_TOKEN", MaxRetries: 1}, NewToken(testToken))
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c, &seen
}

func mrBody(base string, iid int, extra string) string {
	return `{"iid":` + strconv.Itoa(iid) + `,"project_id":7,"source_project_id":7,"state":"opened",` +
		`"source_branch":"fix/PAY-31-round","target_branch":"main","sha":"` + strings.Repeat("a", 40) + `",` +
		`"created_at":"2026-11-10T23:30:00.000Z","title":"PAY-31: Round totals","description":"Body",` +
		`"web_url":"` + base + `/acme/payments/billing/-/merge_requests/` + strconv.Itoa(iid) + `"` + extra + `}`
}

func TestMergeRequest(t *testing.T) {
	var base string
	c, seen := server(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mrBody(base, 12, `,"author":{"name":"x"}`)))
	})
	base = c.opts.BaseURL
	mr, err := c.MergeRequest(context.Background(), "acme/payments/billing", 12)
	if err != nil {
		t.Fatal(err)
	}
	if mr.IID != 12 || mr.SourceBranch != "fix/PAY-31-round" || mr.TargetBranch != "main" || mr.State != "opened" || mr.Fork || mr.Description != "Body" {
		t.Fatalf("mr = %+v", mr)
	}
	r := (*seen)[0]
	if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/v4/projects/acme%2Fpayments%2Fbilling/merge_requests/12" {
		t.Fatalf("request %s %s", r.Method, r.URL.EscapedPath())
	}
	if r.Header.Get("PRIVATE-TOKEN") != testToken || r.Header.Get("Authorization") != "" {
		t.Fatal("the token must go only in PRIVATE-TOKEN")
	}
}

func TestMergeRequest_Refusals(t *testing.T) {
	cases := map[string]string{
		"fork":         `,"source_project_id":8`,
		"bad sha":      `,"sha":"abc"`,
		"bad state":    `,"state":"draft"`,
		"other url":    `,"web_url":"https://evil.example/x/-/merge_requests/12"`,
		"spaced ref":   `,"source_branch":"a b"`,
		"leading dash": `,"target_branch":"-x"`,
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			var base string
			c, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
				// A later duplicate key wins in encoding/json, which lets one case override one field.
				_, _ = w.Write([]byte(mrBody(base, 12, extra)))
			})
			base = c.opts.BaseURL
			mr, err := c.MergeRequest(context.Background(), "acme/payments/billing", 12)
			if name == "fork" {
				if err != nil || !mr.Fork {
					t.Fatalf("fork: mr=%+v err=%v", mr, err)
				}
				return
			}
			var gerr *apperr.GitLabError
			if !errors.As(err, &gerr) || gerr.Kind != apperr.GitLabInvalidResponse {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestMergeRequest_OutsideAllowedGroupsSendsNothing(t *testing.T) {
	c, seen := server(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") })
	_, err := c.MergeRequest(context.Background(), "other/group/x", 1)
	var gerr *apperr.GitLabError
	if !errors.As(err, &gerr) || len(*seen) != 0 {
		t.Fatalf("err = %v, requests = %d", err, len(*seen))
	}
	if !strings.Contains(err.Error(), "allowed_groups") {
		t.Fatalf("err = %v", err)
	}
}

func TestLatestMergeRequest(t *testing.T) {
	var base string
	empty := false
	c, seen := server(t, func(w http.ResponseWriter, r *http.Request) {
		if empty {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte("[" + mrBody(base, 13, "") + "]"))
	})
	base = c.opts.BaseURL
	mr, ok, err := c.LatestMergeRequest(context.Background(), "acme/payments/billing", "fix/PAY-31-round")
	if err != nil || !ok || mr.IID != 13 {
		t.Fatalf("mr=%+v ok=%v err=%v", mr, ok, err)
	}
	q := (*seen)[0].URL.Query()
	if q.Get("source_branch") != "fix/PAY-31-round" || q.Get("order_by") != "created_at" || q.Get("sort") != "desc" {
		t.Fatalf("query %v", q)
	}
	empty = true
	if _, ok, err := c.LatestMergeRequest(context.Background(), "acme/payments/billing", "x"); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	empty = false
	if _, _, err := c.LatestMergeRequest(context.Background(), "acme/payments/billing", "other"); err == nil {
		t.Fatal("a merge request of another branch must be refused")
	}
}

func TestCheckReadOnly(t *testing.T) {
	cases := []struct {
		body string
		ok   bool
		want string
	}{
		{`{"active":true,"revoked":false,"scopes":["read_api"]}`, true, ""},
		{`{"active":true,"revoked":false,"scopes":["read_api","read_repository","read_user"]}`, true, ""},
		{`{"active":true,"revoked":false,"scopes":["api"]}`, false, "scopes: api"},
		{`{"active":true,"revoked":false,"scopes":["read_api","write_repository"]}`, false, "scopes: write_repository"},
		{`{"active":true,"revoked":false,"scopes":[]}`, false, "no scope"},
		{`{"active":true,"revoked":true,"scopes":["read_api"]}`, false, "revoked"},
		{`{"active":false,"revoked":false,"scopes":["read_api"]}`, false, "not active"},
		{`{"scopes":["read_api"]}`, false, "token record"},
	}
	for _, tc := range cases {
		c, seen := server(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) })
		err := c.CheckReadOnly(context.Background())
		if tc.ok != (err == nil) {
			t.Fatalf("%s: err = %v", tc.body, err)
		}
		if err != nil && !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want %q", tc.body, err, tc.want)
		}
		if p := (*seen)[0].URL.Path; p != "/api/v4/personal_access_tokens/self" {
			t.Fatalf("path %s", p)
		}
	}
}

func TestErrors_RedactTheTokenAndClassify(t *testing.T) {
	cases := []struct {
		status int
		kind   apperr.GitLabFailureKind
		tries  int
	}{
		{401, apperr.GitLabAuth, 1},
		{404, apperr.GitLabNotFound, 1},
		{302, apperr.GitLabRedirect, 1},
		{503, apperr.GitLabUnavailable, 2},
		{429, apperr.GitLabRateLimited, 2},
		{418, apperr.GitLabInvalidResponse, 1},
	}
	for _, tc := range cases {
		c, seen := server(t, func(w http.ResponseWriter, r *http.Request) {
			if tc.status == 302 {
				w.Header().Set("Location", "https://login.example/?t="+testToken)
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte("echo " + r.Header.Get("PRIVATE-TOKEN")))
		})
		err := c.CheckReadOnly(context.Background())
		var gerr *apperr.GitLabError
		if !errors.As(err, &gerr) || gerr.Kind != tc.kind {
			t.Fatalf("%d: err = %v", tc.status, err)
		}
		if strings.Contains(err.Error(), testToken) {
			t.Fatalf("%d: the token leaked: %v", tc.status, err)
		}
		if len(*seen) != tc.tries {
			t.Fatalf("%d: %d requests, want %d", tc.status, len(*seen), tc.tries)
		}
	}
}

func TestParseMergeRequestURL(t *testing.T) {
	base := "https://gitlab.example.com"
	ok := map[string]struct {
		project string
		iid     int
	}{
		"https://gitlab.example.com/acme/payments/billing/-/merge_requests/1294": {"acme/payments/billing", 1294},
		"https://GitLab.example.com/acme/payments/billing/-/merge_requests/7/":   {"acme/payments/billing", 7},
		"https://gitlab.example.com/a/b/-/merge_requests/1":                      {"a/b", 1},
	}
	for raw, want := range ok {
		p, iid, good := ParseMergeRequestURL(base, raw)
		if !good || p != want.project || iid != want.iid {
			t.Errorf("%s: %s %d %v", raw, p, iid, good)
		}
	}
	for _, raw := range []string{
		"http://gitlab.example.com/a/b/-/merge_requests/1",
		"https://evil.example/a/b/-/merge_requests/1",
		"https://gitlab.example.com/a/b/-/merge_requests/01",
		"https://gitlab.example.com/a/b/-/merge_requests/1?x=1",
		"https://gitlab.example.com/a/b/-/merge_requests/1/diffs",
		"https://gitlab.example.com/a/-/merge_requests/1",
		"https://u:p@gitlab.example.com/a/b/-/merge_requests/1",
		"https://gitlab.example.com/a/b/-/issues/1",
		"PAY-12",
	} {
		if _, _, good := ParseMergeRequestURL(base, raw); good {
			t.Errorf("%s accepted", raw)
		}
	}
	if p, iid, good := ParseMergeRequestURL("https://h.example/gitlab", "https://h.example/gitlab/a/b/-/merge_requests/3"); !good || p != "a/b" || iid != 3 {
		t.Errorf("context path: %s %d %v", p, iid, good)
	}
}

func TestAllowed(t *testing.T) {
	groups := []string{"acme/payments", "tools/"}
	for p, want := range map[string]bool{
		"acme/payments/billing": true, "acme/payments": true, "acme/paymentsx/a": false,
		"tools/x": true, "other/x": false, "acme/payments/../x": false,
	} {
		if Allowed(groups, p) != want {
			t.Errorf("Allowed(%q) = %v", p, !want)
		}
	}
}

func TestTokenPrintsRedacted(t *testing.T) {
	c := NewHTTPClient(Options{BaseURL: "https://gitlab.example.com"}, NewToken(testToken))
	for _, s := range []string{
		strings.Join([]string{c.token.String(), c.token.GoString()}, " "),
	} {
		if strings.Contains(s, testToken) {
			t.Fatal(s)
		}
	}
}
