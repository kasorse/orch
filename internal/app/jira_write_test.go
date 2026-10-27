package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"orchestrator/internal/config"
	"orchestrator/internal/domain"
)

const jiraWriteTestToken = "jira-test-token-0123456789"

// jiraIssueServer answers what the write-back asks of one issue, PAY-12, and keeps every request.
type jiraIssueServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []jiraRequest

	status      string
	transitions string
	// commentStatus and transitionStatus are what the two POSTs answer.
	commentStatus, transitionStatus int
}

type jiraRequest struct {
	method, path, query, auth, body string
}

const reviewTransitions = `{"transitions":[
  {"id":"11","name":"Start progress","to":{"name":"In Progress"}},
  {"id":"21","name":"Send to review","to":{"name":"In Review"}}]}`

func newJiraIssueServer(t *testing.T) *jiraIssueServer {
	t.Helper()
	f := &jiraIssueServer{status: "To Do", transitions: reviewTransitions, commentStatus: http.StatusCreated, transitionStatus: http.StatusNoContent}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, jiraRequest{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), string(body)})
		f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/PAY-12":
			fmt.Fprintf(w, `{"key":"PAY-12","fields":{"status":{"name":%q}}}`, f.status)
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/2/issue/PAY-12/transitions":
			fmt.Fprint(w, f.transitions)
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/2/issue/PAY-12/comment":
			w.WriteHeader(f.commentStatus)
			if f.commentStatus == http.StatusCreated {
				fmt.Fprint(w, `{"id":"10042"}`)
			} else {
				// Jira echoing the credentials back must not reach the output.
				fmt.Fprintf(w, `{"errorMessages":["boom"],"echo":%q,"token":%q}`, r.Header.Get("Authorization"), jiraWriteTestToken)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/2/issue/PAY-12/transitions":
			w.WriteHeader(f.transitionStatus)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *jiraIssueServer) requests() []jiraRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]jiraRequest(nil), f.reqs...)
}

// posts are the requests that wrote something.
func (f *jiraIssueServer) posts() []jiraRequest {
	var posts []jiraRequest
	for _, r := range f.requests() {
		if r.method == http.MethodPost {
			posts = append(posts, r)
		}
	}
	return posts
}

// jiraRun is a finished worktree run under --mode auto for the Jira issue PAY-12, with the
// write-back opts, whose Jira section points at f and holds no jira.write of its own: what is
// written comes from the run's options, as in a resume.
func jiraRun(t *testing.T, f *jiraIssueServer, opts domain.JiraWriteOptions) (*session, *bytes.Buffer) {
	t.Helper()
	s, _, out := finishedWorktreeRun(t, domain.ModeAuto, nil, nil)
	t.Setenv("TEST_JIRA_TOKEN", jiraWriteTestToken)
	s.cfg.Jira = &config.JiraConfig{BaseURL: f.URL, Auth: config.JiraAuthBearer, TokenEnv: "TEST_JIRA_TOKEN", Timeout: 2 * time.Second}
	s.run.Task.Source = &domain.TaskSource{Kind: domain.TaskSourceJira, Key: "PAY-12", URL: "https://jira.example.com/browse/PAY-12", FetchedAt: time.Now()}
	s.run.Options.JiraWrite = opts
	return s, out
}

// deliverJiraRun commits and delivers the run, and returns the saved run.
func deliverJiraRun(t *testing.T, s *session, out *bytes.Buffer) *domain.Run {
	t.Helper()
	ctx := context.Background()
	sha := offerCommit(ctx, s, false)
	if sha == "" {
		t.Fatalf("no commit:\n%s", out)
	}
	if err := finishWorktree(ctx, s, sha); err != nil {
		t.Fatalf("finishWorktree = %v\n%s", err, out)
	}
	saved, err := s.store.LoadRun(s.run.ID)
	if err != nil {
		t.Fatalf("the saved run does not load: %v", err)
	}
	if strings.Contains(out.String(), jiraWriteTestToken) {
		t.Errorf("the output holds the credential:\n%s", out)
	}
	return saved
}

