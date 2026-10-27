package jira

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
)

const transitionsAnswer = `{"expand":"transitions","transitions":[
  {"id":"11","name":"Start progress","to":{"id":"3","name":"In Progress"},"hasScreen":false},
  {"id":"21","name":"Send to review","to":{"id":"4","name":"In Review"}},
  {"id":"31","name":"Done"}]}`

func TestComment_Request(t *testing.T) {
	var log bodyLog
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		b, _ := io.ReadAll(r.Body)
		log.add(b)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"10042","self":"https://example.atlassian.net/rest/api/2/issue/PAY-12/comment/10042","body":"x"}`)
	})
	c, _ := testClient(testOptions(srv.URL+"/jira/"), basicCreds())

	id, err := c.Comment(context.Background(), "PAY-12", "Merge request [!1|https://x/-/merge_requests/1]")
	if err != nil || id != "10042" {
		t.Fatalf("Comment = %q, %v; want 10042", id, err)
	}
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Method != http.MethodPost || req.URL.Path != "/jira/rest/api/2/issue/PAY-12/comment" || req.URL.RawQuery != "" {
		t.Errorf("request = %s %s?%s", req.Method, req.URL.Path, req.URL.RawQuery)
	}
	if got := req.Header.Get("Authorization"); got != "Basic "+testBasic {
		t.Errorf("Authorization = %q", got)
	}
	if got, want := string(log.all()[0]), `{"body":"Merge request [!1|https://x/-/merge_requests/1]"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestComment_StrictAnswers(t *testing.T) {
	cases := []struct{ name, body string }{
		{"not JSON", `<html>`},
		{"no id", `{}`},
		{"an id that is not a number", `{"id":"../../x"}`},
		{"a numeric id", `{"id":10042}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprint(w, tc.body)
			})
			c, _ := testClient(testOptions(srv.URL), basicCreds())
			_, err := c.Comment(context.Background(), "PAY-12", "x")
			jerr := asJiraError(t, err)
			if jerr.Kind != apperr.JiraInvalidResponse || jerr.Op != apperr.JiraOpComment || !MayExist(err) {
				t.Errorf("error = %+v, want an invalid response to a comment that may exist", jerr)
			}
			if len(srv.requests()) != 1 {
				t.Errorf("%d requests, want 1", len(srv.requests()))
			}
		})
	}
}

// A 200 where a 201 is the answer is not a comment's answer.
func TestComment_WrongSuccessStatus(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, `{"id":"1"}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	_, err := c.Comment(context.Background(), "PAY-12", "x")
	if jerr := asJiraError(t, err); jerr.Kind != apperr.JiraInvalidResponse || jerr.StatusCode != 200 {
		t.Errorf("error = %+v", jerr)
	}
}

// A 500 may have posted the comment: it is returned at once, never sent again.
func TestComment_ServerErrorIsNotRetried(t *testing.T) {
	srv := newServer(t, answer(http.StatusInternalServerError, ""))
	opts := testOptions(srv.URL)
	opts.MaxRetries = 3
	c, sleeps := testClient(opts, basicCreds())

	_, err := c.Comment(context.Background(), "PAY-12", "x")
	jerr := asJiraError(t, err)
	if jerr.Kind != apperr.JiraUnavailable || jerr.Op != apperr.JiraOpComment || jerr.StatusCode != 500 {
		t.Errorf("error = %+v", jerr)
	}
	if !MayExist(err) {
		t.Error("MayExist = false after a 500: the comment may have been posted")
	}
	if len(srv.requests()) != 1 || len(*sleeps) != 0 {
		t.Errorf("%d requests and %d waits, want 1 and 0", len(srv.requests()), len(*sleeps))
	}
}

func TestComment_RetriesOnlyANotNowAnswer(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"7"}`)
	})
	c, sleeps := testClient(testOptions(srv.URL), basicCreds())
	id, err := c.Comment(context.Background(), "PAY-12", "x")
	if err != nil || id != "7" || len(srv.requests()) != 2 || len(*sleeps) != 1 {
		t.Fatalf("Comment = %q, %v after %d requests and %d waits; want 7 after 2 and 1", id, err, len(srv.requests()), len(*sleeps))
	}
}

func TestComment_Refusals(t *testing.T) {
	cases := []struct {
		name   string
		status int
		kind   apperr.JiraFailureKind
	}{
		{"credentials", 401, apperr.JiraAuth},
		{"no permission", 403, apperr.JiraNotFound},
		{"no issue", 404, apperr.JiraNotFound},
		{"not now, no Retry-After", 429, apperr.JiraRateLimited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, answer(tc.status, ""))
			c, _ := testClient(testOptions(srv.URL), basicCreds())
			_, err := c.Comment(context.Background(), "PAY-12", "x")
			jerr := asJiraError(t, err)
			if jerr.Kind != tc.kind || MayExist(err) {
				t.Errorf("error = %+v, MayExist = %v; want %s and a refusal", jerr, MayExist(err), tc.kind)
			}
		})
	}
}

func TestComment_RejectedAndRedirect(t *testing.T) {
	srv := newServer(t, answer(http.StatusBadRequest, `{"errorMessages":["Comment body can not be empty!"]}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	_, err := c.Comment(context.Background(), "PAY-12", "")
	if jerr := asJiraError(t, err); jerr.Kind != apperr.JiraRejected || !strings.Contains(err.Error(), "Comment body can not be empty!") || MayExist(err) {
		t.Errorf("error = %v", err)
	}

	elsewhere := newServer(t, answer(http.StatusOK, "{}"))
	redirecting := newServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		http.Redirect(w, r, elsewhere.URL+"/login", http.StatusFound)
	})
	c, _ = testClient(testOptions(redirecting.URL), basicCreds())
	_, err = c.Comment(context.Background(), "PAY-12", "x")
	if jerr := asJiraError(t, err); jerr.Kind != apperr.JiraRedirect || MayExist(err) {
		t.Errorf("error = %v", err)
	}
	if len(elsewhere.requests()) != 0 {
		t.Error("the redirect was followed: the credentials went to another host")
	}
}

