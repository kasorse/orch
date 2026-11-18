package app

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/jira"
	"github.com/kasorse/orch/internal/listener"
	"github.com/kasorse/orch/internal/provider"
)

// The plan of an Epic (specs/listener-story/SPEC.md §5–§9), with a fake Jira, a fake GitLab catalog
// over bare repositories on disk, and a fake provider that answers by the action in the prompt.

// epicMoves is what each status of the Epic offers: the ids are the fake's own.
var epicMoves = map[string][]struct{ id, name, to string }{
	"To Do":       {{"11", "Start progress", "In Progress"}, {"21", "Block", "Blocked"}, {"22", "Fail", "Failed"}},
	"In Progress": {{"21", "Block", "Blocked"}, {"22", "Fail", "Failed"}, {"24", "Back to queue", "To Do"}},
}

// epicJira is a Jira with one Epic, PMC-1: its fetch, its comments, its transitions and its status.
type epicJira struct {
	srv *httptest.Server

	mu       sync.Mutex
	status   string
	labels   []string
	comments []string
	posts    []string
	// thread is the Epic's comments as Jira lists them: the listener's and a person's, oldest first.
	thread []string

	// The children: the bodies of the creations and of the links the fake received, the keys the
	// search finds under the Epic already, the status a child is created in, and what the n-th
	// creation or link (from 1) answers: "" for success, "hang" for no answer at all, else a status code.
	issues      []string
	links       []string
	existing    []string
	jqls        []string
	childStatus string
	// issueType is the planned ticket's type, Epic unless a test sets it.
	issueType  string
	createMode func(n int) string
	linkMode   func(n int) string
	timeout    time.Duration
}

// nameKey is a Jira reference by key or by name.
type nameKey struct{ Key, Name string }

// childIssue is the fields of a creation the tests read.
type childIssue struct {
	Fields struct {
		Project     nameKey   `json:"project"`
		IssueType   nameKey   `json:"issuetype"`
		Parent      nameKey   `json:"parent"`
		Summary     string    `json:"summary"`
		Description string    `json:"description"`
		Labels      []string  `json:"labels"`
		Components  []nameKey `json:"components"`
	} `json:"fields"`
}

func (j *epicJira) createdIssues() []childIssue {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]childIssue, len(j.issues))
	for i, body := range j.issues {
		if err := json.Unmarshal([]byte(body), &out[i]); err != nil {
			panic(err)
		}
	}
	return out
}

func (j *epicJira) linkBodies() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.links)
}

// childKey is the key the n-th creation (from 0) is answered with: PMC-2, PMC-3, ...
func childKey(n int) string { return fmt.Sprintf("PMC-%d", n+2) }

// answerWrite makes the fake's answer to a write by its mode: true when the handler has answered.
func answerWrite(w http.ResponseWriter, req *http.Request, mode string) bool {
	switch mode {
	case "":
		return false
	case "hang":
		<-req.Context().Done()
	default:
		code := 500
		_, _ = fmt.Sscanf(mode, "%d", &code)
		w.WriteHeader(code)
		fmt.Fprint(w, `{"errorMessages":["refused by the fake"],"errors":{}}`)
	}
	return true
}

func newEpicJira(t *testing.T, status string, labels ...string) *epicJira {
	t.Helper()
	j := &epicJira{status: status, labels: append([]string{"ai-run"}, labels...), childStatus: "To Do", timeout: 10 * time.Second}
	j.srv = httptest.NewServer(http.HandlerFunc(j.handle))
	t.Cleanup(j.srv.Close)
	return j
}

