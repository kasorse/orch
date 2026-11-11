package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/app"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Revise scenarios 80-83 (specs/revise/tasks/TASK-RV06-scenarios-docs.md): orchestrator revise
// against the fake executables, the fake Jira, a bare remote that answers like GitLab
// (fakeexec.AddGitLabRemote) and a fake GitLab API served over TLS on loopback under the remote's
// host name, gitlab.test (app.SetGitLabHTTPClient).

const reviseReadToken = "test-gitlab-read-token"

// fakeGitLabAPI answers the read-only client: the token's record and merge requests by iid.
type fakeGitLabAPI struct {
	srv    *httptest.Server
	scopes string
	mu     sync.Mutex
	mrs    map[int]string
	paths  []string
}

func (g *fakeGitLabAPI) handle(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.paths = append(g.paths, r.Method+" "+r.URL.EscapedPath())
	if r.Header.Get("PRIVATE-TOKEN") != reviseReadToken || r.Method != http.MethodGet {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/api/v4/personal_access_tokens/self" {
		fmt.Fprintf(w, `{"id":1,"active":true,"revoked":false,"scopes":%s}`, g.scopes)
		return
	}
	i := strings.LastIndex(r.URL.Path, "/merge_requests/")
	var iid int
	if i < 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	fmt.Sscanf(r.URL.Path[i+len("/merge_requests/"):], "%d", &iid)
	body, ok := g.mrs[iid]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = w.Write([]byte(body))
}

// Paths are the requests received, "METHOD /escaped/path".
func (g *fakeGitLabAPI) Paths() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.paths...)
}

// startGitLabAPI serves the fake GitLab API and routes the orchestrator's GitLab client to it.
func startGitLabAPI(t *testing.T) *fakeGitLabAPI {
	t.Helper()
	g := &fakeGitLabAPI{scopes: `["read_api"]`, mrs: map[int]string{}}
	g.srv = httptest.NewTLSServer(http.HandlerFunc(g.handle))
	t.Cleanup(g.srv.Close)
	transport := g.srv.Client().Transport.(*http.Transport).Clone()
	addr := g.srv.Listener.Addr().String()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	transport.TLSClientConfig.ServerName = "example.com" // the name httptest's certificate is for
	t.Cleanup(app.SetGitLabHTTPClient(&http.Client{Transport: transport}))
	t.Setenv("GITLAB_READ_TOKEN", reviseReadToken)
	return g
}

func (g *fakeGitLabAPI) addMR(project string, iid int, state, source, target, sha string, created time.Time) {
	body, _ := json.Marshal(map[string]any{
		"iid": iid, "project_id": 7, "source_project_id": 7, "state": state,
		"source_branch": source, "target_branch": target, "sha": sha,
		"created_at": created.UTC().Format(time.RFC3339), "title": "PAY-31: Fix Add",
		"description": "The merge request's own description.",
		"web_url":     fmt.Sprintf("https://%s/%s/-/merge_requests/%d", fakeexec.GitLabHost, project, iid),
	})
	g.mu.Lock()
	g.mrs[iid] = string(body)
	g.mu.Unlock()
}

// reviseFixture is a worktree repository whose origin has the target branch and the merge
// request's branch fix/PAY-31 one commit ahead (the local copy deleted), a fake Jira with PAY-31
// linking !7, and the fake GitLab API with !7 in state.
type reviseFixture struct {
	r              stageRun
	repoDir        string
	project        string
	target, fixSHA string
	jira           *fakeJira
	gitlab         *fakeGitLabAPI
}