func TestComment_CancelledBeforeSending(t *testing.T) {
	srv := newServer(t, answer(http.StatusCreated, `{"id":"1"}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Comment(ctx, "PAY-12", "x")
	if jerr := asJiraError(t, err); jerr.Kind != apperr.JiraCancelled || MayExist(err) || len(srv.requests()) != 0 {
		t.Errorf("error = %v, MayExist = %v, %d requests", err, MayExist(err), len(srv.requests()))
	}
}

func TestTransitions(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, transitionsAnswer))
	c, _ := testClient(testOptions(srv.URL), basicCreds())

	got, err := c.Transitions(context.Background(), "PAY-12")
	if err != nil {
		t.Fatal(err)
	}
	want := []Transition{{"11", "Start progress", "In Progress"}, {"21", "Send to review", "In Review"}, {"31", "Done", ""}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Transitions = %v, want %v", got, want)
	}
	req := srv.requests()[0]
	if req.Method != http.MethodGet || req.URL.Path != "/rest/api/2/issue/PAY-12/transitions" {
		t.Errorf("request = %s %s", req.Method, req.URL.Path)
	}
}

func TestTransitions_StrictAnswers(t *testing.T) {
	cases := []struct{ name, body string }{
		{"not JSON", `nope`},
		{"no list", `{}`},
		{"an id that is not a number", `{"transitions":[{"id":"x","name":"A"}]}`},
		{"no name", `{"transitions":[{"id":"1","name":""}]}`},
		{"a name on two lines", `{"transitions":[{"id":"1","name":"A\nB"}]}`},
		{"a target on two lines", `{"transitions":[{"id":"1","name":"A","to":{"name":"B\nC"}}]}`},
		{"too many", `{"transitions":[` + strings.TrimSuffix(strings.Repeat(`{"id":"1","name":"A"},`, maxTransitions+1), ",") + `]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, answer(http.StatusOK, tc.body))
			c, _ := testClient(testOptions(srv.URL), basicCreds())
			_, err := c.Transitions(context.Background(), "PAY-12")
			if jerr := asJiraError(t, err); jerr.Kind != apperr.JiraInvalidResponse || jerr.Op != apperr.JiraOpTransitions {
				t.Errorf("error = %+v, want an invalid response", jerr)
			}
		})
	}
}

func TestTransitions_NoEmptyListFailure(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, `{"transitions":[]}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	got, err := c.Transitions(context.Background(), "PAY-12")
	if err != nil || len(got) != 0 {
		t.Errorf("Transitions = %v, %v; want none and no error", got, err)
	}
}

func TestTransition_Request(t *testing.T) {
	var log bodyLog
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		b, _ := io.ReadAll(r.Body)
		log.add(b)
		w.WriteHeader(http.StatusNoContent)
	})
	c, _ := testClient(testOptions(srv.URL), basicCreds())

	if err := c.Transition(context.Background(), "PAY-12", "21"); err != nil {
		t.Fatal(err)
	}
	req := srv.requests()[0]
	if req.Method != http.MethodPost || req.URL.Path != "/rest/api/2/issue/PAY-12/transitions" {
		t.Errorf("request = %s %s", req.Method, req.URL.Path)
	}
	if got, want := string(log.all()[0]), `{"transition":{"id":"21"}}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestTransition_Failures(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, ""))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	if err := c.Transition(context.Background(), "PAY-12", "21/../x"); err == nil || MayExist(err) || len(srv.requests()) != 0 {
		t.Errorf("a bad id: err = %v, MayExist = %v, %d requests; want a refusal before sending", err, MayExist(err), len(srv.requests()))
	}
	// Anything but 204 is not the answer to a transition.
	if err := c.Transition(context.Background(), "PAY-12", "21"); asJiraError(t, err).Kind != apperr.JiraInvalidResponse {
		t.Errorf("200: err = %v", err)
	}

	failing := newServer(t, answer(http.StatusInternalServerError, ""))
	c, _ = testClient(testOptions(failing.URL), basicCreds())
	err := c.Transition(context.Background(), "PAY-12", "21")
	if jerr := asJiraError(t, err); jerr.Op != apperr.JiraOpTransition || !MayExist(err) || len(failing.requests()) != 1 {
		t.Errorf("500: err = %v, MayExist = %v, %d requests; want one request that may have moved the issue", err, MayExist(err), len(failing.requests()))
	}

	refusing := newServer(t, answer(http.StatusBadRequest, `{"errorMessages":["Transition 21 is not valid"]}`))
	c, _ = testClient(testOptions(refusing.URL), basicCreds())
	err = c.Transition(context.Background(), "PAY-12", "21")
	if jerr := asJiraError(t, err); jerr.Kind != apperr.JiraRejected || MayExist(err) {
		t.Errorf("400: err = %v", err)
	}
}