func (j *epicJira) handle(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// The writes that may never answer must not hold the lock: the test reads the fake meanwhile.
	if req.Method == http.MethodPost && (req.URL.Path == "/rest/api/2/issue" || req.URL.Path == "/rest/api/2/issueLink") {
		j.handleWrite(w, req)
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/rest/api/2/search"):
		j.jqls = append(j.jqls, req.URL.Query().Get("jql"))
		var found []string
		keys := slices.Clone(j.existing)
		for n := range j.issues {
			keys = append(keys, childKey(n))
		}
		for _, k := range keys {
			found = append(found, fmt.Sprintf(`{"key":%q,"fields":{"summary":"child","issuetype":{"name":"Task"},"status":{"name":"To Do"},`+
				`"labels":["ai-run"],"components":[],"reporter":{"accountId":"bot-1"}}}`, k))
		}
		fmt.Fprintf(w, `{"issues":[%s],"isLast":true}`, strings.Join(found, ","))
	case req.Method == http.MethodGet && req.URL.RawQuery == "fields=status" && !strings.HasSuffix(req.URL.Path, "/PMC-1"):
		fmt.Fprintf(w, `{"key":"PMC-2","fields":{"status":{"name":%q}}}`, j.childStatus)
	case req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/PMC-1"):
		body, _ := io.ReadAll(req.Body)
		var in struct {
			Update struct {
				Labels []map[string]string `json:"labels"`
			} `json:"update"`
		}
		_ = json.Unmarshal(body, &in)
		for _, op := range in.Update.Labels {
			j.labels = slices.DeleteFunc(j.labels, func(l string) bool { return l == op["remove"] })
		}
		j.posts = append(j.posts, "PUT "+req.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/transitions"):
		var list []string
		for _, m := range epicMoves[j.status] {
			list = append(list, fmt.Sprintf(`{"id":%q,"name":%q,"to":{"name":%q}}`, m.id, m.name, m.to))
		}
		fmt.Fprintf(w, `{"transitions":[%s]}`, strings.Join(list, ","))
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/transitions"):
		body, _ := io.ReadAll(req.Body)
		var in struct {
			Transition struct {
				ID string `json:"id"`
			} `json:"transition"`
		}
		_ = json.Unmarshal(body, &in)
		j.posts = append(j.posts, req.URL.Path+" "+strings.TrimSpace(string(body)))
		for _, m := range epicMoves[j.status] {
			if m.id == in.Transition.ID {
				j.status = m.to
				break
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/comment"):
		fmt.Fprintf(w, `{"comments":[%s],"total":%d}`, strings.Join(j.thread, ","), len(j.thread))
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/comment"):
		body, _ := io.ReadAll(req.Body)
		var in struct {
			Body string `json:"body"`
		}
		_ = json.Unmarshal(body, &in)
		j.comments = append(j.comments, in.Body)
		j.addToThread("bot-1", in.Body)
		j.posts = append(j.posts, req.URL.Path)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"10001"}`)
	case req.Method == http.MethodGet && req.URL.RawQuery == "fields=status":
		fmt.Fprintf(w, `{"key":"PMC-1","fields":{"status":{"name":%q}}}`, j.status)
	case req.Method == http.MethodGet:
		labels, _ := json.Marshal(j.labels)
		fmt.Fprintf(w, `{"id":"10001","self":"https://ignored","key":"PMC-1","fields":{"summary":"Digital categories migration",`+
			`"issuetype":{"name":%q},"status":{"name":%q},"labels":%s,"components":[],"reporter":{"accountId":"rep-1"},`+
			`"description":"Add a categories dictionary to sku-master and show it in sku-master-fe, as product-master does."}}`, cmp.Or(j.issueType, "Epic"), j.status, labels)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// handleWrite is a creation or a link: it records the body, then answers by the mode of its number.
func (j *epicJira) handleWrite(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	isLink := req.URL.Path == "/rest/api/2/issueLink"
	j.mu.Lock()
	var mode string
	n := 0
	if isLink {
		j.links = append(j.links, string(body))
		n = len(j.links)
		if j.linkMode != nil {
			mode = j.linkMode(n)
		}
	} else {
		j.issues = append(j.issues, string(body))
		n = len(j.issues)
		if j.createMode != nil {
			mode = j.createMode(n)
		}
	}
	j.posts = append(j.posts, "POST "+req.URL.Path)
	j.mu.Unlock()
	if answerWrite(w, req, mode) {
		return
	}
	w.WriteHeader(http.StatusCreated)
	if !isLink {
		fmt.Fprintf(w, `{"id":"%d","key":%q,"self":"https://ignored"}`, 20000+n, childKey(n-1))
	}
}

// addToThread adds a comment by author to the Epic's list, a minute after the one before; j.mu is held.
func (j *epicJira) addToThread(author, body string) {
	b, _ := json.Marshal(body)
	j.thread = append(j.thread, fmt.Sprintf(`{"id":"%d","author":{"accountId":%q},"created":"2026-11-17T10:%02d:00.000+0000","body":%s}`,
		30000+len(j.thread), author, len(j.thread), b))
}

// personComments is a person's comment on the Epic, after what is there.
func (j *epicJira) personComments(body string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.addToThread("rep-1", body)
}

func (j *epicJira) client() *jira.Client {
	return jira.NewHTTPClient(jira.Options{BaseURL: j.srv.URL, Auth: jira.AuthBasic, Timeout: j.timeout},
		jira.NewCredentials(jira.AuthBasic, "bot@example.com", "token"))
}

func (j *epicJira) statusNow() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

func (j *epicJira) commentBodies() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.comments)
}

func (j *epicJira) postCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.posts)
}

// planProvider is the provider of every planning call: it answers by the action named in the prompt's
// first line and records each call.
type planProvider struct {
	mu      sync.Mutex
	outputs map[string]string  // by action
	costs   map[string]float64 // by action
	calls   []planCall
	// hold, when set, blocks a select call until it is closed; entered says it arrived.
	hold    chan struct{}
	entered chan struct{}
}

type planCall struct {
	action, repo, dir, prompt, model string
	authority                        domain.Authority
}

func (p *planProvider) Supports(domain.Authority) bool { return true }

func (p *planProvider) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	first, _, _ := strings.Cut(req.Prompt, "\n")
	action := strings.TrimPrefix(first, "ACTION: ")
	call := planCall{action: action, dir: req.WorkingDir, prompt: req.Prompt, model: req.Model, authority: req.Authority}
	if _, rest, ok := strings.Cut(req.Prompt, `Survey the repository "`); ok {
		call.repo, _, _ = strings.Cut(rest, `"`)
	}
	p.mu.Lock()
	p.calls = append(p.calls, call)
	hold, entered := p.hold, p.entered
	out, cost := p.outputs[action], p.costs[action]
	p.mu.Unlock()
	if hold != nil && action == "select" {
		entered <- struct{}{}
		select {
		case <-hold:
		case <-ctx.Done():
			return provider.Result{}, ctx.Err()
		}
	}
	res := provider.Result{Output: out}
	if cost > 0 {
		res.Usage = &domain.Usage{CostUSD: cost}
	}
	return res, nil
}

func (p *planProvider) actions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, c := range p.calls {
		out = append(out, c.action)
	}
	return out
}

func (p *planProvider) callsOf(action string) []planCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []planCall
	for _, c := range p.calls {
		if c.action == action {
			out = append(out, c)
		}
	}
	return out
}

const epicSurveyAnswer = `{"summary":"What this repository has.","relevant_files":["a.txt"],"interfaces":[],"constraints":[]}`

// epicRequirementsAnswer is what the plan understood of the Epic, with no question.
const epicRequirementsAnswer = `{"summary":"A categories dictionary in sku-master, shown by sku-master-fe.",` +
	`"requirements":["sku-master keeps the categories of product-master, by country.","sku-master-fe shows the category of each SKU."],"questions":[]}`

// epicQuestionsAnswer is the same with two questions, which block the Epic.
const epicQuestionsAnswer = `{"summary":"A categories dictionary in sku-master, shown by sku-master-fe.",` +
	`"requirements":["sku-master keeps the categories of product-master, by country."],` +
	`"questions":["Which countries come first?","Is the old [category] list kept?"]}`

const epicSelectAnswer = `{"services":[{"path":"g/pim/sku-master","why":"the dictionary lives here"},{"path":"g/pim/sku-master-fe","why":"the editor shows it"}]}`

const epicSpecifyAnswer = `{"slug":"digital-categories","prefix":"PM","title":"Digital categories",` +
	`"spec":{"problem":"p","approach":"a","contracts":"c","out_of_scope":"","risks":""},"tasks":[` +
	`{"id":"PM01","repo":"sku-master","title":"The dictionary","size":"M","depends_on":[],"goal":"g","acceptance":["a"],"notes":""},` +
	`{"id":"PM02","repo":"sku-master-fe","title":"The editor","size":"M","depends_on":[],"goal":"g","acceptance":["a"],"notes":""},` +
	`{"id":"PM03","repo":"sku-master","title":"The country attributes","size":"S","depends_on":["PM01"],"goal":"g","acceptance":["a"],"notes":""}]}`

// fakeCatalog is the GitLab groups' projects.
type fakeCatalog struct {
	mu       sync.Mutex
	projects map[string][]domain.Project
	err      error
	groups   []string
}

func (f *fakeCatalog) GroupProjects(_ context.Context, group string) ([]domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groups = append(f.groups, group)
	if f.err != nil {
		return nil, f.err
	}
	return f.projects[group], nil
}

func (f *fakeCatalog) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.groups)
}

// planRemote makes the bare repository root/<project>.git, whose default branch main has one commit.
func planRemote(t *testing.T, root, project string) {
	t.Helper()
	bare := filepath.Join(root, filepath.FromSlash(project)+".git")
	work := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	ckGit(t, root, "init", "-q", "--bare", bare)
	ckGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	ckGit(t, root, "init", "-q", work)
	ckGit(t, work, "checkout", "-q", "-b", "main")
	ckWrite(t, filepath.Join(work, "a.txt"), project+"\n")
	ckGit(t, work, "add", "-A")
	ckGit(t, work, "commit", "-qm", "one")
	ckGit(t, work, "remote", "add", "origin", bare)
	ckGit(t, work, "push", "-q", "origin", "main")
}

