package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/app"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// The fakes of the Jira listener scenarios (specs/jira-listener/SPEC.md, TASK-JL05): a Jira with
// a search, comments, transitions and a status, a "GitLab" that is a local bare repository, and a
// listener that runs its tickets in this process (specs/embed/SPEC.md §2): TestMain answers only
// for the two execs that remain, mcp-verify and git's credential helper.

const (
	listenBot      = "bot-1"
	listenReporter = "rep-1"
	listenPerson   = "person-1"
)

type listenComment struct {
	id, author, body string
	created          time.Time
}

type listenIssue struct {
	key, summary, description, status, issueType, reporter string
	labels, components                                     []string
	comments                                               []listenComment
}

// listenJira is a Jira Cloud with the endpoints the listener uses. Its search returns every issue
// whatever the JQL says: the listener must re-check in Go what the query filtered.
type listenJira struct {
	srv *httptest.Server

	mu     sync.Mutex
	issues map[string]*listenIssue
	order  []string
	reqs   []jiraRequest
	jqls   []string
	nextID int
	// noStart takes the claiming transition out of what the queue's status offers.
	noStart bool
	// afterComments runs when the comments of an issue are read, without the lock held: a person
	// acting in the middle of the listener's work.
	afterComments func(key string)
	// beforeTransition runs when a transition is posted, before it is made, without the lock held:
	// another listener's claim landing between the listener's GET and its POST.
	beforeTransition func(key, id string)
}

// listenTransitions is what each status offers: the ids are the fake's own. The queue offers the
// claim, and blocked and failed for a ticket the listener ends without a claim (spent, an Epic).
var listenTransitions = map[string][]struct{ id, name, to string }{
	"To Do": {{"11", "Start progress", "In Progress"}, {"21", "Block", "Blocked"}, {"22", "Fail", "Failed"}},
	"In Progress": {
		{"21", "Block", "Blocked"}, {"22", "Fail", "Failed"}, {"23", "Review", "Ready to Review"}, {"24", "Back to queue", "To Do"},
	},
	"Blocked": {{"31", "Reopen", "To Do"}},
}

func startListenJira(t *testing.T) *listenJira {
	t.Helper()
	j := &listenJira{issues: map[string]*listenIssue{}, nextID: 10000}
	j.srv = httptest.NewServer(http.HandlerFunc(j.handle))
	t.Cleanup(j.srv.Close)
	t.Setenv("JIRA_EMAIL", jiraEmail)
	t.Setenv("JIRA_API_TOKEN", jiraToken)
	return j
}

func (j *listenJira) URL() string { return j.srv.URL }

// add puts an issue in the queue: status To Do, the label ai-run, reported by the allowed reporter.
func (j *listenJira) add(key, summary, description string, labels ...string) *listenIssue {
	j.mu.Lock()
	defer j.mu.Unlock()
	is := &listenIssue{key: key, summary: summary, description: description, status: "To Do", issueType: "Bug",
		reporter: listenReporter, labels: append([]string{"ai-run"}, labels...)}
	j.issues[key] = is
	j.order = append(j.order, key)
	return is
}

func (j *listenJira) setStatus(key, status string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.issues[key].status = status
}

// edit runs f on the issue key, as a person editing it in Jira.
func (j *listenJira) edit(key string, f func(*listenIssue)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	f(j.issues[key])
}

func (j *listenJira) labelsOf(key string) []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.issues[key].labels)
}

func (j *listenJira) statusOf(key string) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.issues[key].status
}

// person comments as a person, a minute after the last comment.
func (j *listenJira) person(key, body string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	is := j.issues[key]
	j.nextID++
	is.comments = append(is.comments, listenComment{id: strconv.Itoa(j.nextID), author: listenPerson, body: body,
		created: time.Date(2026, 10, 29, 11, 40, len(is.comments), 0, time.UTC)})
}

// botComments are the bodies of the comments the listener posted on key.
func (j *listenJira) botComments(key string) []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []string
	for _, c := range j.issues[key].comments {
		if c.author == listenBot {
			out = append(out, c.body)
		}
	}
	return out
}

// posts are the POSTs received whose path ends with suffix.
func (j *listenJira) posts(suffix string) []jiraRequest {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []jiraRequest
	for _, r := range j.reqs {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, suffix) {
			out = append(out, r)
		}
	}
	return out
}