func newReviseFixture(t *testing.T, state string) reviseFixture {
	t.Helper()
	repoDir := fakeexec.InitGitFixture(t)
	// The bare repository lives under the temporary directory: its project path starts with the
	// directory's first segment, the one group allowed.
	group := strings.Split(strings.Trim(filepath.ToSlash(os.TempDir()), "/"), "/")[0]
	j := startJira(t, map[string]string{})
	gl := startGitLabAPI(t)
	r, _, _ := newWorktreeRun(t, repoDir, func(o *configOptions) {
		o.jira = &jiraOptions{baseURL: j.URL()}
		o.extraYAML += fmt.Sprintf("\ngitlab:\n  base_url: https://%s\n  allowed_groups: [%s]\n  api_token_env: GITLAB_READ_TOKEN\n", fakeexec.GitLabHost, group)
	})
	// The requirement's one line is the review's one criterion.
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "review",
		reviewJSON("pass", "[]", `[{"index": 1, "met": true, "evidence": "fixture.go:4"}]`))
	target := currentBranch(t, repoDir)
	mustGit(t, repoDir, "push", "-q", "origin", target)
	mustGit(t, repoDir, "checkout", "-q", "-b", "fix/PAY-31")
	if err := os.WriteFile(filepath.Join(repoDir, "first.txt"), []byte("the merge request's first commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repoDir, "add", "-A")
	mustGit(t, repoDir, "commit", "-qm", "first")
	fixSHA, _ := gitOutput(repoDir, "rev-parse", "HEAD")
	mustGit(t, repoDir, "push", "-q", "origin", "fix/PAY-31")
	mustGit(t, repoDir, "checkout", "-q", target)
	mustGit(t, repoDir, "branch", "-q", "-D", "fix/PAY-31")

	project := strings.TrimSuffix(strings.Trim(filepath.ToSlash(r.remote), "/"), ".git")
	gl.addMR(project, 7, state, "fix/PAY-31", target, fixSHA, time.Now().Add(-time.Hour))
	mrURL := fmt.Sprintf("https://%s/%s/-/merge_requests/7", fakeexec.GitLabHost, project)
	j.issues["PAY-31"] = jiraIssue(t, "PAY-31", "Fix Add", "Add must add.", nil)
	j.issues["PAY-31/remotelink"] = `[{"id":1,"relationship":"merge request","object":{"url":"` + mrURL + `","title":"!7"}}]`
	return reviseFixture{r: r, repoDir: repoDir, project: project, target: target, fixSHA: fixSHA, jira: j, gitlab: gl}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := gitOutput(dir, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

// reviseRunJSON is the part of run.json the revise scenarios assert on.
type reviseRunJSON struct {
	Status string `json:"status"`
	Commit string `json:"commit"`
	Task   struct {
		Description string   `json:"description"`
		Acceptance  []string `json:"acceptance"`
		Source      struct {
			Kind   string `json:"kind"`
			Key    string `json:"key"`
			Revise struct {
				Mode   string `json:"mode"`
				Branch string `json:"branch"`
			} `json:"revise"`
		} `json:"source"`
	} `json:"task"`
	Worktree struct {
		Branch         string `json:"branch"`
		BaseRef        string `json:"base_ref"`
		ExistingBranch bool   `json:"existing_branch"`
	} `json:"worktree"`
	ReviewBase string `json:"review_base"`
	MR         *struct {
		State string `json:"state"`
		IID   int    `json:"iid"`
		URL   string `json:"url"`
	} `json:"mr"`
	JiraWrite *struct {
		Description *struct {
			State string `json:"state"`
		} `json:"description"`
	} `json:"jira_write"`
}

func loadReviseRun(t *testing.T, repoDir string) reviseRunJSON {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var run reviseRunJSON
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

// descriptionPuts are the bodies of the PUTs the fake Jira received.
func (j *fakeJira) descriptionPuts() []string {
	var out []string
	for _, r := range j.Requests() {
		if r.Method == http.MethodPut {
			out = append(out, r.Body)
		}
	}
	return out
}

func assertDescriptionUpdated(t *testing.T, j *fakeJira, run reviseRunJSON) {
	t.Helper()
	puts := j.descriptionPuts()
	date := time.Now().UTC().Format("2006-01-02")
	if len(puts) != 1 || !strings.Contains(puts[0], `Add must add.\n\nUpdate `+date+`: Round half up`) {
		t.Errorf("description PUTs = %q", puts)
	}
	if run.JiraWrite == nil || run.JiraWrite.Description == nil || run.JiraWrite.Description.State != "done" {
		t.Errorf("jira_write = %+v", run.JiraWrite)
	}
}

// 80: a Jira key whose latest merge request is open: a new commit on its branch, pushed without
// options, the merge request recorded as updated, the review over the whole merge request, and the
// requirement appended to the ticket's description after the push.
func TestRevise80_JiraKeyUpdatesTheOpenMergeRequest(t *testing.T) {
	f := newReviseFixture(t, "opened")
	targetTracking, _ := gitOutput(f.repoDir, "rev-parse", "refs/remotes/origin/"+f.target)

	stdout, stderr, err := runOrchestrator(t, f.repoDir, f.r.cfg, "revise", "PAY-31", "Round half up")
	mustSucceed(t, stdout, stderr, err)

	run := loadReviseRun(t, f.repoDir)
	if run.Status != "completed" || run.Task.Source.Kind != "revise" || run.Task.Source.Key != "PAY-31" ||
		run.Task.Source.Revise.Mode != "update" || !run.Worktree.ExistingBranch || run.Worktree.Branch != "fix/PAY-31" || run.Worktree.BaseRef != f.fixSHA {
		t.Fatalf("run = %+v", run)
	}
	if !strings.HasPrefix(run.Task.Description, "Round half up\n\n---\n") || !strings.Contains(run.Task.Description, "Add must add.") ||
		len(run.Task.Acceptance) != 1 || run.Task.Acceptance[0] != "Round half up" {
		t.Errorf("task = %+v", run.Task)
	}
	if run.MR == nil || run.MR.State != "updated" || run.MR.IID != 7 {
		t.Fatalf("mr = %+v", run.MR)
	}
	if tip, _ := gitOutput(f.r.remote, "rev-parse", "fix/PAY-31"); tip != run.Commit {
		t.Errorf("the remote's fix/PAY-31 = %s, want the run's commit %s", tip, run.Commit)
	}
	if parent, _ := gitOutput(f.r.remote, "rev-parse", run.Commit+"^"); parent != f.fixSHA {
		t.Errorf("the commit's parent = %s, want the merge request's tip %s", parent, f.fixSHA)
	}
	if options := fakeexec.GitLabPushOptions(t, f.r.remote); len(options) != 0 {
		t.Errorf("push options sent: %q", options)
	}
	if msg, _ := gitOutput(f.repoDir, "log", "-1", "--format=%B", run.Commit); !strings.Contains(msg, "Orchestrator-Revises: !7") {
		t.Errorf("commit message = %q", msg)
	}
	if run.ReviewBase == "" {
		t.Error("review_base is not recorded")
	}
	reviews, _ := filepath.Glob(filepath.Join(f.repoDir, ".orchestrator", "runs", "*", "artifacts", "*-review.json"))
	if len(reviews) != 1 {
		t.Fatalf("review artifacts = %q, want one", reviews)
	}
	data, _ := os.ReadFile(reviews[0])
	// The whole merge request: its first commit's file is in the diff the review read.
	assertContains(t, "the review prompt", string(data), "first.txt")
	assertContains(t, "stdout", stdout, "Merge request !7 updated: ")
	assertDescriptionUpdated(t, f.jira, run)
	for _, p := range f.gitlab.Paths() {
		if !strings.HasPrefix(p, "GET ") {
			t.Errorf("GitLab got %s", p)
		}
	}
	// The operator's checkout keeps its branch; the fetch wrote only under refs/orchestrator/.
	if branch := currentBranch(t, f.repoDir); branch != f.target {
		t.Errorf("the operator's branch is %s", branch)
	}
	// The fetches write under refs/orchestrator/ only; the push moves the pushed branch's tracking
	// ref, as any push does.
	if got, _ := gitOutput(f.repoDir, "rev-parse", "refs/remotes/origin/"+f.target); got != targetTracking {
		t.Errorf("origin/%s moved from %s to %s", f.target, targetTracking, got)
	}
	if got, _ := gitOutput(f.repoDir, "for-each-ref", "--format=%(refname)", "refs/orchestrator/revise/"); !strings.Contains(got, "-target") {
		t.Errorf("refs/orchestrator/revise/ = %q", got)
	}
}

// 81: a merged merge request: a new branch from the target, a new merge request opened by the push,
// and the description appended.
func TestRevise81_MergedOpensANewMergeRequest(t *testing.T) {
	f := newReviseFixture(t, "merged")

	stdout, stderr, err := runOrchestrator(t, f.repoDir, f.r.cfg, "revise", "PAY-31", "Round half up")
	mustSucceed(t, stdout, stderr, err)

	run := loadReviseRun(t, f.repoDir)
	if run.Task.Source.Revise.Mode != "new" || run.Worktree.ExistingBranch || run.Worktree.Branch == "fix/PAY-31" {
		t.Fatalf("run = %+v", run)
	}
	if run.MR == nil || run.MR.State != "created" {
		t.Fatalf("mr = %+v", run.MR)
	}
	options := fakeexec.GitLabPushOptions(t, f.r.remote)
	if len(options) == 0 || options[0] != "merge_request.create" || options[1] != "merge_request.target="+f.target {
		t.Errorf("push options = %q", options)
	}
	if tip, _ := gitOutput(f.r.remote, "rev-parse", "fix/PAY-31"); tip != f.fixSHA {
		t.Errorf("the merged merge request's branch moved to %s", tip)
	}
	assertDescriptionUpdated(t, f.jira, run)
}

// 82: a token that can write is refused before any other request: exit 2, no run, nothing read from
// Jira.
func TestRevise82_AWriteTokenIsRefused(t *testing.T) {
	f := newReviseFixture(t, "opened")
	f.gitlab.scopes = `["api"]`

	stdout, stderr, err := runOrchestrator(t, f.repoDir, f.r.cfg, "revise", "PAY-31", "Round half up")
	if exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Fatalf("err = %v, want exit 2\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	assertContains(t, "the error", err.Error(), "read-only token", "scopes: api")
	assertNotContains(t, "the error", err.Error(), reviseReadToken)
	assertNoRuns(t, f.repoDir)
	if reqs := f.jira.Requests(); len(reqs) != 0 {
		t.Errorf("Jira got %d requests", len(reqs))
	}
	if paths := f.gitlab.Paths(); len(paths) != 1 {
		t.Errorf("GitLab got %q, want only the token's scopes", paths)
	}
}

// 83: a planned revise resumes with no GitLab token at all: the resume builds no GitLab client and
// pushes as the run would have.
func TestRevise83_ResumeNeedsNoGitLab(t *testing.T) {
	f := newReviseFixture(t, "opened")
	stdout, stderr, err := runOrchestratorInteractive(t, f.repoDir, f.r.cfg, "a\n", "--mode", "plan", "revise", "PAY-31", "Round half up")
	mustSucceed(t, stdout, stderr, err)
	id := filepath.Base(filepath.Dir(runJSONPath(t, f.repoDir)))
	asked := len(f.gitlab.Paths())

	unsetenv(t, "GITLAB_READ_TOKEN")
	stdout, stderr, err = resume(t, f.repoDir, f.r.cfg, id)
	mustSucceed(t, stdout, stderr, err)

	if got := len(f.gitlab.Paths()); got != asked {
		t.Errorf("the resume made %d GitLab requests", got-asked)
	}
	run := loadReviseRun(t, f.repoDir)
	if run.MR == nil || run.MR.State != "updated" {
		t.Fatalf("mr = %+v", run.MR)
	}
	if u, err := url.Parse(run.MR.URL); err != nil || u.Host != fakeexec.GitLabHost {
		t.Errorf("mr.url = %q", run.MR.URL)
	}
	assertDescriptionUpdated(t, f.jira, run)
}