// epicEnv is a listener with one Epic, PMC-1, and a catalog of five projects of the group g.
type epicEnv struct {
	t    *testing.T
	r    *listenRunner
	jira *epicJira
	prov *planProvider
	cat  *fakeCatalog
	out  *bytes.Buffer
	// remotes holds the bare repositories the checkouts clone.
	remotes string
}

// pushFile commits name with content to the main branch of project's remote.
func (e *epicEnv) pushFile(project, name, content string) {
	e.t.Helper()
	work := filepath.Join(e.t.TempDir(), "work")
	ckGit(e.t, e.remotes, "clone", "-q", filepath.Join(e.remotes, filepath.FromSlash(project)+".git"), work)
	ckWrite(e.t, filepath.Join(work, name), content)
	ckGit(e.t, work, "add", "-A")
	ckGit(e.t, work, "commit", "-qm", "add "+name)
	ckGit(e.t, work, "push", "-q", "origin", "main")
}

var epicCatalog = []domain.Project{
	{Path: "g/pim/sku-master", Name: "sku-master", Description: "The SKU dictionaries"},
	{Path: "g/pim/sku-master-fe", Name: "sku-master-fe", Description: "The SKU editor"},
	{Path: "g/pim/product-master", Name: "product-master", Description: "Products"},
	{Path: "g/pim/product-master-ui", Name: "product-master-ui"},
	{Path: "g/qa/pim-qa", Name: "pim-qa", Description: "End-to-end tests"},
}

func newEpicEnv(t *testing.T, status string, labels ...string) *epicEnv {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	root := t.TempDir()
	for _, p := range epicCatalog {
		planRemote(t, root, p.Path)
	}
	out := &bytes.Buffer{}
	r := newSlotRunner(t, 2, out)
	j := newEpicJira(t, status, labels...)
	r.jira, r.bot = j.client(), "bot-1"
	create := &config.JiraCreateConfig{IssueType: "Task", Link: config.JiraLinkParent, Labels: []string{"pim"}, Components: map[string]string{"sku-master": "SKU"}}
	r.cfg = config.Config{Jira: &config.JiraConfig{MaxBytes: 1 << 16, Create: create}, GitLab: &config.GitLabConfig{BaseURL: "https://gitlab.example"}}
	r.lc.Query = config.ListenerQuery{Projects: []string{"PMC"}, Label: "ai-run", Statuses: []string{"To Do"}, Reporters: []string{"bot-1"}}
	r.lc.Blockers = &config.ListenerBlockers{LinkType: "Blocks", DoneStatuses: []string{"Done"}}
	r.cfg.Actions.Investigate = config.ActionConfig{ProviderModel: config.ProviderModel{Provider: "claude", Model: "m"}}
	r.lc.MaxAttempts = 3
	r.lc.Labels = config.ListenerLabels{ServicePrefix: "service:", ProjectPrefix: "gitlab:", Restart: "ai-restart", RefPrefix: "ref:"}
	r.lc.Story = &config.ListenerStory{MaxServices: 5, MaxRefs: 3, MaxTasks: 12, MaxCatalog: 500, QueueChildren: true}
	r.lc.DefaultConfig = filepath.Join(t.TempDir(), "default.yaml")
	if err := os.WriteFile(r.lc.DefaultConfig, []byte("version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.checkout = ckCheckout(t, root)
	r.plansDir = filepath.Join(t.TempDir(), "plans")

	prov := &planProvider{
		outputs: map[string]string{"select": epicSelectAnswer, "survey": epicSurveyAnswer, "requirements": epicRequirementsAnswer, "specify": epicSpecifyAnswer},
		costs:   map[string]float64{},
	}
	r.registry = provider.NewRegistry()
	r.registry.Register(provider.Claude, prov)
	cat := &fakeCatalog{projects: map[string][]domain.Project{"g": epicCatalog}}
	r.catalog = cat
	return &epicEnv{t: t, r: r, jira: j, prov: prov, cat: cat, out: out, remotes: root}
}

func (e *epicEnv) head() domain.TicketHead {
	return domain.TicketHead{Key: "PMC-1", Type: "Epic", Status: "To Do", Reporter: "rep-1", Labels: e.jira.labelsNow()}
}

func (j *epicJira) labelsNow() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.labels)
}

// plan claims and plans the Epic and waits for the plan to end.
func (e *epicEnv) plan() {
	e.t.Helper()
	if err := e.r.dispatch(e.head()); err != nil {
		e.t.Fatalf("dispatch: %v\n%s", err, e.out.String())
	}
	e.r.wg.Wait()
	if err := e.r.fatalErr(); err != nil {
		e.t.Fatalf("fatal: %v\n%s", err, e.out.String())
	}
}

func (e *epicEnv) planDir() string { return filepath.Join(e.r.plansDir, "PMC-1.1") }

func (e *epicEnv) artifact(name string) map[string]any {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.planDir(), "artifacts", name+".json"))
	if err != nil {
		e.t.Fatalf("artifact %s: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		e.t.Fatalf("artifact %s: %v", name, err)
	}
	return m
}

func (e *epicEnv) record() planRecord {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.planDir(), "plan.json"))
	if err != nil {
		e.t.Fatalf("plan.json: %v", err)
	}
	var rec planRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		e.t.Fatalf("plan.json: %v\n%s", err, data)
	}
	return rec
}

// stateIsValid reads the saved state back as a restart would.
func (e *epicEnv) stateIsValid() listener.State {
	e.t.Helper()
	data, err := os.ReadFile(e.r.stateFile)
	if err != nil {
		e.t.Fatal(err)
	}
	st, err := listener.ParseState(data)
	if err != nil {
		e.t.Fatalf("the saved state does not load: %v\n%s", err, data)
	}
	return st
}

