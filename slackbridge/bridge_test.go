package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testConfig() *Config {
	c := &Config{
		AllowedUsers: []string{"U1"},
		Channels:     map[string]string{"C9": "api"},
		Projects: map[string]*Project{
			"web": {Runner: "local", Local: &Local{Dir: "/tmp"}},
			"api": {Runner: "local", Local: &Local{Dir: "/tmp"}},
		},
	}
	return c
}

func TestParse(t *testing.T) {
	c := testConfig()
	cases := []struct{ text, channel, project, task, err string }{
		{"<@UBOT> web: fix the &lt;Table&gt; header", "C1", "web", "fix the <Table> header", ""},
		{"<@UBOT> fix it", "C9", "api", "fix it", ""},
		{"note: fix it", "C9", "api", "note: fix it", ""}, // unknown prefix stays in the task
		{"fix it", "C1", "", "", "no project"},
		{"<@UBOT> api:", "C1", "", "", "empty"},
		{"Web: fix it", "C1", "web", "fix it", ""},
		{"*api*: fix it", "C1", "api", "fix it", ""},
		{"api: see <https://x.io/a|a link>", "C1", "api", "see https://x.io/a", ""},
	}
	for _, tc := range cases {
		r, err := c.Parse(tc.text, tc.channel)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%q: want error %q, got %v", tc.text, tc.err, err)
			}
			continue
		}
		if err != nil || r.Project != tc.project || r.Task != tc.task {
			t.Errorf("%q: got %+v %v", tc.text, r, err)
		}
	}
	c.DefaultProject = "web"
	if r, _ := c.Parse("fix it", "C1"); r.Project != "web" {
		t.Errorf("default project: %+v", r)
	}
}

func TestValidate(t *testing.T) {
	c := testConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.AllowedUsers = nil
	c.Projects["x"] = &Project{Runner: "ssh"}
	c.Channels["C2"] = "nope"
	err := c.Validate()
	for _, want := range []string{"allowed_users is required", "runner must be local or gitlab", `project "nope"`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
}

func TestLoadConfigStrict(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("allowed_users: [U1]\nbogus: 1\nprojects: {a: {runner: local, local: {dir: /tmp}}}\n"), 0o600)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("unknown key must fail: %v", err)
	}
}

func TestLocalCommandKeepsTaskOneArg(t *testing.T) {
	r := &LocalRunner{cfg: &Local{Orchestrator: "./orchestrator", Config: "o.yaml", Args: []string{"--claude-auth", "api"}}}
	got := r.command(Request{Task: "fix $(rm -rf /); `x`"})
	want := []string{"./orchestrator", "--config", "o.yaml", "--claude-auth", "api", "--mode", "auto", "--output", "json", "run", "fix $(rm -rf /); `x`"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q", got)
	}
}

func TestEnvFromCommand(t *testing.T) {
	env, err := envFromCommand(context.Background(), `printf "export A='x y'\nexport B=2\nnoise\n"`)
	if err != nil || fmt.Sprint(env) != "[A=x y B=2]" {
		t.Fatalf("%v %v", env, err)
	}
	m := mergeEnv([]string{"A=old", "C=3"}, env)
	if fmt.Sprint(m) != "[A=x y C=3 B=2]" {
		t.Fatalf("%v", m)
	}
}

func TestLocalRunnerWithFakeOrchestrator(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "orch")
	os.WriteFile(fake, []byte(`#!/bin/sh
echo "working... task=$TOKEN_FROM_ENV" >&2
echo '{"v":1,"run_id":"r1","status":"completed","exit_code":0,"workflow":["implement","test"],"branch":"orch/x","mr":{"iid":3,"url":"https://gl/mr/3"},"cost_usd":0.5}'
`), 0o755)
	r := &LocalRunner{cfg: &Local{Dir: dir, Orchestrator: fake, EnvCommand: "echo export TOKEN_FROM_ENV=1"}}
	o := r.Run(context.Background(), Request{Task: "t"}, func(string) {})
	if !o.OK || !strings.Contains(o.Text, "https://gl/mr/3") || !strings.Contains(o.Text, "implement → test") {
		t.Fatalf("%+v", o)
	}

	os.WriteFile(fake, []byte("#!/bin/sh\necho boom >&2\nexit 2\n"), 0o755)
	o = r.Run(context.Background(), Request{Task: "t"}, func(string) {})
	if o.OK || !strings.Contains(o.Text, "boom") {
		t.Fatalf("%+v", o)
	}
}

