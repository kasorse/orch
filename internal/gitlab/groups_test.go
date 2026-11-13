package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

func projectJSON(path, name, desc, branch string) string {
	d, b := "null", "null"
	if desc != "" {
		d = fmt.Sprintf("%q", desc)
	}
	if branch != "" {
		b = fmt.Sprintf("%q", branch)
	}
	return fmt.Sprintf(`{"id":1,"path_with_namespace":%q,"name":%q,"description":%s,"default_branch":%s,"archived":false}`, path, name, d, b)
}

func TestGroupProjects_TwoPagesAndAForeignProject(t *testing.T) {
	c, seen := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "":
			w.Header().Set("X-Next-Page", "2")
			_, _ = w.Write([]byte("[" + projectJSON("acme/payments/billing", "billing", "Bills\nand\tinvoices", "main") + "," +
				projectJSON("other/group/x", "x", "", "main") + "," +
				projectJSON("acme/paymentsx/y", "y", "", "main") + "]"))
		case "2":
			_, _ = w.Write([]byte("[" + projectJSON("acme/payments/team/ledger", "ledger", "", "master") + "," +
				projectJSON("acme/payments/empty", "empty", "", "") + "]"))
		default:
			t.Errorf("unexpected page %s", r.URL)
		}
	})
	var logged []string
	c.opts.Logf = func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }

	got, err := c.GroupProjects(context.Background(), "acme/payments")
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Project{
		{Path: "acme/payments/billing", Name: "billing", Description: "Bills and invoices", DefaultBranch: "main"},
		{Path: "acme/payments/team/ledger", Name: "ledger", DefaultBranch: "master"},
		{Path: "acme/payments/empty", Name: "empty"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("projects = %+v, want %+v", got, want)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "2 projects") {
		t.Errorf("log = %v, want one line counting the 2 dropped projects", logged)
	}
	if len(*seen) != 2 {
		t.Fatalf("%d requests, want 2", len(*seen))
	}
	for _, r := range *seen {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/v4/groups/acme%2Fpayments/projects" {
			t.Errorf("request %s %s", r.Method, r.URL.EscapedPath())
		}
		q := r.URL.Query()
		if q.Get("include_subgroups") != "true" || q.Get("archived") != "false" || q.Get("per_page") != "100" {
			t.Errorf("query %v", q)
		}
		if r.Header.Get("PRIVATE-TOKEN") != testToken {
			t.Error("the token must go in PRIVATE-TOKEN")
		}
	}
}

func TestGroupProjects_FollowsALinkHeaderOnTheBase(t *testing.T) {
	var base string
	c, seen := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("Link", fmt.Sprintf(`<%s/api/v4/groups/acme%%2Fpayments/projects?page=2&per_page=100>; rel="next", <%s/x?page=1>; rel="first"`, base, base))
			_, _ = w.Write([]byte("[" + projectJSON("acme/payments/a", "a", "", "main") + "]"))
			return
		}
		_, _ = w.Write([]byte("[" + projectJSON("acme/payments/b", "b", "", "main") + "]"))
	})
	base = c.opts.BaseURL
	got, err := c.GroupProjects(context.Background(), "acme/payments")
	if err != nil || len(got) != 2 || got[1].Path != "acme/payments/b" {
		t.Fatalf("got %+v, err %v", got, err)
	}
	if len(*seen) != 2 || (*seen)[1].URL.Query().Get("page") != "2" {
		t.Errorf("requests = %d", len(*seen))
	}
}

func TestGroupProjects_RefusesANextPageOutsideTheBase(t *testing.T) {
	for name, link := range map[string]string{
		"other host":  `<https://evil.example/api/v4/groups/acme%2Fpayments/projects?page=2>; rel="next"`,
		"other path":  `</api/v4/projects/1/secrets?page=2>; rel="next"`,
		"other level": `<%s/other/api/v4/groups/acme%2Fpayments/projects?page=2>; rel="next"`,
	} {
		t.Run(name, func(t *testing.T) {
			var base string
			c, seen := server(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Link", strings.ReplaceAll(link, "%s", base))
				_, _ = w.Write([]byte("[" + projectJSON("acme/payments/a", "a", "", "main") + "]"))
			})
			base = c.opts.BaseURL
			_, err := c.GroupProjects(context.Background(), "acme/payments")
			var gerr *apperr.GitLabError
			if !errors.As(err, &gerr) || gerr.Kind != apperr.GitLabInvalidResponse {
				t.Fatalf("err = %v", err)
			}
			if len(*seen) != 1 {
				t.Errorf("%d requests, want 1: the foreign page must not be requested", len(*seen))
			}
		})
	}
}

func TestGroupProjects_RefusesARedirect(t *testing.T) {
	c, seen := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://login.example/?t="+testToken)
		w.WriteHeader(http.StatusFound)
	})
	_, err := c.GroupProjects(context.Background(), "acme/payments")
	var gerr *apperr.GitLabError
	if !errors.As(err, &gerr) || gerr.Kind != apperr.GitLabRedirect {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), testToken) || len(*seen) != 1 {
		t.Errorf("err = %v, %d requests", err, len(*seen))
	}
}

func TestGroupProjects_GroupOutsideAllowedGroupsSendsNothing(t *testing.T) {
	c, seen := server(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") })
	for _, group := range []string{"other", "acme", "acme/paymentsx", "acme/payments/../x", ""} {
		_, err := c.GroupProjects(context.Background(), group)
		var gerr *apperr.GitLabError
		if !errors.As(err, &gerr) || gerr.Kind != apperr.GitLabNotFound || !strings.Contains(err.Error(), "allowed_groups") {
			t.Errorf("%q: err = %v", group, err)
		}
	}
	if len(*seen) != 0 {
		t.Errorf("%d requests", len(*seen))
	}
}

func TestGroupProjects_StrictDecoding(t *testing.T) {
	for name, body := range map[string]string{
		"not a list":  `{"projects":[]}`,
		"null":        `null`,
		"no path":     `[{"name":"a"}]`,
		"path number": `[{"path_with_namespace":3,"name":"a"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := server(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
			_, err := c.GroupProjects(context.Background(), "acme/payments")
			var gerr *apperr.GitLabError
			if !errors.As(err, &gerr) || gerr.Kind != apperr.GitLabInvalidResponse {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestGroupProjects_RetriesAServerErrorAndRedactsTheToken(t *testing.T) {
	c, seen := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("echo " + r.Header.Get("PRIVATE-TOKEN")))
	})
	_, err := c.GroupProjects(context.Background(), "acme/payments")
	var gerr *apperr.GitLabError
	if !errors.As(err, &gerr) || gerr.Kind != apperr.GitLabUnavailable || len(*seen) != 2 {
		t.Fatalf("err = %v, %d requests", err, len(*seen))
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("the token leaked: %v", err)
	}
}