func TestPlan_SelectsFromTheCatalogSurveysAndSpecifies(t *testing.T) {
	env := newEpicEnv(t, "To Do", "ref:g/pim/product-master")
	env.plan()

	if got, want := env.prov.actions(), []string{"select", "survey", "brief", "brief", "requirements", "specify"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v\n%s", got, want, env.out.String())
	}
	if got := env.cat.asked(); !slices.Equal(got, []string{"g"}) {
		t.Errorf("the catalog was read for %v, want the allowed group once", got)
	}

	// The selection: tool-less, over the whole catalog, the reference named as not choosable.
	sel := env.prov.callsOf("select")[0]
	if sel.authority != domain.AuthorityNone || sel.dir != "" {
		t.Errorf("select ran with authority %q in %q, want none and no directory", sel.authority, sel.dir)
	}
	for _, p := range epicCatalog {
		if !strings.Contains(sel.prompt, p.Path+" | ") {
			t.Errorf("the catalog lacks %s", p.Path)
		}
	}
	if !strings.Contains(sel.prompt, "may not be chosen: g/pim/product-master") {
		t.Errorf("the reference is not named as not choosable:\n%s", sel.prompt)
	}

	// A survey of the reference only, read-only, in place, told it is one: no target is surveyed
	// (specs/listener-requirements/SPEC.md §6).
	surveys := env.prov.callsOf("survey")
	var repos []string
	for _, s := range surveys {
		repos = append(repos, s.repo)
		if s.authority != domain.AuthorityRead || s.dir != filepath.Join(env.r.checkout.servicesDir, s.repo) {
			t.Errorf("survey of %s ran with authority %q in %q", s.repo, s.authority, s.dir)
		}
		if !strings.Contains(s.prompt, "This repository is a reference") {
			t.Errorf("survey of %s is not told it is a reference", s.repo)
		}
	}
	if !slices.Equal(repos, []string{"product-master"}) {
		t.Errorf("surveyed %v, want the reference only", repos)
	}

	// The requirements: tool-less, from the Epic and the reference's survey.
	req := env.prov.callsOf("requirements")[0]
	if req.authority != domain.AuthorityNone || req.dir != "" || !strings.Contains(req.prompt, "Add a categories dictionary to sku-master") ||
		!strings.Contains(req.prompt, "the survey of the reference repository product-master") {
		t.Errorf("requirements ran with authority %q in %q, or lacks the Epic or the reference:\n%s", req.authority, req.dir, req.prompt)
	}

	// Specify works from the requirements and the targets' names and notes, with no target survey.
	spec := env.prov.callsOf("specify")[0]
	for _, want := range []string{
		"reference: not changed, never a task's repo", "run in parallel",
		"the only values \"repo\" may take): sku-master, sku-master-fe\n",
		"The target repositories were not studied", "sku-master keeps the categories of product-master, by country.",
		"sku-master: The SKU dictionaries; the dictionary lives here", "sku-master-fe: The SKU editor; the editor shows it",
	} {
		if !strings.Contains(spec.prompt, want) {
			t.Errorf("the specify prompt lacks %q:\n%s", want, spec.prompt)
		}
	}
	if strings.Contains(spec.prompt, "the survey of the repository sku-master") {
		t.Errorf("specify was given a target's survey:\n%s", spec.prompt)
	}

	// Every call's prompt and answer, and the plan, are in the plan's directory.
	for _, name := range []string{"select", "survey-product-master", "requirements", "specify"} {
		a := env.artifact(name)
		if a["prompt"] == "" || a["output"] == "" || a["answer"] == nil {
			t.Errorf("artifact %s = %v, want its prompt, output and answer", name, a)
		}
	}
	for _, name := range []string{"survey-sku-master", "survey-sku-master-fe"} {
		if _, err := os.Stat(filepath.Join(env.planDir(), "artifacts", name+".json")); err == nil {
			t.Errorf("a target's survey artifact %s was written", name)
		}
	}
	if log := env.out.String(); !strings.Contains(log, "PMC-1: requirements: 2, questions: 0") {
		t.Errorf("the requirements are not logged:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(env.planDir(), "ticket.json")); err != nil {
		t.Errorf("ticket.json: %v", err)
	}
	rec := env.record()
	if !rec.Planned || rec.Source != "select" || len(rec.Targets) != 2 || rec.Targets[0].Why != "the dictionary lives here" ||
		len(rec.References) != 1 || rec.References[0].Path != "g/pim/product-master" ||
		rec.Requirements == nil || len(rec.Requirements.Requirements) != 2 || len(rec.Questions) != 0 {
		t.Errorf("plan.json = %+v", rec)
	}
	if want := []listener.Link{{Blocked: "PM03", Blocker: "PM01"}}; !slices.Equal(rec.Chain, want) {
		t.Errorf("chain = %v, want %v: the second task of sku-master waits for the first, sku-master-fe is not linked", rec.Chain, want)
	}
	if len(rec.Tasks) != 3 || rec.Tasks[2].ID != "PM03" || rec.Tasks[2].BlockedBy != "PM01" || len(rec.Children) != 3 {
		t.Errorf("tasks = %+v, children = %v", rec.Tasks, rec.Children)
	}

	// The plan became its children: the Epic is planned, commented on (the requirements, then the plan),
	// and stays where the claim put it.
	e := env.r.entryOf("PMC-1")
	if e.Phase != listener.PhaseFinished || e.Outcome != listener.ResultPlanned || e.Kind != listener.KindPlan || e.PlanDir != "plans/PMC-1.1" || e.Attempts != 1 {
		t.Errorf("entry = %+v", e)
	}
	comments := env.jira.commentBodies()
	if len(comments) != 2 || !strings.HasPrefix(comments[0], "[orchestrator]\nHow the plan understood PMC-1 (references: product\\-master):\n") ||
		strings.Contains(comments[0], "keeps the categories") || strings.Contains(comments[0], "Open questions") ||
		!strings.Contains(comments[1], "Planned: 3 child tickets created under PMC-1.") {
		t.Errorf("comments = %q, want the requirements, then the plan's", comments)
	}
	if posts := env.jira.writes(); len(posts) == 0 {
		t.Errorf("no child was created")
	}
	if got := env.jira.statusNow(); got != "In Progress" {
		t.Errorf("status = %q, want In Progress: the Epic is not moved", got)
	}
	if st := env.stateIsValid(); st.Tickets["PMC-1"].PlanDir != "plans/PMC-1.1" {
		t.Errorf("saved entry = %+v", st.Tickets["PMC-1"])
	}
	// The reference and the targets are checked out; a target without instructions is briefed from its code.
	for _, name := range []string{"product-master", "sku-master", "sku-master-fe"} {
		if _, err := os.Stat(filepath.Join(env.r.checkout.servicesDir, name, "a.txt")); err != nil {
			t.Errorf("checkout of %s: %v", name, err)
		}
	}
	if b := env.prov.callsOf("brief")[0]; !strings.Contains(b.prompt, "It has no instruction file") {
		t.Errorf("the brief of a target without instructions:\n%s", b.prompt)
	}
}

// A target's brief is made in its checkout, read only, from its instruction files (once each: a file that
// repeats another is left out), the task and its code; the brief reaches the requirements and specify prompts. A failed brief is logged and the plan goes on.
func TestPlan_TheTargetsInstructionsMakeTheirBriefs(t *testing.T) {
	env := newEpicEnv(t, "To Do", "ref:g/pim/product-master")
	env.pushFile("g/pim/sku-master", "CLAUDE.md", "# sku-master\nProducts are per country; there are no sites.\n")
	env.pushFile("g/pim/sku-master", "AGENTS.md", "# sku-master\nProducts are per country; there are no sites.\n")
	env.pushFile("g/pim/sku-master-fe", "README.md", "The editor.\n")
	env.prov.outputs["brief"] = `{"architecture":"Countries, not sites.","rules":["A product attribute is per country."]}`
	env.plan()

	if got, want := env.prov.actions(), []string{"select", "survey", "brief", "brief", "requirements", "specify"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v\n%s", got, want, env.out.String())
	}
	briefs := env.prov.callsOf("brief")
	if b := briefs[0]; b.authority != domain.AuthorityRead || filepath.Base(b.dir) != "sku-master" || !strings.Contains(b.prompt, "=== CLAUDE.md ===") ||
		!strings.Contains(b.prompt, "Add a categories dictionary") ||
		strings.Contains(b.prompt, "=== AGENTS.md ===") || !strings.Contains(b.prompt, "there are no sites") {
		t.Errorf("the brief of sku-master: authority %q, dir %q, prompt:\n%s", b.authority, b.dir, b.prompt)
	}
	for _, action := range []string{"requirements", "specify"} {
		p := env.prov.callsOf(action)[0].prompt
		if !strings.Contains(p, "the brief of the target repository sku-master") || !strings.Contains(p, "Countries, not sites.") ||
			!strings.Contains(p, "sites where the target has countries") {
			t.Errorf("the %s prompt lacks the briefs:\n%s", action, p)
		}
	}
	if a := env.artifact("brief-sku-master"); a["answer"] == nil {
		t.Errorf("the brief's artifact = %v", a)
	}

	// A brief that fails leaves the target without one, and the plan goes on.
	env2 := newEpicEnv(t, "To Do", "ref:g/pim/product-master")
	env2.pushFile("g/pim/sku-master", "CLAUDE.md", "x\n")
	env2.prov.outputs["brief"] = "no json"
	env2.plan()
	if e := env2.r.entryOf("PMC-1"); e.Outcome != listener.ResultPlanned || !strings.Contains(env2.out.String(), "PMC-1: no brief of sku-master: brief") {
		t.Errorf("entry = %+v\n%s", e, env2.out.String())
	}
}

// Planning reads no service's configuration: with none anywhere (no default_config, no configs_dir)
// the reference's survey is on the listener's investigate route and the plan reaches specify.
func TestPlan_NeedsNoServiceConfiguration(t *testing.T) {
	env := newEpicEnv(t, "To Do", "ref:g/pim/product-master")
	env.r.lc.DefaultConfig = ""
	env.plan()

	models := map[string]string{}
	for _, s := range env.prov.callsOf("survey") {
		models[s.repo] = s.model
	}
	want := map[string]string{"product-master": "m"}
	if !maps.Equal(models, want) {
		t.Fatalf("survey models = %v, want %v\n%s", models, want, env.out.String())
	}
	if len(env.prov.callsOf("specify")) != 1 {
		t.Errorf("the plan stopped before specify:\n%s", env.out.String())
	}
}

func TestPlan_GitlabLabelsAreTheServicesAndSkipTheSelection(t *testing.T) {
	env := newEpicEnv(t, "To Do", "gitlab:g/pim/sku-master", "gitlab:g/pim/sku-master-fe")
	env.plan()

	// No reference: the requirements come from the Epic's text alone (B1), and no target is surveyed.
	if got, want := env.prov.actions(), []string{"brief", "brief", "requirements", "specify"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v (no select, no survey)\n%s", got, want, env.out.String())
	}
	if p := env.prov.callsOf("requirements")[0].prompt; strings.Contains(p, "Reference surveys") {
		t.Errorf("the requirements of an Epic without references hold a survey:\n%s", p)
	}
	if c := env.jira.commentBodies(); len(c) != 2 || !strings.Contains(c[0], "How the plan understood PMC-1:") {
		t.Errorf("comments = %q", c)
	}
	if got := env.cat.asked(); len(got) != 0 {
		t.Errorf("the catalog was read for %v with the services named", got)
	}
	if rec := env.record(); rec.Source != "labels" || !rec.Planned || len(rec.Targets) != 2 || rec.Targets[0].Name != "sku-master" {
		t.Errorf("plan.json = %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(env.planDir(), "artifacts", "select.json")); err == nil {
		t.Error("a select artifact was written without a select call")
	}
}

func TestPlan_ASelectionOutsideTheCatalogFailsWithTheAnswerKept(t *testing.T) {
	env := newEpicEnv(t, "To Do")
	env.prov.outputs["select"] = `{"services":[{"path":"g/pim/billing","why":"made up"}]}`
	env.plan()

	if got := env.prov.actions(); !slices.Equal(got, []string{"select"}) {
		t.Fatalf("calls = %v, want the select only", got)
	}
	e := env.r.entryOf("PMC-1")
	if e.Phase != listener.PhaseFinished || e.Outcome != listener.ResultFailed || e.Attempts != 1 || !strings.Contains(e.Reason, "g/pim/billing") {
		t.Errorf("entry = %+v", e)
	}
	a := env.artifact("select")
	if a["output"] != env.prov.outputs["select"] || a["answer"] != nil || !strings.Contains(fmt.Sprint(a["error"]), "not a project of the catalog") ||
		!strings.Contains(fmt.Sprint(a["prompt"]), "ACTION: select") {
		t.Errorf("select artifact = %v, want the prompt, the raw answer and the error", a)
	}
	if rec := env.record(); rec.Outcome != "failed" || rec.Planned {
		t.Errorf("plan.json = %+v", rec)
	}
	if got := env.jira.statusNow(); got != "Failed" {
		t.Errorf("status = %q, want Failed", got)
	}
}

func TestPlan_AReferenceThatIsAlsoATargetIsBlocked(t *testing.T) {
	env := newEpicEnv(t, "To Do", "gitlab:g/pim/sku-master", "ref:g/pim/sku-master")
	env.plan()

	if got := env.prov.actions(); len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}
	e := env.r.entryOf("PMC-1")
	if e.Phase != listener.PhaseFinished || e.Outcome != listener.ResultBlocked || e.Attempts != 0 || !strings.Contains(e.Reason, "also a service of the plan") {
		t.Errorf("entry = %+v", e)
	}
	if got := env.jira.statusNow(); got != "Blocked" {
		t.Errorf("status = %q, want Blocked", got)
	}
	if rec := env.record(); rec.Outcome != "blocked" {
		t.Errorf("plan.json = %+v", rec)
	}
}

func TestPlan_LabelsThatNameNothingUsableAreBlocked(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels []string
	}{
		{"a reference outside the allowed groups", []string{"ref:other/pim/product-master"}},
		{"a malformed label", []string{"gitlab:nogroup"}},
		{"too many references", []string{"ref:g/a/one", "ref:g/a/two", "ref:g/a/three", "ref:g/a/four"}},
		{"two services of one name", []string{"gitlab:g/pim/sku-master", "gitlab:g/qa/sku-master"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newEpicEnv(t, "To Do", tc.labels...)
			env.plan()
			if e := env.r.entryOf("PMC-1"); e.Outcome != listener.ResultBlocked || e.Attempts != 0 || e.Phase != listener.PhaseFinished {
				t.Errorf("entry = %+v, want blocked, no attempt", e)
			}
			if got := env.prov.actions(); len(got) != 0 {
				t.Errorf("calls = %v, want none", got)
			}
		})
	}
}