func TestStatus(t *testing.T) {
	srv := newServer(t, answer(http.StatusOK, `{"id":"1","key":"PAY-12","fields":{"status":{"id":"4","name":"In Review"}}}`))
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	got, err := c.Status(context.Background(), "PAY-12")
	if err != nil || got != "In Review" {
		t.Fatalf("Status = %q, %v", got, err)
	}
	if req := srv.requests()[0]; req.Method != http.MethodGet || req.URL.Path != "/rest/api/2/issue/PAY-12" || req.URL.RawQuery != "fields=status" {
		t.Errorf("request = %s %s?%s", req.Method, req.URL.Path, req.URL.RawQuery)
	}

	for _, body := range []string{`{}`, `{"fields":{"status":{}}}`, `[]`} {
		bad := newServer(t, answer(http.StatusOK, body))
		c, _ = testClient(testOptions(bad.URL), basicCreds())
		if _, err := c.Status(context.Background(), "PAY-12"); asJiraError(t, err).Kind != apperr.JiraInvalidResponse {
			t.Errorf("%s: err = %v, want an invalid response", body, err)
		}
	}
}

// What a failure prints never holds the credentials, even when Jira echoes them.
func TestWriteErrorsNeverHoldCredentials(t *testing.T) {
	echo := func(w http.ResponseWriter, r *http.Request, _ int) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"echo":%q,"token":%q}`, r.Header.Get("Authorization"), testToken)
	}
	srv := newServer(t, echo)
	c, _ := testClient(testOptions(srv.URL), basicCreds())
	_, err := c.Comment(context.Background(), "PAY-12", "x")
	assertNoSecret(t, err.Error())
	_, err = c.Transitions(context.Background(), "PAY-12")
	assertNoSecret(t, err.Error())
	assertNoSecret(t, c.Transition(context.Background(), "PAY-12", "1").Error())
	_, err = c.Status(context.Background(), "PAY-12")
	assertNoSecret(t, err.Error())
}

func TestMergeRequestComment_Wiki(t *testing.T) {
	m := MergeRequestComment{
		IID: 12, URL: "https://gitlab.example.com/group/app/-/merge_requests/12",
		Title: "fix: PAY-12 Use + in Add", Review: "pass", Tests: "passed", Cost: "$0.84",
		RunID: "20261023-101500-ab12cd",
	}
	want := "Merge request [!12|https://gitlab.example.com/group/app/-/merge_requests/12]: fix: PAY\\-12 Use \\+ in Add\n" +
		"Review: pass · Tests: passed · Cost: $0.84 · Run 20261023\\-101500\\-ab12cd"
	if got := m.Wiki(); got != want {
		t.Errorf("Wiki =\n%s\nwant\n%s", got, want)
	}
	if got := (MergeRequestComment{IID: 3, URL: "https://h/-/merge_requests/3"}).Wiki(); got != "Merge request [!3|https://h/-/merge_requests/3]" {
		t.Errorf("a bare comment = %q", got)
	}
}

// A title written by a model cannot start markup, a mention or a link, and a URL cannot end its
// link early.
func TestMergeRequestComment_EscapesWhatIsNotOurs(t *testing.T) {
	m := MergeRequestComment{
		IID: 1, URL: "https://h/a]b|c{d}/-/merge_requests/1",
		Title: "[~admin] {code}\n*bold* !img.png! [x|http://evil]", Review: "pass]", RunID: "r1",
	}
	got := m.Wiki()
	if strings.Count(got, "\n") != 1 {
		t.Errorf("the title's line feed is kept:\n%s", got)
	}
	first, _, _ := strings.Cut(got, "\n")
	for _, raw := range []string{"[~admin]", "{code}", "*bold*", "!img.png!", "[x|http://evil]"} {
		if strings.Contains(first, raw) {
			t.Errorf("%q is not escaped in %q", raw, first)
		}
	}
	if !strings.HasPrefix(first, "Merge request [!1|https://h/a%5Db%7Cc%7Bd%7D/-/merge_requests/1]: ") {
		t.Errorf("link = %q", first)
	}
}
