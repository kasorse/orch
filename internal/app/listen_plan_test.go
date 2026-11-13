package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
}

func newEpicJira(t *testing.T, status string, labels ...string) *epicJira {
	t.Helper()
	j := &epicJira{status: status, labels: append([]string{"ai-run"}, labels...)}
	j.srv = httptest.NewServer(http.HandlerFunc(j.handle))
	t.Cleanup(j.srv.Close)
	return j
}

func (j *epicJira) handle(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
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
		fmt.Fprint(w, `{"comments":[],"total":0}`)
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/comment"):
		body, _ := io.ReadAll(req.Body)
		var in struct {
			Body string `json:"body"`
		}
		_ = json.Unmarshal(body, &in)
		j.comments = append(j.comments, in.Body)
		j.posts = append(j.posts, req.URL.Path)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"10001"}`)
	case req.Method == http.MethodGet && req.URL.RawQuery == "fields=status":
		fmt.Fprintf(w, `{"key":"PMC-1","fields":{"status":{"name":%q}}}`, j.status)
	case req.Method == http.MethodGet:
		labels, _ := json.Marshal(j.labels)
		fmt.Fprintf(w, `{"id":"10001","self":"https://ignored","key":"PMC-1","fields":{"summary":"Digital categories migration",`+
			`"issuetype":{"name":"Epic"},"status":{"name":%q},"labels":%s,"components":[],"reporter":{"accountId":"rep-1"},`+
			`"description":"Add a categories dictionary to sku-master and show it in sku-master-fe, as product-master does."}}`, j.status, labels)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (j *epicJira) client() *jira.Client {
	return jira.NewHTTPClient(jira.Options{BaseURL: j.srv.URL, Auth: jira.AuthBasic, Timeout: 10 * time.Second},
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
	action, repo, dir, prompt string
	authority                 domain.Authority
}

func (p *planProvider) Supports(domain.Authority) bool { return true }

func (p *planProvider) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	first, _, _ := strings.Cut(req.Prompt, "\n")
	action := strings.TrimPrefix(first, "ACTION: ")
	call := planCall{action: action, dir: req.WorkingDir, prompt: req.Prompt, authority: req.Authority}
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
	r.cfg = config.Config{Jira: &config.JiraConfig{MaxBytes: 1 << 16}}
	r.cfg.Actions.Investigate = config.ActionConfig{ProviderModel: config.ProviderModel{Provider: "claude", Model: "m"}}
	r.lc.MaxAttempts = 3
	r.lc.Labels = config.ListenerLabels{ServicePrefix: "service:", ProjectPrefix: "gitlab:", Restart: "ai-restart", RefPrefix: "ref:"}
	r.lc.Story = &config.ListenerStory{MaxServices: 5, MaxRefs: 3, MaxTasks: 12, MaxCatalog: 500}
	r.lc.DefaultConfig = filepath.Join(t.TempDir(), "default.yaml")
	if err := os.WriteFile(r.lc.DefaultConfig, []byte("version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.loadConfig = func(string) (config.Config, error) {
		var c config.Config
		c.Actions.Investigate = config.ActionConfig{ProviderModel: config.ProviderModel{Provider: "claude", Model: "survey-model"}}
		c.Context.ExtraDeny = []string{"vault"}
		return c, nil
	}
	r.checkout = ckCheckout(t, root)
	r.plansDir = filepath.Join(t.TempDir(), "plans")

	prov := &planProvider{
		outputs: map[string]string{"select": epicSelectAnswer, "survey": epicSurveyAnswer, "specify": epicSpecifyAnswer},
		costs:   map[string]float64{},
	}
	r.registry = provider.NewRegistry()
	r.registry.Register(provider.Claude, prov)
	cat := &fakeCatalog{projects: map[string][]domain.Project{"g": epicCatalog}}
	r.catalog = cat
	return &epicEnv{t: t, r: r, jira: j, prov: prov, cat: cat, out: out}
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

	if got, want := env.prov.actions(), []string{"select", "survey", "survey", "survey", "specify"}; !slices.Equal(got, want) {
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

	// A survey of each target and one of the reference, read-only, in place, told which is which.
	surveys := env.prov.callsOf("survey")
	var repos []string
	for _, s := range surveys {
		repos = append(repos, s.repo)
		if s.authority != domain.AuthorityRead || s.dir != filepath.Join(env.r.checkout.servicesDir, s.repo) {
			t.Errorf("survey of %s ran with authority %q in %q", s.repo, s.authority, s.dir)
		}
		if isRef := strings.Contains(s.prompt, "This repository is a reference"); isRef != (s.repo == "product-master") {
			t.Errorf("survey of %s: told it is a reference = %v", s.repo, isRef)
		}
	}
	if !slices.Equal(repos, []string{"sku-master", "sku-master-fe", "product-master"}) {
		t.Errorf("surveyed %v, want the two targets, then the reference", repos)
	}
	spec := env.prov.callsOf("specify")[0]
	if !strings.Contains(spec.prompt, "reference: not changed, never a task's repo") || !strings.Contains(spec.prompt, "run in parallel") ||
		!strings.Contains(spec.prompt, "the only values \"repo\" may take): sku-master, sku-master-fe\n") {
		t.Errorf("specify is not told about the reference, the parallel runs and the targets:\n%s", spec.prompt)
	}

	// Every call's prompt and answer, and the plan, are in the plan's directory.
	for _, name := range []string{"select", "survey-sku-master", "survey-sku-master-fe", "survey-product-master", "specify"} {
		a := env.artifact(name)
		if a["prompt"] == "" || a["output"] == "" || a["answer"] == nil {
			t.Errorf("artifact %s = %v, want its prompt, output and answer", name, a)
		}
	}
	if _, err := os.Stat(filepath.Join(env.planDir(), "ticket.json")); err != nil {
		t.Errorf("ticket.json: %v", err)
	}
	rec := env.record()
	if !rec.Planned || rec.Source != "select" || len(rec.Targets) != 2 || rec.Targets[0].Why != "the dictionary lives here" ||
		len(rec.References) != 1 || rec.References[0].Path != "g/pim/product-master" {
		t.Errorf("plan.json = %+v", rec)
	}
	if want := []listener.Link{{Blocked: "PM03", Blocker: "PM01"}}; !slices.Equal(rec.Chain, want) {
		t.Errorf("chain = %v, want %v: the second task of sku-master waits for the first, sku-master-fe is not linked", rec.Chain, want)
	}
	if len(rec.Tasks) != 3 || rec.Tasks[2].ID != "PM03" || rec.Tasks[2].BlockedBy != "PM01" || len(rec.Children) != 0 {
		t.Errorf("tasks = %+v, children = %v", rec.Tasks, rec.Children)
	}

	// Nothing is created in Jira yet: the Epic ends failed, with a comment that says so.
	e := env.r.entryOf("PMC-1")
	if e.Phase != listener.PhaseFinished || e.Outcome != listener.ResultFailed || e.Kind != listener.KindPlan || e.PlanDir != "plans/PMC-1.1" || e.Attempts != 1 {
		t.Errorf("entry = %+v", e)
	}
	comments := env.jira.commentBodies()
	if len(comments) != 1 || !strings.Contains(comments[0], "creating the children is not built yet") {
		t.Errorf("comments = %q", comments)
	}
	if got := env.jira.statusNow(); got != "Failed" {
		t.Errorf("status = %q, want Failed", got)
	}
	if st := env.stateIsValid(); st.Tickets["PMC-1"].PlanDir != "plans/PMC-1.1" {
		t.Errorf("saved entry = %+v", st.Tickets["PMC-1"])
	}
	for _, name := range []string{"sku-master", "sku-master-fe", "product-master"} {
		if _, err := os.Stat(filepath.Join(env.r.checkout.servicesDir, name, "a.txt")); err != nil {
			t.Errorf("checkout of %s: %v", name, err)
		}
	}
}

func TestPlan_GitlabLabelsAreTheServicesAndSkipTheSelection(t *testing.T) {
	env := newEpicEnv(t, "To Do", "gitlab:g/pim/sku-master", "gitlab:g/pim/sku-master-fe")
	env.plan()

	if got, want := env.prov.actions(), []string{"survey", "survey", "specify"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v (no select)\n%s", got, want, env.out.String())
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
	env.prov.costs["survey"] = 0.2
	env.plan()

	if got, want := env.prov.actions(), []string{"select", "survey", "survey", "survey"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v: the three surveys cost $0.60, which reaches the budget before specify\n%s", got, want, env.out.String())
	}
	e := env.r.entryOf("PMC-1")
	if e.Outcome != listener.ResultFailed || e.Attempts != 1 || !strings.Contains(e.Reason, "max_cost_usd") {
		t.Errorf("entry = %+v", e)
	}
	if _, err := os.Stat(filepath.Join(env.planDir(), "artifacts", "specify.json")); err == nil {
		t.Error("a specify artifact was written without a specify call")
	}
	if rec := env.record(); rec.Outcome != "failed" || rec.CostUSD < 0.59 || rec.Planned {
		t.Errorf("plan.json = %+v, want the cost of the three surveys", rec)
	}
	if len(env.jira.commentBodies()) != 1 || env.jira.statusNow() != "Failed" {
		t.Errorf("comments = %q, status = %q", env.jira.commentBodies(), env.jira.statusNow())
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
	if e.Phase != listener.PhaseFinished || e.Outcome != listener.ResultFailed || e.PlanDir != "plans/PMC-1.1" {
		t.Errorf("entry = %+v, want the plan made again to its end", e)
	}
	if got := env.prov.actions(); !slices.Equal(got, []string{"survey", "survey", "specify"}) {
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