func TestPlan_TheBudgetStopsBeforeSpecify(t *testing.T) {
	env := newEpicEnv(t, "To Do", "ref:g/pim/product-master")
	limit := 0.5
	env.r.lc.Story.MaxCostUSD = &limit
	env.prov.costs["survey"] = 0.3
	env.prov.costs["requirements"] = 0.3
	env.plan()

	if got, want := env.prov.actions(), []string{"select", "survey", "brief", "brief", "requirements"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v: the survey and the requirements cost $0.60, which reaches the budget before specify\n%s", got, want, env.out.String())
	}
	e := env.r.entryOf("PMC-1")
	if e.Outcome != listener.ResultFailed || e.Attempts != 1 || !strings.Contains(e.Reason, "max_cost_usd") {
		t.Errorf("entry = %+v", e)
	}
	if _, err := os.Stat(filepath.Join(env.planDir(), "artifacts", "specify.json")); err == nil {
		t.Error("a specify artifact was written without a specify call")
	}
	if rec := env.record(); rec.Outcome != "failed" || rec.CostUSD < 0.59 || rec.Planned {
		t.Errorf("plan.json = %+v, want the cost of the survey and the requirements", rec)
	}
	// The requirements comment, then the failure's.
	if c := env.jira.commentBodies(); len(c) != 2 || !strings.Contains(c[0], "How the plan understood PMC-1") || !strings.Contains(c[1], `max\_cost\_usd`) ||
		env.jira.statusNow() != "Failed" {
		t.Errorf("comments = %q, status = %q", c, env.jira.statusNow())
	}
}