func TestGitLabRunner(t *testing.T) {
	var polls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/trigger/pipeline"):
			r.ParseForm()
			if r.Form.Get("token") != "tt" || r.Form.Get("variables[ORCH_TASK]") != "do x" || r.Form.Get("variables[ORCH_SLACK_TS]") != "1.2" {
				w.WriteHeader(400)
				return
			}
			fmt.Fprint(w, `{"id":7,"status":"created","web_url":"https://gl/p/7"}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/pipelines/7"):
			if r.Header.Get("PRIVATE-TOKEN") != "at" {
				w.WriteHeader(401)
				return
			}
			polls++
			st := "running"
			if polls > 1 {
				st = "success"
			}
			fmt.Fprintf(w, `{"id":7,"status":%q,"web_url":"https://gl/p/7"}`, st)
		}
	}))
	defer srv.Close()
	t.Setenv("TT", "tt")
	t.Setenv("AT", "at")
	r := &GitLabRunner{cfg: &GitLab{BaseURL: srv.URL, ProjectID: "grp/proj", Ref: "develop", TriggerTokenEnv: "TT", APITokenEnv: "AT"},
		Slack: SlackRef{"C1", "1.2"}, poll: time.Millisecond}
	var progress []string
	o := r.Run(context.Background(), Request{Task: "do x"}, func(s string) { progress = append(progress, s) })
	if !o.OK || len(progress) != 1 || !strings.Contains(progress[0], "#7") {
		t.Fatalf("%+v %v", o, progress)
	}
}

type fakePoster struct {
	mu      sync.Mutex
	posts   []string
	lastKey string
}

func (f *fakePoster) Post(ch, ts, text string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, ts+"|"+text)
	return "m"
}
func (f *fakePoster) React(string, string, string) {}
func (f *fakePoster) all() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.posts, "\n")
}

type fakeRunner struct {
	mu      *sync.Mutex
	running *int
	max     *int
}

func (r fakeRunner) Run(ctx context.Context, req Request, _ func(string)) Outcome {
	task := req.Task
	r.mu.Lock()
	*r.running++
	if *r.running > *r.max {
		*r.max = *r.running
	}
	r.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	r.mu.Lock()
	*r.running--
	r.mu.Unlock()
	return Outcome{OK: true, Text: "done " + task}
}

func TestBridgeAllowlistQueueAndDedupe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &fakePoster{}
	b := NewBridge(ctx, testConfig(), out)
	var mu sync.Mutex
	running, max := 0, 0
	b.newRunner = func(string, SlackRef) Runner { return fakeRunner{&mu, &running, &max} }

	b.Handle("U2", "C1", "1", "", "api: x")
	b.Handle("U1", "C1", "2", "", "api: a")
	b.Handle("U1", "C1", "2", "", "api: a") // duplicate delivery
	b.Handle("U1", "C1", "3", "", "api: b")
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(out.all(), "done b") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := out.all()
	if !strings.Contains(got, "1|:no_entry: you are not allowed") {
		t.Errorf("allowlist: %s", got)
	}
	if strings.Count(got, "done a") != 1 || !strings.Contains(got, "done b") {
		t.Errorf("runs: %s", got)
	}
	if max != 1 {
		t.Errorf("one run at a time per project, saw %d", max)
	}
}

func (f *fakePoster) PostApproval(ch, ts, text, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, ts+"|APPROVE["+key+"] "+text)
	f.lastKey = key
}

func (f *fakePoster) PostAsk(ch, ts, text, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, ts+"|ASK["+key+"] "+text)
}

func (f *fakePoster) Update(ch, ts, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, "update|"+text)
}

func (f *fakePoster) key() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastKey
}

type fakePlanner struct{ resumed chan string }

func (fakePlanner) Run(context.Context, Request, func(string)) Outcome {
	return Outcome{OK: true, Text: "ran directly"}
}
func (fakePlanner) Plan(_ context.Context, req Request, _ func(string)) (Outcome, string) {
	return Outcome{OK: true, Text: "plan for " + req.Task}, "r1"
}
func (p fakePlanner) Resume(_ context.Context, id string, _ func(string)) Outcome {
	p.resumed <- id
	return Outcome{OK: true, Text: "resumed " + id}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBridgeApproveBeforeRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := testConfig()
	cfg.Projects["api"].Approve = true
	out := &fakePoster{}
	b := NewBridge(ctx, cfg, out)
	pl := fakePlanner{resumed: make(chan string, 2)}
	b.newRunner = func(string, SlackRef) Runner { return pl }

	b.Handle("U1", "C1", "5", "", "api: fix x")
	waitFor(t, func() bool { return out.key() != "" })
	if !strings.Contains(out.all(), "plan for fix x") || strings.Contains(out.all(), "ran directly") {
		t.Fatalf("plan first: %s", out.all())
	}
	key := out.key()
	b.Decide("U2", key, true, "C1", "m1") // not allowed: ignored
	b.Decide("U1", key, true, "C1", "m1")
	if id := <-pl.resumed; id != "r1" {
		t.Fatalf("resumed %q", id)
	}
	waitFor(t, func() bool { return strings.Contains(out.all(), "resumed r1") })
	b.Decide("U1", key, true, "C1", "m1") // second click
	if !strings.Contains(out.all(), "already decided") {
		t.Errorf("double click: %s", out.all())
	}

	b.Handle("U1", "C1", "6", "", "api: fix y")
	waitFor(t, func() bool { return strings.Count(out.all(), "APPROVE[") == 2 })
	b.Decide("U1", out.key(), false, "C1", "m2")
	if !strings.Contains(out.all(), "cancelled by <@U1>") || len(pl.resumed) != 0 {
		t.Errorf("cancel: %s", out.all())
	}
}

func TestApproveNeedsLocal(t *testing.T) {
	c := testConfig()
	c.Projects["gl"] = &Project{Runner: "gitlab", Approve: true, GitLab: &GitLab{ProjectID: "1", Ref: "main", TriggerTokenEnv: "T"}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "approve is only supported") {
		t.Fatal(err)
	}
}

func TestLiveProgressEditsOneMessage(t *testing.T) {
	out := &fakePoster{}
	b := NewBridge(context.Background(), testConfig(), out)
	p := b.live("C1", "1")
	p("a")
	p("a")
	p("b")
	got := out.all()
	if strings.Count(got, "1|a") != 1 || !strings.Contains(got, "update|b") || strings.Count(got, "\n") != 1 {
		t.Fatalf("%s", got)
	}
}

func TestLocalRunnerStreamsLog(t *testing.T) {
	old := progressEvery
	progressEvery = 20 * time.Millisecond
	defer func() { progressEvery = old }()
	dir := t.TempDir()
	fake := filepath.Join(dir, "orch")
	os.WriteFile(fake, []byte("#!/bin/sh\necho step-one >&2\nsleep 0.2\necho step-two >&2\necho '{\"run_id\":\"r\",\"status\":\"completed\",\"exit_code\":0}'\n"), 0o755)
	var mu sync.Mutex
	var seen []string
	r := &LocalRunner{cfg: &Local{Dir: dir, Orchestrator: fake}}
	o := r.Run(context.Background(), Request{Task: "x"}, func(s string) { mu.Lock(); seen = append(seen, s); mu.Unlock() })
	all := strings.Join(seen, "\n---\n")
	if !o.OK || !strings.Contains(all, "step-one") || !strings.Contains(seen[len(seen)-1], "step-two") || !strings.Contains(seen[len(seen)-1], "done in") {
		t.Fatalf("%v\n%s", o, all)
	}
}

func TestParseJira(t *testing.T) {
	c := testConfig()
	c.Jira = &JiraConfig{DefaultProjectKey: "PROJ"}
	cases := []struct{ text, project, jira, task string }{
		{"<https://example.atlassian.net/browse/PROJ-15230>", "", "PROJ-15230", ""},
		{"<@UBOT> api: https://x.atlassian.net/browse/PROJ-1 only the header, please", "api", "PROJ-1", "only the header, please"},
		{"PROJ-15230: skip the backend part", "", "PROJ-15230", "skip the backend part"},
		{"please do PROJ-15230; the endpoint returns {id, items}", "", "PROJ-15230", "please do ; the endpoint returns {id, items}"},
		{"15230 use the table from Orders", "", "PROJ-15230", "use the table from Orders"},
		{"#15230", "", "PROJ-15230", ""},
		{"web: 15230 x", "web", "PROJ-15230", "x"},
		{"PROJ-15230\nline two\nline three", "", "PROJ-15230", "line two line three"},
	}
	for _, tc := range cases {
		r, err := c.Parse(tc.text, "D1")
		if err != nil || r.Project != tc.project || r.Jira != tc.jira || r.Task != tc.task {
			t.Errorf("%q: %+v %v", tc.text, r, err)
		}
	}
	c.Jira = nil
	if r, _ := c.Parse("15230 x", "C9"); r.Jira != "" || r.Task != "15230 x" {
		t.Errorf("no default key: %+v", r)
	}
	lr := &LocalRunner{cfg: &Local{}}
	got := fmt.Sprint(lr.command(Request{Jira: "PROJ-1", Task: "notes"}))
	if got != "[orchestrator --mode auto --output json run --jira PROJ-1 notes]" {
		t.Error(got)
	}
}

func TestProjectFromTicket(t *testing.T) {
	c := testConfig()
	c.DefaultProject = "api"
	c.Projects["web"].JiraMatch = []string{"proj-web", "web"}
	c.Projects["api"].JiraMatch = []string{"backend"}
	if p := c.ProjectFor(Ticket{Summary: "[web] create items", Labels: []string{"backend"}}); p != "api" {
		t.Errorf("label beats summary tag: %q", p)
	}
	if p := c.ProjectFor(Ticket{Components: []string{"PROJ-Web"}, Labels: []string{"backend"}}); p != "web" {
		t.Errorf("component first: %q", p)
	}
	if p := c.ProjectFor(Ticket{Summary: "nothing"}); p != "" {
		t.Errorf("%q", p)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &fakePoster{}
	b := NewBridge(ctx, c, out)
	got := make(chan Request, 2)
	b.newRunner = func(string, SlackRef) Runner { return chanRunner(got) }
	b.readTicket = func(_ context.Context, key string) (Ticket, error) {
		return Ticket{Key: key, Summary: "[web] create orders view", Components: []string{"proj-web"}}, nil
	}
	b.Handle("U1", "D1", "1", "", "PROJ-15230 the endpoint returns {id}")
	r := <-got
	if r.Project != "web" || r.Jira != "PROJ-15230" || r.Task != "the endpoint returns {id}" {
		t.Errorf("%+v", r)
	}
	b.Handle("U1", "D1", "2", "", "api: PROJ-15230") // an explicit project wins
	if r := <-got; r.Project != "api" {
		t.Errorf("%+v", r)
	}
	if !strings.Contains(out.all(), "PROJ-15230* [web] create orders view → *web*") {
		t.Errorf("%s", out.all())
	}
}

type chanRunner chan Request

func (c chanRunner) Run(_ context.Context, r Request, _ func(string)) Outcome {
	c <- r
	return Outcome{OK: true, Text: "ok"}
}

type askingPlanner struct{ seen chan Request }

func (askingPlanner) Run(context.Context, Request, func(string)) Outcome { return Outcome{} }
func (p askingPlanner) Plan(_ context.Context, req Request, _ func(string)) (Outcome, string) {
	p.seen <- req
	if req.Force || strings.Contains(req.Task, "answer") {
		return Outcome{OK: true, Text: "plan"}, "r9"
	}
	return Outcome{NeedsInput: true, Text: "the task needs more detail"}, "r8"
}
func (askingPlanner) Resume(context.Context, string, func(string)) Outcome { return Outcome{OK: true} }

func TestNeedsInputReplyAndForce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := testConfig()
	cfg.Projects["api"].Approve = true
	out := &fakePoster{}
	b := NewBridge(ctx, cfg, out)
	p := askingPlanner{seen: make(chan Request, 4)}
	b.newRunner = func(string, SlackRef) Runner { return p }

	b.Handle("U1", "C1", "10", "", "api: build items")
	<-p.seen
	waitFor(t, func() bool { return strings.Contains(out.all(), "ASK[C1/10]") })
	b.Handle("U1", "C1", "11", "10", "<@UBOT> the answer: GET /item")
	if r := <-p.seen; r.Task != "build items\nthe answer: GET /item" || r.Project != "api" {
		t.Fatalf("reply joins the task: %+v", r)
	}
	waitFor(t, func() bool { return out.key() != "" })

	b.Handle("U1", "C1", "20", "", "api: other")
	<-p.seen
	waitFor(t, func() bool { return strings.Contains(out.all(), "ASK[C1/20]") })
	b.Force("U2", "C1/20", "C1", "m") // not allowed
	b.Force("U1", "C1/20", "C1", "m")
	if r := <-p.seen; !r.Force || r.Task != "other" {
		t.Fatalf("force: %+v", r)
	}
	b.Force("U1", "C1/20", "C1", "m")
	waitFor(t, func() bool { return strings.Contains(out.all(), "already answered") })
}

func TestForcedConfigLowersThreshold(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "o.yaml"), []byte("version: 2\npolicy:\n  clarify:\n    threshold: 0.8\n  stages: {x: 1}\n"), 0o644)
	r := &LocalRunner{cfg: &Local{Dir: dir, Config: "o.yaml"}}
	path, cleanup, err := r.forcedConfig()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	cleanup()
	if !strings.Contains(string(b), "threshold: 0.01") || !strings.Contains(string(b), "version: 2") || !strings.Contains(string(b), "x: 1") {
		t.Fatalf("%s", b)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("temp config left behind")
	}
}

func TestAPISpecContract(t *testing.T) {
	dir := t.TempDir()
	doc := `{"paths":{"/api/v1/shop/orders/item/query":{"post":{
	  "requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Req"}}}},
	  "responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Res"}}}}}}},
	 "/api/v1/other":{"get":{}}},
	 "components":{"schemas":{
	  "Req":{"type":"object","required":["store_id"],"properties":{"store_id":{"type":"integer"},"item_ids":{"type":"array","items":{"type":"integer"},"nullable":true}}},
	  "Res":{"type":"object","properties":{"status":{"type":"string","description":"Possible values: Ok; Error"}}}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, doc) }))
	defer srv.Close()
	a := &APISpec{URL: srv.URL, Path: "cache/swagger.json"}
	if fresh, err := a.Refresh(context.Background(), dir); err != nil || !fresh {
		t.Fatal(fresh, err)
	}
	if fresh, _ := a.Refresh(context.Background(), dir); fresh {
		t.Error("a fresh copy is not downloaded again")
	}
	got, err := a.Contract(dir, "create items by {{shop/orders/item/query}}, see https://x.io/a/b")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"POST /api/v1/shop/orders/item/query", "store_id: number", "item_ids?: number[] | null", "status?: Ok|Error"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "/api/v1/other") {
		t.Error("only the named endpoints")
	}
	if got, _ := a.Contract(dir, "no endpoints here"); got != "" {
		t.Errorf("%q", got)
	}
}
