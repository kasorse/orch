package integration

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The credentials the fake Jira accepts; startJira puts them in JIRA_EMAIL and
// JIRA_API_TOKEN.
const (
	jiraEmail = "operator@example.com"
	jiraToken = "test-jira-token"
)

// defaultJiraTransitions is what the fake Jira lists as the moves an issue can make.
const defaultJiraTransitions = `{"transitions":[` +
	`{"id":"21","name":"Start progress","to":{"name":"In Progress"}},` +
	`{"id":"31","name":"Send to review","to":{"name":"In Review"}}]}`

// jiraRequest is what the fake Jira recorded of one request.
type jiraRequest struct {
	Path, Query, Auth string
	// Method and Body are set for a write (a POST: a creation, a comment, a transition; a PUT: a
	// revise run's description).
	Method, Body string
}

// fakeJira serves canned REST v2 issues by key on a loopback httptest server
// (specs/jira/SPEC.md §16). It checks Basic auth against the credentials above. It also takes
// the writes of specs/delivery/SPEC.md §10: the status read, the transitions list, a comment
// (201 with an id) and a transition (204).
type fakeJira struct {
	srv    *httptest.Server
	issues map[string]string
	// fail lists statuses to answer the first requests with, before answering normally.
	fail []int
	// status is the status every issue is in for the write-back's read, "To Do" unless a test sets
	// it; transitions is the answer to GET .../transitions (defaultJiraTransitions).
	status      string
	transitions string

	mu      sync.Mutex
	reqs    []jiraRequest
	created int
}

// startJira runs a fake Jira serving issues (key -> JSON body) and sets the credential
// variables to what it accepts. A key it does not know answers 404.
func startJira(t *testing.T, issues map[string]string, fail ...int) *fakeJira {
	t.Helper()
	j := &fakeJira{issues: issues, fail: fail, status: "To Do", transitions: defaultJiraTransitions}
	j.srv = httptest.NewServer(http.HandlerFunc(j.handle))
	t.Cleanup(j.srv.Close)
	t.Setenv("JIRA_EMAIL", jiraEmail)
	t.Setenv("JIRA_API_TOKEN", jiraToken)
	return j
}

func (j *fakeJira) URL() string { return j.srv.URL }

// Requests returns every request received, in order.
func (j *fakeJira) Requests() []jiraRequest {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]jiraRequest(nil), j.reqs...)
}

func (j *fakeJira) handle(w http.ResponseWriter, r *http.Request) {
	req := jiraRequest{Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization")}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		body, _ := io.ReadAll(r.Body)
		req.Method, req.Body = r.Method, string(body)
	}
	j.mu.Lock()
	j.reqs = append(j.reqs, req)
	n := len(j.reqs)
	created := 0
	if req.Method == http.MethodPost && r.URL.Path == "/rest/api/2/issue" {
		j.created++
		created = j.created
	}
	j.mu.Unlock()

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(jiraEmail+":"+jiraToken))
	switch {
	case r.Header.Get("Authorization") != want:
		w.WriteHeader(http.StatusUnauthorized)
		return
	case n <= len(j.fail):
		w.WriteHeader(j.fail[n-1])
		return
	case req.Method == http.MethodPost && r.URL.Path == "/rest/api/2/issue":
		// A creation: the next key of the project the request names, 201 as Jira answers.
		var in struct {
			Fields struct {
				Project struct {
					Key string `json:"key"`
				} `json:"project"`
			} `json:"fields"`
		}
		_ = json.Unmarshal([]byte(req.Body), &in)
		key := fmt.Sprintf("%s-%d", in.Fields.Project.Key, 100+created)
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"id":"%d","key":%q,"self":"https://ignored"}`, 20000+created, key)
		return
	case req.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comment"):
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"10042"}`))
		return
	case req.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/transitions"):
		w.WriteHeader(http.StatusNoContent)
		return
	case req.Method == http.MethodPut:
		w.WriteHeader(http.StatusNoContent)
		return
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/transitions"):
		_, _ = w.Write([]byte(j.transitions))
		return
	case r.Method == http.MethodGet && r.URL.RawQuery == "fields=status":
		key := strings.TrimPrefix(r.URL.Path, "/rest/api/2/issue/")
		_, _ = fmt.Fprintf(w, `{"key":%q,"fields":{"status":{"name":%q}}}`, key, j.status)
		return
	}
	body, ok := j.issues[strings.TrimPrefix(r.URL.Path, "/rest/api/2/issue/")]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`))
		return
	}
	_, _ = w.Write([]byte(body))
}

// jiraIssue is a v2 issue body; extra is merged into its fields.
func jiraIssue(t *testing.T, key, summary, description string, extra map[string]any) string {
	t.Helper()
	fields := map[string]any{"summary": summary, "description": description, "issuetype": map[string]string{"name": "Bug"}}
	for k, v := range extra {
		fields[k] = v
	}
	data, err := json.Marshal(map[string]any{"id": "10001", "self": "https://ignored", "key": key, "fields": fields})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// unsetenv removes name for the rest of the test, restoring it afterwards.
func unsetenv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

// assertNoRuns fails when a run directory was created under repoDir.
func assertNoRuns(t *testing.T, repoDir string) {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(repoDir, ".orchestrator", "runs", "*"))
	if err != nil || len(dirs) != 0 {
		t.Errorf("expected no run directory, got %v (err=%v)", dirs, err)
	}
}