// The budget is checked before the requirements call too.
func TestPlan_TheBudgetStopsBeforeTheRequirements(t *testing.T) {
	env := newEpicEnv(t, "To Do", "ref:g/pim/product-master")
	limit := 0.5
	env.r.lc.Story.MaxCostUSD = &limit
	env.prov.costs["survey"] = 0.6
	env.plan()

	if got, want := env.prov.actions(), []string{"select", "survey"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v\n%s", got, want, env.out.String())
	}
	if c := env.jira.commentBodies(); len(c) != 1 || strings.Contains(c[0], "How the plan understood") {
		t.Errorf("comments = %q, want the failure's only", c)
	}
}

// An Epic whose requirements leave a question is blocked, with one comment that holds them and the
// questions, and nothing is specified or created (specs/listener-requirements/SPEC.md §3, R2).
func TestPlan_AQuestionBlocksTheEpicWithOneComment(t *testing.T) {
	env := newEpicEnv(t, "To Do", "ref:g/pim/product-master")
	env.prov.outputs["requirements"] = epicQuestionsAnswer
	env.prov.outputs["lookup"] = `{"answers":[]}`
	env.plan()

	if got, want := env.prov.actions(), []string{"select", "survey", "brief", "brief", "requirements", "lookup"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v: no specify\n%s", got, want, env.out.String())
	}
	if got := env.jira.writes(); len(got) != 0 {
		t.Errorf("writes = %v, want no child", got)
	}
	e := env.r.entryOf("PMC-1")
	if e.Phase != listener.PhaseFinished || e.Outcome != listener.ResultBlocked || e.Attempts != 0 || !strings.Contains(e.Reason, "2 open questions") {
		t.Errorf("entry = %+v, want blocked, no attempt", e)
	}
	if got := env.jira.statusNow(); got != "Blocked" {
		t.Errorf("status = %q, want Blocked", got)
	}
	c := env.jira.commentBodies()
	if len(c) != 1 {
		t.Fatalf("comments = %q, want one", c)
	}
	for _, want := range []string{
		"[orchestrator]\nHow the plan understood PMC-1 (references: product\\-master):",
		"A categories dictionary in sku\\-master, shown by sku\\-master\\-fe.",
		"Open questions — answer them in a comment and move the Epic back to To Do:",
		`# Which countries come first\?`, `# Is the old \[category\] list kept\?`,
	} {
		if !strings.Contains(c[0], want) {
			t.Errorf("the comment lacks %q:\n%s", want, c[0])
		}
	}
	rec := env.record()
	if rec.Outcome != "blocked" || rec.Planned || rec.Requirements == nil || !slices.Equal(rec.Questions, []string{"Which countries come first?", "Is the old [category] list kept?"}) {
		t.Errorf("plan.json = %+v", rec)
	}
	if !strings.Contains(env.out.String(), "PMC-1: blocked by 2 open questions") {
		t.Errorf("the block is not logged:\n%s", env.out.String())
	}

	// A person answers and moves the Epic back: the next plan's requirements read the answer.
	env.jira.mu.Lock()
	env.jira.status = "To Do"
	env.jira.mu.Unlock()
	env.jira.personComments("Germany first; the old list goes.")
	env.prov.outputs["requirements"] = epicRequirementsAnswer
	env.plan()

	reqs := env.prov.callsOf("requirements")
	if len(reqs) != 2 || !strings.Contains(reqs[1].prompt, "Germany first; the old list goes.") || !strings.Contains(reqs[1].prompt, `Which countries come first\?`) {
		t.Fatalf("the second requirements prompt lacks the discussion:\n%s", reqs[len(reqs)-1].prompt)
	}
	if e := env.r.entryOf("PMC-1"); e.Outcome != listener.ResultPlanned || len(env.jira.createdIssues()) != 3 {
		t.Errorf("entry = %+v, children %d: the second plan does not reach its children", e, len(env.jira.createdIssues()))
	}
}

