package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kasorse/orch/internal/app"
)

// An Epic from its label to its children's merge requests (specs/listener-story/SPEC.md §11, LS07):
// the listener plans the Epic labelled ai-run with a ref: label, creates three children in two
// services and links the two of one service, then runs the children like any ticket, the two
// services' first children at once and the second child of a service once its blocker is Done.
//
// GitLab is one local HTTP server on 127.0.0.1 (gitlab.base_url may be plain http there): the groups'
// projects over the API, and every project's repository over git's smart HTTP (git http-backend), so
// the listener clones, fetches and pushes to the very URLs its labels and checks name.

// gitLabHTTP is that GitLab: bare repositories under root, one per project path.
type gitLabHTTP struct {
	srv      *httptest.Server
	root     string
	projects []string
	branch   string

	mu       sync.Mutex
	apiPaths []string
}

// gitLabHTTPHook is the post-receive hook of each project: it plays GitLab's answer to a push that
// opens a merge request, on the server's host, numbering the merge requests of the project.
const gitLabHTTPHook = `#!/bin/sh
i=0
create=
while [ "$i" -lt "${GIT_PUSH_OPTION_COUNT:-0}" ]; do
  eval "opt=\$GIT_PUSH_OPTION_$i"
  [ "$opt" = "merge_request.create" ] && create=1
  i=$((i + 1))
done
[ -n "$create" ] || exit 0
n=$(cat merge-requests.count 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > merge-requests.count
while read -r old new ref; do
  echo "" >&2
  echo "View merge request for ${ref#refs/heads/}:" >&2
  echo "  https://127.0.0.1/%s/-/merge_requests/$n" >&2
  echo "" >&2
done
`