func (j *listenJira) requestCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.reqs)
}

func (j *listenJira) searches() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.jqls...)
}

func (j *listenJira) handle(w http.ResponseWriter, r *http.Request) {
	req := jiraRequest{Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization")}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		body, _ := io.ReadAll(r.Body)
		req.Method, req.Body = r.Method, string(body)
	}
	j.mu.Lock()
	j.reqs = append(j.reqs, req)
	j.mu.Unlock()

	if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(jiraEmail+":"+jiraToken)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	writeJSON := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case r.URL.Path == "/rest/api/2/myself":
		writeJSON(map[string]string{"accountId": listenBot})
		return
	case r.URL.Path == "/rest/api/2/search/jql":
		j.mu.Lock()
		j.jqls = append(j.jqls, r.URL.Query().Get("jql"))
		issues := []any{}
		for _, k := range j.order {
			issues = append(issues, j.issueJSON(j.issues[k], false))
		}
		j.mu.Unlock()
		writeJSON(map[string]any{"issues": issues, "isLast": true})
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/rest/api/2/issue/")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	key, sub, _ := strings.Cut(rest, "/")
	j.mu.Lock()
	is := j.issues[key]
	j.mu.Unlock()
	if is == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch {
	case sub == "" && r.Method == http.MethodGet && r.URL.RawQuery == "fields=status":
		j.mu.Lock()
		status := is.status
		j.mu.Unlock()
		writeJSON(map[string]any{"key": key, "fields": map[string]any{"status": map[string]string{"name": status}}})
	case sub == "" && r.Method == http.MethodPut:
		var in struct {
			Update struct {
				Labels []struct {
					Remove string `json:"remove"`
				} `json:"labels"`
			} `json:"update"`
		}
		if err := json.Unmarshal([]byte(req.Body), &in); err != nil || len(in.Update.Labels) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		j.mu.Lock()
		for _, l := range in.Update.Labels {
			is.labels = slices.DeleteFunc(is.labels, func(have string) bool { return have == l.Remove })
		}
		j.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case sub == "" && r.Method == http.MethodGet:
		j.mu.Lock()
		v := j.issueJSON(is, true)
		j.mu.Unlock()
		writeJSON(v)
	case sub == "transitions" && r.Method == http.MethodGet:
		j.mu.Lock()
		list := []any{}
		for _, t := range listenTransitions[is.status] {
			if j.noStart && t.id == "11" {
				continue
			}
			list = append(list, map[string]any{"id": t.id, "name": t.name, "to": map[string]string{"name": t.to}})
		}
		j.mu.Unlock()
		writeJSON(map[string]any{"transitions": list})
	case sub == "transitions" && r.Method == http.MethodPost:
		var in struct {
			Transition struct {
				ID string `json:"id"`
			} `json:"transition"`
		}
		_ = json.Unmarshal([]byte(req.Body), &in)
		j.mu.Lock()
		before := j.beforeTransition
		j.mu.Unlock()
		if before != nil {
			before(key, in.Transition.ID)
		}
		j.mu.Lock()
		moved := false
		for _, t := range listenTransitions[is.status] {
			if t.id == in.Transition.ID && !(j.noStart && t.id == "11") {
				is.status, moved = t.to, true
				break
			}
		}
		j.mu.Unlock()
		if !moved {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errorMessages":["Transition is not valid for this issue."]}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case sub == "comment" && r.Method == http.MethodGet:
		j.mu.Lock()
		list := []any{}
		for _, c := range is.comments {
			list = append(list, map[string]any{"id": c.id, "author": map[string]string{"accountId": c.author},
				"created": c.created.Format("2006-01-02T15:04:05.000-0700"), "body": c.body})
		}
		after := j.afterComments
		j.mu.Unlock()
		writeJSON(map[string]any{"comments": list, "total": len(list)})
		if after != nil {
			after(key)
		}
	case sub == "comment" && r.Method == http.MethodPost:
		var in struct {
			Body string `json:"body"`
		}
		_ = json.Unmarshal([]byte(req.Body), &in)
		j.mu.Lock()
		j.nextID++
		id := strconv.Itoa(j.nextID)
		is.comments = append(is.comments, listenComment{id: id, author: listenBot, body: in.Body,
			created: time.Date(2026, 10, 29, 12, 0, len(is.comments), 0, time.UTC)})
		j.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"id":%q}`, id)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// issueJSON is the issue as Jira answers; the caller holds the lock.
func (j *listenJira) issueJSON(is *listenIssue, full bool) map[string]any {
	components := []any{}
	for _, c := range is.components {
		components = append(components, map[string]string{"name": c})
	}
	fields := map[string]any{
		"summary": is.summary, "issuetype": map[string]string{"name": is.issueType},
		"status": map[string]string{"name": is.status}, "labels": is.labels, "components": components,
		"reporter": map[string]string{"accountId": is.reporter},
	}
	if full {
		fields["description"] = is.description
	}
	return map[string]any{"id": "10001", "self": "https://ignored", "key": is.key, "fields": fields}
}

// syncBuffer is a writer the listener and the test can use at once.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// listenEnv is a listener's working directory with one service, billing, a clone of a bare
// repository that plays GitLab.
type listenEnv struct {
	t              *testing.T
	wd, svc, bare  string
	cfg            string // the listener's config
	childCfg       string // the config of the service, which has none of its own
	jira           *listenJira
	invocationsLog string
	// maxAttempts is listener.max_attempts.
	maxAttempts int
}

type listenOptions struct {
	// pPre and rechecks script the clarity checks of the fake claude (see clarifyEnv).
	pPre      float64
	rechecks  []float64
	attempts  int
	childOpts []func(*configOptions)
}

func newListenEnv(t *testing.T, o listenOptions) *listenEnv {
	t.Helper()
	t.Setenv("GITLAB_TOKEN", "glpat-integration-token")
	repoDir, invocationsLog, _, _ := clarifyEnv(t, o.pPre, o.rechecks...)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("FAKE_FIX_TARGET_FILE", "fixture.go")
	bare := fakeexec.AddGitLabRemote(t, repoDir)

	// The bare repository holds the default branch, as a GitLab project does.
	branch, err := gitOutput(repoDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, repoDir, "push", "-q", "origin", branch)

	// The service is a clone of it in the services directory, which is the listener's working
	// directory; its origin names the project on the fake GitLab's host.
	wd := t.TempDir()
	gitRun(t, wd, "clone", "-q", bare, "billing")
	svc := filepath.Join(wd, "billing")
	gitRun(t, svc, "config", "user.email", "fixture@example.com")
	gitRun(t, svc, "config", "user.name", "Fixture")
	gitRun(t, svc, "remote", "set-url", "origin", "file://"+fakeexec.GitLabHost+bare)

	j := startListenJira(t)
	childCfg := clarifyConfig(t, append([]func(*configOptions){func(c *configOptions) { c.isolation = isolationAbsent }}, o.childOpts...)...)
	attempts := o.attempts
	if attempts == 0 {
		attempts = 2
	}
	cfg := clarifyConfig(t, func(c *configOptions) {
		c.isolation = isolationAbsent
		c.jira = &jiraOptions{baseURL: j.URL()}
		c.extraYAML = fmt.Sprintf(`
listener:
  poll_interval: 30s
  services_dir: %s
  default_config: %s
  max_attempts: %d
  clone_timeout: 1m
  query:
    projects: [PAY]
    label: ai-run
    statuses: ["To Do"]
    reporters: [%s]
  transitions:
    start: In Progress
    blocked: Blocked
    failed: Failed
    done: Ready to Review
    retry: To Do
gitlab:
  base_url: https://gitlab.test
  token_env: GITLAB_TOKEN
  allowed_groups: [group]
`, wd, childCfg, attempts, listenReporter)
	})
	return &listenEnv{t: t, wd: wd, svc: svc, bare: bare, cfg: cfg, childCfg: childCfg, jira: j,
		invocationsLog: invocationsLog, maxAttempts: attempts}
}

// listenerEnvNames are the variables the listener changes in its own process (specs/embed/SPEC.md
// §2.4) besides the Jira ones, which startListener sets again: git's credential helper entries.
var listenerEnvNames = []string{"GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_KEY_1", "GIT_CONFIG_VALUE_1"}

// setJiraEnv sets the Jira variables the fake accepts: a listener removes them from its process
// once its client is built, and the next one this test process starts needs them again. The test's
// t.Setenv restores the original values.
func (e *listenEnv) setJiraEnv() {
	e.t.Helper()
	e.t.Setenv("JIRA_EMAIL", jiraEmail)
	e.t.Setenv("JIRA_API_TOKEN", jiraToken)
}

// resetListenerProcessEnv puts the process's environment where a listener finds it at its start: the
// Jira variables set and no helper entries of an earlier listener of this test process.
func (e *listenEnv) resetListenerProcessEnv() {
	e.t.Helper()
	e.setJiraEnv()
	for _, name := range listenerEnvNames {
		e.t.Setenv(name, "")
		_ = os.Unsetenv(name)
	}
}

// startListener runs `orchestrator jira listen args...` in the working directory, in the
// background. wait stops it (when cancel is not nil it is called first) and returns what it returned.
// The tickets it takes run in this process, in the services' directories, while the process's working
// directory is the listener's.
func (e *listenEnv) startListener(ctx context.Context, stderr io.Writer, args ...string) (wait func() error) {
	e.t.Helper()
	e.resetListenerProcessEnv()
	orig, err := os.Getwd()
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.Chdir(e.wd); err != nil {
		e.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		full := append([]string{"--config", e.cfg, "jira", "listen"}, args...)
		done <- app.Run(ctx, full, strings.NewReader(""), io.Discard, stderr)
	}()
	var once sync.Once
	var result error
	wait = func() error {
		once.Do(func() {
			result = <-done
			if err := os.Chdir(orig); err != nil {
				e.t.Error(err)
			}
		})
		return result
	}
	// A test that fails with the listener still running must not leave the process in a directory
	// that is about to be removed.
	e.t.Cleanup(func() {
		cancel()
		_ = wait()
	})
	return wait
}

// listenOnce runs one poll and the ticket it takes, and returns what the listener said on stderr.
func (e *listenEnv) listenOnce() (string, error) {
	e.t.Helper()
	var stderr syncBuffer
	err := e.startListener(context.Background(), &stderr, "--once")()
	return stderr.String(), err
}

// listenerState is the part of state.json the scenarios read.
type listenerState struct {
	V       int                          `json:"v"`
	Tickets map[string]listenerStateItem `json:"tickets"`
}

type listenerStateItem struct {
	Attempts   int    `json:"attempts"`
	Retries    int    `json:"retries"`
	Service    string `json:"service"`
	Phase      string `json:"phase"`
	RunID      string `json:"run_id"`
	RunDir     string `json:"run_dir"`
	TicketHash string `json:"ticket_hash"`
	Outcome    string `json:"outcome"`
	NextTryAt  string `json:"next_try_at"`
	Writes     struct {
		Comment    struct{ State, ID string } `json:"comment"`
		Transition struct{ State string }     `json:"transition"`
	} `json:"writes"`
}

func (e *listenEnv) state() listenerState {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.wd, ".orchestrator", "listener", "state.json"))
	if err != nil {
		e.t.Fatalf("state.json: %v", err)
	}
	var st listenerState
	if err := json.Unmarshal(data, &st); err != nil {
		e.t.Fatalf("state.json: %v\n%s", err, data)
	}
	return st
}

// runs are the run.json files of the service's runs, oldest first.
func (e *listenEnv) runs() []listenerRun {
	e.t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(e.svc, ".orchestrator", "runs", "*", "run.json"))
	var out []listenerRun
	for _, f := range dirs {
		data, err := os.ReadFile(f)
		if err != nil {
			e.t.Fatal(err)
		}
		var r listenerRun
		if err := json.Unmarshal(data, &r); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	for i := range out {
		for k := i + 1; k < len(out); k++ {
			if out[k].CreatedAt < out[i].CreatedAt {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	return out
}

type listenerRunAuth struct {
	Requested string `json:"requested"`
	DecidedBy string `json:"decided_by"`
}

type listenerRun struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
	CreatedAt     string `json:"created_at"`
	Task          struct {
		Description string `json:"description"`
	} `json:"task"`
	MR *struct {
		State string `json:"state"`
		IID   int    `json:"iid"`
		URL   string `json:"url"`
	} `json:"mr"`
	ClaudeAuth *listenerRunAuth `json:"claude_auth"`
	CodexAuth  *listenerRunAuth `json:"codex_auth"`
}