// Before the questions are put to people the lookup reads the references and the targets, checked out
// for it: what the code settles is written on the Epic as decided, with where it shows, and only the
// rest is asked.
func TestPlan_TheLookupAnswersAQuestionFromTheCode(t *testing.T) {
	env := newEpicEnv(t, "To Do", "ref:g/pim/product-master")
	env.prov.outputs["requirements"] = epicQuestionsAnswer
	env.prov.outputs["lookup"] = `{"answers":[{"question":2,"answer":"No: the import replaces it.","evidence":"product-master/import.go"}]}`
	env.plan()

	if got, want := env.prov.actions(), []string{"select", "survey", "brief", "brief", "requirements", "lookup"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v\n%s", got, want, env.out.String())
	}
	look := env.prov.callsOf("lookup")[0]
	if look.authority != domain.AuthorityRead || look.dir != env.r.checkout.servicesDir {
		t.Errorf("lookup: authority %q, dir %q; want read in %s", look.authority, look.dir, env.r.checkout.servicesDir)
	}
	for _, want := range []string{"- product-master (reference", "- sku-master (the task changes it)", "- sku-master-fe (the task changes it)",
		"1. Which countries come first?", "2. Is the old [category] list kept?"} {
		if !strings.Contains(look.prompt, want) {
			t.Errorf("the lookup prompt lacks %q", want)
		}
	}
	for _, name := range []string{"sku-master", "sku-master-fe"} {
		if _, err := os.Stat(filepath.Join(env.r.checkout.servicesDir, name)); err != nil {
			t.Errorf("the target %s was not checked out for the lookup: %v", name, err)
		}
	}
	c := env.jira.commentBodies()
	if len(c) != 1 || !strings.Contains(c[0], "Decided without asking") ||
		!strings.Contains(c[0], `* Is the old \[category\] list kept\? No: the import replaces it. (found in product\-master/import.go)`) ||
		!strings.Contains(c[0], `# Which countries come first\?`) || strings.Contains(c[0], `# Is the old`) {
		t.Errorf("comments = %q, want the answer decided and one question left", c)
	}
	if rec := env.record(); !slices.Equal(rec.Questions, []string{"Which countries come first?"}) {
		t.Errorf("plan.json questions = %q", rec.Questions)
	}
	if !strings.Contains(env.out.String(), "PMC-1: the code answered 1 of 2 questions") || !strings.Contains(env.out.String(), "blocked by 1 open question") {
		t.Errorf("log:\n%s", env.out.String())
	}
}

// When the code answers every question, and the model took a default of its own, nothing is asked:
// the plan goes on, and what was decided reaches the children as requirements.
func TestPlan_EveryQuestionAnsweredFromTheCodeLetsThePlanGoOn(t *testing.T) {
	env := newEpicEnv(t, "To Do", "ref:g/pim/product-master")
	env.prov.outputs["requirements"] = `{"summary":"A categories dictionary in sku-master, shown by sku-master-fe.",` +
		`"requirements":["sku-master keeps the categories of product-master, by country."],` +
		`"assumptions":["The names are kept in en, nl and fr."],"questions":["Is the old list kept?"]}`
	env.prov.outputs["lookup"] = `{"answers":[{"question":1,"answer":"No.","evidence":"product-master/import.go"}]}`
	env.plan()

	if got, want := env.prov.actions(), []string{"select", "survey", "brief", "brief", "requirements", "lookup", "specify"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v\n%s", got, want, env.out.String())
	}
	if e := env.r.entryOf("PMC-1"); e.Outcome != listener.ResultPlanned {
		t.Fatalf("entry = %+v, want planned\n%s", e, env.out.String())
	}
	c := env.jira.commentBodies()
	if len(c) != 2 || !strings.Contains(c[0], "* The names are kept in en, nl and fr.") ||
		!strings.Contains(c[0], "* Is the old list kept\\? No. (found in product\\-master/import.go)") || strings.Contains(c[0], "Open questions") {
		t.Errorf("comments = %q", c)
	}
	if spec := env.prov.callsOf("specify")[0].prompt; !strings.Contains(spec, "Assumed: The names are kept in en, nl and fr.") ||
		!strings.Contains(spec, "Assumed: Is the old list kept? No. (found in product-master/import.go)") {
		t.Errorf("the specify prompt lacks what was decided:\n%s", spec)
	}
}

// A service with more tasks than listener.story.max_tasks_per_service is specified once more, told which
// to merge; still over, the plan fails and nothing is created.
func TestPlan_TooManyTasksInAServiceAreSpecifiedAgainThenFail(t *testing.T) {
	env := newEpicEnv(t, "To Do", "ref:g/pim/product-master")
	env.r.lc.Story.MaxTasksPerService = 1 // the fixture has two tasks in sku-master
	env.plan()

	specs := env.prov.callsOf("specify")
	if len(specs) != 2 || !strings.Contains(specs[0].prompt, "At most 1 tasks per repository.") ||
		!strings.Contains(specs[1].prompt, "Your previous answer had 2 tasks in sku-master, more than listener.story.max_tasks_per_service (1).") {
		t.Fatalf("specify calls = %d\n%s", len(specs), env.out.String())
	}
	e := env.r.entryOf("PMC-1")
	if e.Outcome != listener.ResultFailed || !strings.Contains(e.Reason, "still has 2 tasks in sku-master") || len(env.jira.createdIssues()) != 0 {
		t.Errorf("entry = %+v, children %d", e, len(env.jira.createdIssues()))
	}
	if a := env.artifact("specify-again"); a["answer"] == nil {
		t.Errorf("the second specify's artifact = %v", a)
	}

	// Within the bound, one specify call.
	env2 := newEpicEnv(t, "To Do", "ref:g/pim/product-master")
	env2.r.lc.Story.MaxTasksPerService = 2
	env2.plan()
	if n := len(env2.prov.callsOf("specify")); n != 1 || env2.r.entryOf("PMC-1").Outcome != listener.ResultPlanned {
		t.Errorf("specify calls = %d, entry = %+v", n, env2.r.entryOf("PMC-1"))
	}
}

func TestPlan_AnInvalidRequirementsAnswerFailsWithTheAnswerKept(t *testing.T) {
	env := newEpicEnv(t, "To Do", "gitlab:g/pim/sku-master")
	env.prov.outputs["requirements"] = `{"summary":"s","requirements":[],"questions":[]}`
	env.plan()

	if got := env.prov.actions(); !slices.Equal(got, []string{"brief", "requirements"}) {
		t.Fatalf("calls = %v, want the requirements only", got)
	}
	e := env.r.entryOf("PMC-1")
	if e.Outcome != listener.ResultFailed || e.Attempts != 1 || !strings.Contains(e.Reason, "requirements") {
		t.Errorf("entry = %+v", e)
	}
	a := env.artifact("requirements")
	if a["output"] != env.prov.outputs["requirements"] || a["answer"] != nil || !strings.Contains(fmt.Sprint(a["error"]), "requirements") {
		t.Errorf("requirements artifact = %v, want the raw answer and the error", a)
	}
	if c := env.jira.commentBodies(); len(c) != 1 || strings.Contains(c[0], "How the plan understood") || env.jira.statusNow() != "Failed" {
		t.Errorf("comments = %q, status = %q", c, env.jira.statusNow())
	}
}