// startGitLabHTTP serves a copy of the repository fixture as each of projects.
func startGitLabHTTP(t *testing.T, fixture string, projects ...string) *gitLabHTTP {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git: %v", err)
	}
	branch, err := gitOutput(fixture, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	g := &gitLabHTTP{root: t.TempDir(), projects: projects, branch: branch}
	for _, p := range projects {
		bare := g.bare(p)
		if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
			t.Fatal(err)
		}
		gitRun(t, g.root, "clone", "-q", "--bare", fixture, bare)
		gitRun(t, bare, "config", "http.receivepack", "true")
		gitRun(t, bare, "config", "receive.advertisePushOptions", "true")
		hook := filepath.Join(bare, "hooks", "post-receive")
		if err := os.WriteFile(hook, []byte(fmt.Sprintf(gitLabHTTPHook, p)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	backend := &cgi.Handler{Path: git, Args: []string{"http-backend"}, Env: []string{
		"GIT_PROJECT_ROOT=" + g.root, "GIT_HTTP_EXPORT_ALL=1", "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1",
	}}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v4/") {
			g.api(w, r)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *gitLabHTTP) bare(project string) string {
	return filepath.Join(g.root, filepath.FromSlash(project)+".git")
}

// api answers the projects of the group "group" (one page), and nothing else.
func (g *gitLabHTTP) api(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.apiPaths = append(g.apiPaths, r.URL.Path)
	g.mu.Unlock()
	if r.Method != http.MethodGet || r.URL.Path != "/api/v4/groups/group/projects" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	list := []map[string]any{}
	for i, p := range g.projects {
		list = append(list, map[string]any{"id": i + 1, "path_with_namespace": p, "name": filepath.Base(p),
			"description": "The " + filepath.Base(p) + " service", "default_branch": g.branch, "archived": false})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (g *gitLabHTTP) asked() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.apiPaths)
}

// mergeRequests is how many merge requests pushes to project opened.
func (g *gitLabHTTP) mergeRequests(project string) string {
	data, err := os.ReadFile(filepath.Join(g.bare(project), "merge-requests.count"))
	if err != nil {
		return "0"
	}
	return strings.TrimSpace(string(data))
}

const (
	epicSelectResponse = `{"services":[{"path":"group/billing","why":"the dictionary lives here"},{"path":"group/shipping","why":"the editor shows it"}]}`
	epicSurveyResponse = `{"summary":"Add is in fixture.go.","relevant_files":["fixture.go"],"interfaces":[],"constraints":[]}`
	// epicRequirementsResponse is what the plan understood, with no question.
	epicRequirementsResponse = `{"summary":"A categories dictionary, shown in the editor.",` +
		`"requirements":["Billing keeps the categories of catalog.","Shipping shows the category of each item."],"questions":[]}`
	// epicAskingResponse is what the plan understood when something the Epic needs is left open.
	epicAskingResponse = `{"summary":"A categories dictionary, shown in the editor.",` +
		`"requirements":["Billing keeps the categories of catalog."],"questions":["Which countries get the categories"]}`
	// epicSpecifyResponse is three tasks: two of billing, the second needing the first, and one of shipping.
	epicSpecifyResponse = `{"slug":"digital-categories","prefix":"DC","title":"Digital categories",` +
		`"spec":{"problem":"p","approach":"a","contracts":"c","out_of_scope":"","risks":""},"tasks":[` +
		`{"id":"DC01","repo":"billing","title":"The dictionary","size":"M","depends_on":[],"goal":"fix Add in fixture.go so that it adds","acceptance":["go test passes"],"notes":""},` +
		`{"id":"DC02","repo":"shipping","title":"The editor","size":"M","depends_on":[],"goal":"fix Add in fixture.go so that it adds","acceptance":["go test passes"],"notes":""},` +
		`{"id":"DC03","repo":"billing","title":"The country attributes","size":"S","depends_on":["DC01"],"goal":"fix Add in fixture.go so that it adds","acceptance":["go test passes"],"notes":""}]}`
)

// newEpicListenEnv is a listener with listener.story and two slots, an empty services directory, the
// GitLab above with the projects group/billing, group/shipping and group/catalog, and the Epic PAY-1
// labelled ai-run and ref:group/catalog.
func newEpicListenEnv(t *testing.T) (*listenEnv, *gitLabHTTP) {
	t.Helper()
	t.Setenv("GITLAB_TOKEN", "glpat-integration-token")
	t.Setenv("GITLAB_READ_TOKEN", "glpat-integration-read-token")
	// The services are cloned by the listener: their commits take the identity from the environment.
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "Fixture"}, {"GIT_AUTHOR_EMAIL", "fixture@example.com"},
		{"GIT_COMMITTER_NAME", "Fixture"}, {"GIT_COMMITTER_EMAIL", "fixture@example.com"}} {
		t.Setenv(kv[0], kv[1])
	}
	repoDir, invocationsLog, _, _ := clarifyEnv(t, 0.85)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("FAKE_FIX_TARGET_FILE", "fixture.go")
	responses := os.Getenv("FAKE_RESPONSES_DIR")
	writeResponse(t, responses, "select", epicSelectResponse)
	writeResponse(t, responses, "survey", epicSurveyResponse)
	writeResponse(t, responses, "requirements", epicRequirementsResponse)
	writeResponse(t, responses, "specify", epicSpecifyResponse)

	gl := startGitLabHTTP(t, repoDir, "group/billing", "group/shipping", "group/catalog")
	wd := t.TempDir()
	j := startListenJira(t)
	childCfg := clarifyConfig(t, func(c *configOptions) { c.isolation = isolationAbsent })
	cfg := clarifyConfig(t, func(c *configOptions) {
		c.isolation = isolationAbsent
		c.jira = &jiraOptions{baseURL: j.URL(), create: "  create:\n    issue_type: Task\n    link: parent\n"}
		c.extraYAML = fmt.Sprintf(`
listener:
  poll_interval: 30s
  services_dir: %s
  default_config: %s
  max_attempts: 2
  clone_timeout: 1m
  max_parallel: 2
  query:
    projects: [PAY]
    label: ai-run
    statuses: ["To Do"]
    reporters: [%s, %s]
  transitions:
    start: In Progress
    blocked: Blocked
    failed: Failed
    done: Ready to Review
    retry: To Do
  blockers:
    link_type: Blocks
    done_statuses: [Done]
  story:
    max_services: 3
gitlab:
  base_url: %s
  token_env: GITLAB_TOKEN
  api_token_env: GITLAB_READ_TOKEN
  allowed_groups: [group]
`, wd, childCfg, listenReporter, listenBot, gl.srv.URL)
	})
	j.add("PAY-1", "Digital categories migration",
		"Add a categories dictionary to billing and show it in shipping, as catalog does.", "ref:group/catalog").issueType = "Epic"
	env := &listenEnv{t: t, wd: wd, cfg: cfg, childCfg: childCfg, jira: j, invocationsLog: invocationsLog, maxAttempts: 2}
	return env, gl
}