// After the merge request, the issue gets a comment and the configured transition, found by its
// name; the credentials go in the header only.
func TestWriteJira_CommentAndTransitionByName(t *testing.T) {
	f := newJiraIssueServer(t)
	s, out := jiraRun(t, f, domain.JiraWriteOptions{Comment: true, Transition: "send TO review"})
	saved := deliverJiraRun(t, s, out)

	posts := f.posts()
	if len(posts) != 2 {
		t.Fatalf("%d writes, want a comment and a transition: %+v", len(posts), f.requests())
	}
	var comment struct{ Body string }
	if err := json.Unmarshal([]byte(posts[0].body), &comment); err != nil || posts[0].path != "/rest/api/2/issue/PAY-12/comment" {
		t.Fatalf("comment = %s %s (%v)", posts[0].path, posts[0].body, err)
	}
	// The title is the commit header, which starts with the key of a Jira task. The run has no
	// test or review step and no cost, so only the run is named after the link.
	wantComment := `Merge request [!1|https://gitlab.test/group/project/-/merge_requests/1]: PAY\-12: Add main` + "\n" +
		"Run " + strings.ReplaceAll(s.run.ID, "-", `\-`)
	if comment.Body != wantComment {
		t.Errorf("comment =\n%s\nwant\n%s", comment.Body, wantComment)
	}
	if posts[1].path != "/rest/api/2/issue/PAY-12/transitions" || posts[1].body != `{"transition":{"id":"21"}}` {
		t.Errorf("transition = %s %s", posts[1].path, posts[1].body)
	}
	for _, r := range f.requests() {
		if r.auth != "Bearer "+jiraWriteTestToken {
			t.Errorf("%s %s: Authorization = %q", r.method, r.path, r.auth)
		}
	}

	want := &domain.RunJiraWrite{
		Comment:    &domain.RunJiraComment{State: domain.JiraCommentDone, ID: "10042"},
		Transition: &domain.RunJiraTransition{Name: "send TO review", State: domain.JiraTransitionDone},
	}
	if saved.JiraWrite == nil || *saved.JiraWrite.Comment != *want.Comment || *saved.JiraWrite.Transition != *want.Transition {
		t.Errorf("jira_write = %+v, want %+v", saved.JiraWrite, want)
	}
	if saved.Options.JiraWrite.Transition != "send TO review" || !saved.Options.JiraWrite.Comment {
		t.Errorf("options.jira_write = %+v", saved.Options.JiraWrite)
	}
	if strings.Contains(out.String(), "Warning") {
		t.Errorf("a warning on a clean write-back:\n%s", out)
	}
}

// A transition is also found by the name of the status it leads to.
func TestWriteJira_TransitionByTargetStatus(t *testing.T) {
	f := newJiraIssueServer(t)
	s, out := jiraRun(t, f, domain.JiraWriteOptions{Transition: "in review"})
	saved := deliverJiraRun(t, s, out)

	posts := f.posts()
	if len(posts) != 1 || posts[0].body != `{"transition":{"id":"21"}}` {
		t.Fatalf("writes = %+v, want the transition 21 only", posts)
	}
	if saved.JiraWrite == nil || saved.JiraWrite.Comment != nil || saved.JiraWrite.Transition == nil || saved.JiraWrite.Transition.State != domain.JiraTransitionDone {
		t.Errorf("jira_write = %+v", saved.JiraWrite)
	}
}