func TestPlan_ASecretInTheRequirementsFailsAndWritesNoRequirements(t *testing.T) {
	secret := "-----BEGIN " + "RSA PRIVATE KEY-----" // split: the repository's own secret scan would flag the fixture
	env := newEpicEnv(t, "To Do", "gitlab:g/pim/sku-master")
	env.prov.outputs["requirements"] = `{"summary":"s","requirements":["use ` + secret + `"],"questions":["q?"]}`
	env.plan()

	if got := env.prov.actions(); !slices.Equal(got, []string{"brief", "requirements", "lookup"}) {
		t.Fatalf("calls = %v, want no specify", got)
	}
	e := env.r.entryOf("PMC-1")
	if e.Outcome != listener.ResultFailed || e.Attempts != 1 || !strings.Contains(e.Reason, "secret") {
		t.Errorf("entry = %+v", e)
	}
	c := env.jira.commentBodies()
	if len(c) != 1 || strings.Contains(c[0], "PRIVATE KEY") || strings.Contains(c[0], "How the plan understood") {
		t.Errorf("comments = %q, want the failure only, without the text", c)
	}
	if rec := env.record(); rec.Requirements != nil || len(rec.Questions) != 0 {
		t.Errorf("plan.json = %+v: the requirements with a secret are recorded", rec)
	}
}

func TestPlan_ACatalogThatCannotBeReadIsMappedBySPEC9(t *testing.T) {
	t.Run("a refused token fails the Epic", func(t *testing.T) {
		env := newEpicEnv(t, "To Do")
		env.cat.err = &apperr.GitLabError{What: "the projects of group g", Kind: apperr.GitLabAuth, StatusCode: 401}
		env.plan()
		e := env.r.entryOf("PMC-1")
		if e.Outcome != listener.ResultFailed || !strings.Contains(e.Reason, "gitlab.api_token_env") {
			t.Errorf("entry = %+v, want failed, naming gitlab.api_token_env", e)
		}
	})
	t.Run("the network sends it back to the queue", func(t *testing.T) {
		env := newEpicEnv(t, "To Do")
		env.cat.err = &apperr.GitLabError{What: "the projects of group g", Kind: apperr.GitLabUnavailable, StatusCode: 503}
		env.plan()
		e := env.r.entryOf("PMC-1")
		if e.Outcome != listener.ResultRetry || e.Retries != 1 || e.Attempts != 0 || e.NextTryAt.IsZero() {
			t.Errorf("entry = %+v, want retry with a backoff and no attempt", e)
		}
		if len(env.jira.commentBodies()) != 0 || env.jira.statusNow() != "To Do" {
			t.Errorf("a retry is not commented: comments %q, status %q", env.jira.commentBodies(), env.jira.statusNow())
		}
	})
}

func TestPlan_WithoutListenerStoryAnEpicIsRefusedAsBefore(t *testing.T) {
	env := newEpicEnv(t, "To Do")
	env.r.lc.Story = nil
	env.plan()

	e := env.r.entryOf("PMC-1")
	if e.Phase != listener.PhaseFinished || e.Outcome != listener.ResultBlocked || e.Kind != "" || e.PlanDir != "" {
		t.Errorf("entry = %+v, want blocked, not a plan", e)
	}
	if got := env.prov.actions(); len(got) != 0 {
		t.Errorf("calls = %v", got)
	}
	if c := env.jira.commentBodies(); len(c) != 1 || !strings.Contains(c[0], "plan it as a story") {
		t.Errorf("comments = %q", c)
	}
	if _, err := os.Stat(env.r.plansDir); err == nil {
		t.Error("a plans directory was made for an Epic that is not planned")
	}
}

// A plan never starts while a run is in progress, and while it plans no slot is free.
func TestPlan_IsExclusive(t *testing.T) {
	env := newEpicEnv(t, "To Do")
	r := env.r

	hold := make(chan struct{})
	r.startRun("PAY-7", "billing", func() error { <-hold; return nil })
	err := r.dispatch(env.head())
	if !errors.Is(err, errEpicWaits) {
		t.Fatalf("dispatch with a run in progress = %v, want errEpicWaits", err)
	}
	if _, ok := r.entry("PMC-1"); ok || env.jira.postCount() != 0 || env.jira.statusNow() != "To Do" {
		t.Errorf("the Epic was claimed while a run was in progress: entry %v, posts %d, status %s", ok, env.jira.postCount(), env.jira.statusNow())
	}
	if !strings.Contains(env.out.String(), "PMC-1 is an Epic to plan: it waits for the runs in progress") {
		t.Errorf("the wait is not logged:\n%s", env.out.String())
	}
	close(hold)
	r.wg.Wait()

	// Planning, with the select call held: nothing else may start.
	env.prov.hold, env.prov.entered = make(chan struct{}), make(chan struct{}, 1)
	if err := r.dispatch(env.head()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-env.prov.entered:
	case <-time.After(30 * time.Second):
		t.Fatalf("the plan never reached its select call:\n%s", env.out.String())
	}
	if r.slotFree() || r.canStart("billing") || r.canStart("") || r.noRuns() {
		t.Error("a run may start while an Epic is planned")
	}
	close(env.prov.hold)
	r.wg.Wait()
	if !r.slotFree() || !r.canStart("billing") || !r.noRuns() {
		t.Error("the slots are still taken after the plan ended")
	}
}

// A plan cut off by a stop has nothing to resume: a restart plans the Epic again from the start.
func TestPlan_ACutOffPlanIsMadeAgainOnRestart(t *testing.T) {
	env := newEpicEnv(t, "In Progress", "gitlab:g/pim/sku-master", "gitlab:g/pim/sku-master-fe")
	claimed(t, env.r, "PMC-1", listener.Entry{Status: "In Progress", Phase: listener.PhaseClaimed, Kind: listener.KindPlan})

	if err := env.r.recover(); err != nil {
		t.Fatal(err)
	}
	env.r.wg.Wait()

	e := env.r.entryOf("PMC-1")
	if e.Phase != listener.PhaseFinished || e.Outcome != listener.ResultPlanned || e.PlanDir != "plans/PMC-1.1" {
		t.Errorf("entry = %+v, want the plan made again to its end", e)
	}
	if got := env.prov.actions(); !slices.Equal(got, []string{"brief", "brief", "requirements", "specify"}) {
		t.Errorf("calls = %v", got)
	}
}

func TestPlan_AStopLeavesTheEpicClaimed(t *testing.T) {
	env := newEpicEnv(t, "To Do")
	env.prov.hold, env.prov.entered = make(chan struct{}), make(chan struct{}, 1)
	if err := env.r.dispatch(env.head()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-env.prov.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the plan never reached its select call")
	}
	env.r.cancel()
	env.r.wg.Wait()

	e := env.r.entryOf("PMC-1")
	if e.Phase != listener.PhaseClaimed || e.Outcome != "" || e.Kind != listener.KindPlan {
		t.Errorf("entry = %+v, want it still claimed, to be planned again", e)
	}
	if got := env.jira.statusNow(); got != "In Progress" || len(env.jira.commentBodies()) != 0 {
		t.Errorf("status = %q, comments = %q: a stop writes nothing", got, env.jira.commentBodies())
	}
}