func TestListen_AnEpicBecomesChildrenThatRunInParallelAndInTheirOrder(t *testing.T) {
	env, gl := newEpicListenEnv(t)
	// "Merged": the fake moves the first child of billing to Done as soon as the listener has delivered it.
	env.jira.afterTransition = func(key, status string) {
		if key == "PAY-2" && status == "Ready to Review" {
			env.jira.setStatus(key, "Done")
		}
	}
	barrier := newRunBarrier(2)
	defer app.SetListenRunHook(barrier.hook)()
	defer barrier.open()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var stderr syncBuffer
	wait := env.startListener(ctx, &stderr)

	// The plan: the Epic's three children, then the first of each service in progress at once.
	if keys := barrier.waitReached(t); !slices.Equal(keys, []string{"PAY-2", "PAY-3"}) {
		t.Fatalf("held runs = %v, want the first child of each service\nstderr:\n%s", keys, stderr.String())
	}
	for key, service := range map[string]string{"PAY-2": "billing", "PAY-3": "shipping"} {
		if it, ok := env.ticket(key); !ok || it.Phase != "running" || it.Service != service {
			t.Errorf("%s = %+v, want in progress in %s", key, it, service)
		}
	}
	// The second child of billing waits: its service is busy and its blocker unfinished.
	env.notClaimed(t, "PAY-4")

	barrier.open()
	waitFor(t, "the three children to finish", func() bool {
		return env.finished("PAY-2") && env.finished("PAY-3") && env.finished("PAY-4")
	})
	stop()
	if err := wait(); err != nil {
		t.Fatalf("the stopped listener returned %v\nstderr:\n%s", err, stderr.String())
	}
	log := stderr.String()

	// The plan: the catalog read once, the selection, a survey of the reference only, the requirements,
	// specify (specs/listener-requirements: no target is surveyed or checked out by the plan).
	calls := invocations(t, env.invocationsLog)
	if want := []string{"select", "survey", "requirements", "specify"}; len(calls) < len(want) || !slices.Equal(calls[:len(want)], want) {
		t.Errorf("calls = %v, want the plan's first: %v", calls, want)
	}
	if got := gl.asked(); !slices.Equal(got, []string{"/api/v4/groups/group/projects"}) {
		t.Errorf("GitLab API requests = %v, want the group's projects once", got)
	}

	// The children: created in the story's order with the queue's labels, under the Epic; one link,
	// the later billing task blocked by the first.
	env.jira.mu.Lock()
	created, links := slices.Clone(env.jira.created), slices.Clone(env.jira.links)
	env.jira.mu.Unlock()
	if len(created) != 3 || len(links) != 1 {
		t.Fatalf("created %d issues and %d links, want 3 and 1\nstderr:\n%s", len(created), len(links), log)
	}
	for i, want := range []struct{ title, service, project string }{
		{"The dictionary", "billing", "group/billing"},
		{"The editor", "shipping", "group/shipping"},
		{"The country attributes", "billing", "group/billing"},
	} {
		var is struct {
			Fields struct {
				Parent  struct{ Key string } `json:"parent"`
				Summary string               `json:"summary"`
				Labels  []string             `json:"labels"`
			} `json:"fields"`
		}
		if err := json.Unmarshal([]byte(created[i]), &is); err != nil {
			t.Fatal(err)
		}
		f := is.Fields
		if f.Parent.Key != "PAY-1" || f.Summary != want.title || !slices.Equal(f.Labels, []string{"ai-run", "service:" + want.service, "gitlab:" + want.project}) {
			t.Errorf("child %d = %+v, want %q in %s under PAY-1", i, f, want.title, want.service)
		}
	}
	var l struct{ InwardIssue, OutwardIssue struct{ Key string } }
	if err := json.Unmarshal([]byte(links[0]), &l); err != nil || l.InwardIssue.Key != "PAY-4" || l.OutwardIssue.Key != "PAY-2" {
		t.Errorf("link = %s, want PAY-4 blocked by PAY-2", links[0])
	}

	// The Epic stays at start, with the requirements comment and the one that names its children, and
	// is planned once.
	if got := env.jira.statusOf("PAY-1"); got != "In Progress" {
		t.Errorf("PAY-1 is in %q, want In Progress", got)
	}
	comments := env.jira.botComments("PAY-1")
	if len(comments) != 2 || !strings.Contains(comments[0], "Requirements of PAY-1, from the Epic and its references (catalog):") ||
		!strings.Contains(comments[1], "Planned: 3 child tickets created under PAY-1.") {
		t.Errorf("PAY-1 comments = %q, want the requirements, then the plan's", comments)
	}
	if e := env.state().Tickets["PAY-1"]; e.Phase != "finished" || e.Outcome != "planned" || e.Attempts != 1 {
		t.Errorf("PAY-1 state = %+v, want planned after one attempt", e)
	}

	// Three merge requests: two of billing, one after the other, and one of shipping; none in the reference.
	billing, shipping := filepath.Join(env.wd, "billing"), filepath.Join(env.wd, "shipping")
	for key, want := range map[string]struct {
		dir, title, status string
	}{
		"PAY-2": {billing, "The dictionary", "Done"},
		"PAY-3": {shipping, "The editor", "Ready to Review"},
		"PAY-4": {billing, "The country attributes", "Ready to Review"},
	} {
		if e := env.state().Tickets[key]; e.Outcome != "done" || e.Attempts != 1 {
			t.Errorf("%s state = %+v, want done after one attempt", key, e)
		}
		if got := env.jira.statusOf(key); got != want.status {
			t.Errorf("%s is in %q, want %s", key, got, want.status)
		}
		found := false
		for _, run := range env.runsIn(want.dir) {
			if strings.Contains(run.Task.Description, want.title) {
				found = true
				if run.Status != "completed" || run.MR == nil || run.MR.State != "created" {
					t.Errorf("the run of %s = %+v, want completed with its merge request", key, run)
				}
			}
		}
		if !found {
			t.Errorf("no run of %s in %s", key, want.dir)
		}
	}
	if b, s := gl.mergeRequests("group/billing"), gl.mergeRequests("group/shipping"); b != "2" || s != "1" {
		t.Errorf("merge requests: billing %s, shipping %s; want 2 and 1", b, s)
	}
	if runs := env.runsIn(filepath.Join(env.wd, "catalog")); len(runs) != 0 || gl.mergeRequests("group/catalog") != "0" {
		t.Errorf("the reference was run (%d runs) or pushed to", len(runs))
	}
	// The second child of billing was claimed only after the first was delivered and moved to Done.
	if first, second := strings.Index(log, "listen: PAY-2 → Ready to Review"), strings.Index(log, "listen: PAY-4 claimed"); first < 0 || second < first {
		t.Errorf("PAY-4 was claimed at %d, before PAY-2 ended at %d:\n%s", second, first, log)
	}
}