// No transition like the configured one: a warning with the names the issue has, nothing posted,
// and the run is not failed.
func TestWriteJira_NoSuchTransitionWarnsWithTheNames(t *testing.T) {
	f := newJiraIssueServer(t)
	s, out := jiraRun(t, f, domain.JiraWriteOptions{Transition: "Ship it"})
	saved := deliverJiraRun(t, s, out)

	if want := `Warning: PAY-12 has no transition "Ship it" (available: Start progress, Send to review)`; !strings.Contains(out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
	if len(f.posts()) != 0 {
		t.Errorf("writes = %+v, want none", f.posts())
	}
	if tr := saved.JiraWrite.Transition; tr == nil || tr.State != domain.JiraTransitionMissing || tr.Name != "Ship it" {
		t.Errorf("jira_write = %+v", saved.JiraWrite)
	}
	if saved.Status != domain.RunCompleted || saved.MR.State != domain.MRCreated {
		t.Errorf("status = %s, mr = %+v: the run's result changed", saved.Status, saved.MR)
	}
}

func TestWriteJira_NoTransitionsAtAll(t *testing.T) {
	f := newJiraIssueServer(t)
	f.transitions = `{"transitions":[]}`
	s, out := jiraRun(t, f, domain.JiraWriteOptions{Transition: "Done"})
	deliverJiraRun(t, s, out)
	if want := `Warning: PAY-12 has no transition "Done" (available: none)`; !strings.Contains(out.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
}

// An issue already in the configured status is left alone: nothing is posted and the transitions
// are not even read.
func TestWriteJira_AlreadyInStatus(t *testing.T) {
	f := newJiraIssueServer(t)
	f.status = "In Review"
	s, out := jiraRun(t, f, domain.JiraWriteOptions{Transition: "in review"})
	saved := deliverJiraRun(t, s, out)

	if len(f.posts()) != 0 {
		t.Errorf("writes = %+v, want none", f.posts())
	}
	for _, r := range f.requests() {
		if strings.HasSuffix(r.path, "/transitions") {
			t.Errorf("the transitions were read: %+v", r)
		}
	}
	if tr := saved.JiraWrite.Transition; tr == nil || tr.State != domain.JiraTransitionUnchanged {
		t.Errorf("jira_write = %+v", saved.JiraWrite)
	}
}

// A 500 to the comment may have posted it: the intent stays, the comment is sent once, and the
// transition still happens. The credentials Jira echoed are not in the output.
func TestWriteJira_CommentServerErrorKeepsTheIntent(t *testing.T) {
	f := newJiraIssueServer(t)
	f.commentStatus = http.StatusInternalServerError
	s, out := jiraRun(t, f, domain.JiraWriteOptions{Comment: true, Transition: "In Review"})
	saved := deliverJiraRun(t, s, out)

	comments := 0
	for _, p := range f.posts() {
		if strings.HasSuffix(p.path, "/comment") {
			comments++
		}
	}
	if comments != 1 {
		t.Errorf("%d comment requests, want 1: it is not retried", comments)
	}
	if c := saved.JiraWrite.Comment; c == nil || c.State != domain.JiraCommentIntent || c.ID != "" {
		t.Errorf("comment = %+v, want the intent kept", saved.JiraWrite.Comment)
	}
	if tr := saved.JiraWrite.Transition; tr == nil || tr.State != domain.JiraTransitionDone {
		t.Errorf("transition = %+v, want done: the comment's failure does not stop it", saved.JiraWrite.Transition)
	}
	if !strings.Contains(out.String(), "Warning: the comment on PAY-12 may have been posted and is not repeated") {
		t.Errorf("no warning:\n%s", out)
	}
	if strings.Contains(out.String(), "boom") || strings.Contains(out.String(), "Bearer") {
		t.Errorf("the output holds Jira's answer body:\n%s", out)
	}
}

// A refused comment (here 403) posted nothing: it is failed, not an intent.
func TestWriteJira_RefusedCommentIsFailed(t *testing.T) {
	f := newJiraIssueServer(t)
	f.commentStatus = http.StatusForbidden
	s, out := jiraRun(t, f, domain.JiraWriteOptions{Comment: true})
	saved := deliverJiraRun(t, s, out)
	if c := saved.JiraWrite.Comment; c == nil || c.State != domain.JiraCommentFailed {
		t.Errorf("comment = %+v", saved.JiraWrite.Comment)
	}
	if !strings.Contains(out.String(), "Warning: cannot comment on PAY-12") {
		t.Errorf("no warning:\n%s", out)
	}
}

func TestWriteJira_FailedTransitionIsAWarning(t *testing.T) {
	f := newJiraIssueServer(t)
	f.transitionStatus = http.StatusInternalServerError
	s, out := jiraRun(t, f, domain.JiraWriteOptions{Transition: "In Review"})
	saved := deliverJiraRun(t, s, out)
	if tr := saved.JiraWrite.Transition; tr == nil || tr.State != domain.JiraTransitionFailed {
		t.Errorf("transition = %+v", saved.JiraWrite.Transition)
	}
	if n := len(f.posts()); n != 1 {
		t.Errorf("%d transition requests, want 1", n)
	}
	if !strings.Contains(out.String(), `Warning: PAY-12 may have been moved by "Send to review" and is not moved again`) {
		t.Errorf("no warning:\n%s", out)
	}
}

// Without a merge request nothing is written to Jira: not when the push made none, not when it
// failed, not when the chain stopped before it.
func TestWriteJira_NothingWithoutAMergeRequest(t *testing.T) {
	cases := map[string]*domain.RunMR{
		"no record":     nil,
		"skipped":       {Skipped: "declined at commit"},
		"declined":      {State: domain.MRPushed, Remote: "origin", Source: "b", Target: "main", Skipped: "declined at merge request"},
		"pushed, no MR": {State: domain.MRPushed, Remote: "origin", Source: "b", Target: "main", Error: "the remote reported no merge request"},
		"push failed":   {State: domain.MRIntent, Remote: "origin", Source: "b", Target: "main", Error: "rejected"},
	}
	for name, mr := range cases {
		t.Run(name, func(t *testing.T) {
			f := newJiraIssueServer(t)
			s, out := jiraRun(t, f, domain.JiraWriteOptions{Comment: true, Transition: "In Review"})
			s.run.MR = mr
			writeJira(context.Background(), s, "")
			if len(f.requests()) != 0 || s.run.JiraWrite != nil || out.Len() != 0 {
				t.Errorf("requests = %+v, jira_write = %+v, output %q; want nothing", f.requests(), s.run.JiraWrite, out)
			}
		})
	}
}

// A typed task has no key, and a run that asked for nothing writes nothing.
func TestWriteJira_NothingWithoutAKeyOrAWish(t *testing.T) {
	f := newJiraIssueServer(t)
	created := &domain.RunMR{State: domain.MRCreated, Remote: "origin", Source: "b", Target: "main", IID: 1, URL: "https://gitlab.test/g/p/-/merge_requests/1"}

	typed, _ := jiraRun(t, f, domain.JiraWriteOptions{Comment: true, Transition: "In Review"})
	typed.run.Task.Source, typed.run.MR = nil, created
	writeJira(context.Background(), typed, "")

	silent, _ := jiraRun(t, f, domain.JiraWriteOptions{})
	silent.run.MR = created
	writeJira(context.Background(), silent, "")

	if len(f.requests()) != 0 || typed.run.JiraWrite != nil || silent.run.JiraWrite != nil {
		t.Errorf("requests = %+v, jira_write = %+v / %+v; want nothing", f.requests(), typed.run.JiraWrite, silent.run.JiraWrite)
	}
}

// A resume that finds no credentials writes nothing and says so, naming the variable and never
// its value.
func TestWriteJira_MissingCredentialsWarn(t *testing.T) {
	f := newJiraIssueServer(t)
	s, out := jiraRun(t, f, domain.JiraWriteOptions{Comment: true})
	t.Setenv("TEST_JIRA_TOKEN", "")
	saved := deliverJiraRun(t, s, out)
	if len(f.requests()) != 0 || saved.JiraWrite != nil {
		t.Errorf("requests = %+v, jira_write = %+v; want nothing", f.requests(), saved.JiraWrite)
	}
	if !strings.Contains(out.String(), "Warning: nothing was written to PAY-12") || !strings.Contains(out.String(), "TEST_JIRA_TOKEN") {
		t.Errorf("no warning naming the variable:\n%s", out)
	}
}

func TestWriteJira_NoJiraSectionWarns(t *testing.T) {
	f := newJiraIssueServer(t)
	s, out := jiraRun(t, f, domain.JiraWriteOptions{Comment: true})
	s.cfg.Jira = nil
	deliverJiraRun(t, s, out)
	if len(f.requests()) != 0 || !strings.Contains(out.String(), "Warning: nothing was written to PAY-12: the configuration has no jira section") {
		t.Errorf("requests = %+v, output:\n%s", f.requests(), out)
	}
}

func TestRunOptions_JiraWrite(t *testing.T) {
	cfg := config.Config{Jira: &config.JiraConfig{Write: &config.JiraWriteConfig{Comment: true, Transition: "In Review"}}}
	if got, want := runOptions(cfg).JiraWrite, (domain.JiraWriteOptions{Comment: true, Transition: "In Review"}); got != want {
		t.Errorf("runOptions.JiraWrite = %+v, want %+v", got, want)
	}
	for name, c := range map[string]config.Config{
		"no jira section": {},
		"no write":        {Jira: &config.JiraConfig{}},
		"nothing asked":   {Jira: &config.JiraConfig{Write: &config.JiraWriteConfig{}}},
	} {
		if got := runOptions(c).JiraWrite; got != (domain.JiraWriteOptions{}) {
			t.Errorf("%s: JiraWrite = %+v, want none", name, got)
		}
	}
}