// An Epic whose requirements leave a question open is blocked with that one comment, and no child is
// created and no target is checked out; once a person answers in a comment and moves the Epic back to
// the queue, its next plan reads the answer, goes through to specify and creates the children
// (specs/listener-requirements/SPEC.md §3, §8).
func TestListen_AnEpicWithAQuestionIsBlockedAndPlannedOnceAnswered(t *testing.T) {
	env, _ := newEpicListenEnv(t)
	responses, prompts := os.Getenv("FAKE_RESPONSES_DIR"), os.Getenv("FAKE_PROMPTS_DIR")
	writeResponse(t, responses, "requirements", epicAskingResponse)

	stderr := mustListen(t, env)

	if got := env.jira.statusOf("PAY-1"); got != "Blocked" {
		t.Fatalf("PAY-1 is in %q, want Blocked\nstderr:\n%s", got, stderr)
	}
	// The references are surveyed, the requirements asked for, and nothing is specified.
	if got, want := invocations(t, env.invocationsLog), []string{"select", "survey", "requirements"}; !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
	comments := env.jira.botComments("PAY-1")
	if len(comments) != 1 {
		t.Fatalf("PAY-1 comments = %q, want one\nstderr:\n%s", comments, stderr)
	}
	for _, want := range []string{"[orchestrator]\n", "Requirements of PAY-1, from the Epic and its references (catalog):",
		"* Billing keeps the categories of catalog.", "Open questions", "# Which countries get the categories", "move the Epic back to To Do"} {
		if !strings.Contains(comments[0], want) {
			t.Errorf("the comment lacks %q:\n%s", want, comments[0])
		}
	}
	env.jira.mu.Lock()
	created := len(env.jira.created)
	env.jira.mu.Unlock()
	if created != 0 {
		t.Errorf("%d children were created, want none", created)
	}
	if e := env.state().Tickets["PAY-1"]; e.Phase != "finished" || e.Outcome != "blocked" || e.Attempts != 0 {
		t.Errorf("PAY-1 state = %+v, want blocked, no attempt counted", e)
	}
	for _, target := range []string{"billing", "shipping"} {
		if _, err := os.Stat(filepath.Join(env.wd, target)); err == nil {
			t.Errorf("the plan checked out the target %s", target)
		}
	}
	if _, err := os.Stat(filepath.Join(env.wd, "catalog")); err != nil {
		t.Errorf("the reference was not checked out: %v", err)
	}

	// A person answers and moves the Epic back to the queue; the plan now understands the Epic.
	env.jira.person("PAY-1", "Only Germany and France.")
	env.jira.setStatus("PAY-1", "To Do")
	writeResponse(t, responses, "requirements", epicRequirementsResponse)
	stderr = mustListen(t, env)

	prompt, err := os.ReadFile(filepath.Join(prompts, "requirements.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Which countries get the categories", "--- comment, ", "Only Germany and France."} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("the second requirements prompt lacks %q:\n%s", want, prompt)
		}
	}
	want := []string{"select", "survey", "requirements", "select", "survey", "requirements", "specify"}
	if got := invocations(t, env.invocationsLog); !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v\nstderr:\n%s", got, want, stderr)
	}
	env.jira.mu.Lock()
	created = len(env.jira.created)
	env.jira.mu.Unlock()
	if created != 3 {
		t.Errorf("%d children were created, want 3\nstderr:\n%s", created, stderr)
	}
	comments = env.jira.botComments("PAY-1")
	if len(comments) != 3 || !strings.Contains(comments[1], "Requirements of PAY-1") || strings.Contains(comments[1], "Open questions") ||
		!strings.Contains(comments[2], "Planned: 3 child tickets created under PAY-1.") {
		t.Errorf("PAY-1 comments = %q, want the questions, the requirements, then the plan's", comments)
	}
	if e := env.state().Tickets["PAY-1"]; e.Outcome != "planned" || e.Attempts != 1 {
		t.Errorf("PAY-1 state = %+v, want planned after one attempt", e)
	}
}
