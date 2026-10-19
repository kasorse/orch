package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"orchestrator/internal/config"
	"orchestrator/internal/persistence"
	"orchestrator/internal/story"
)

const (
	jiraTestEmail = "operator@example.com"
	jiraTestToken = "jira-token-0123456789abcdef"
)

// fakeJira answers the creations of a story run: 201 with PAY-(100+n) for the n-th request, unless
// fail takes it over.
type fakeJira struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
	fail   func(n int, w http.ResponseWriter) bool
}

func newFakeJira(t *testing.T) *fakeJira {
	t.Helper()
	f := &fakeJira{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		n, fail := len(f.bodies), f.fail
		f.mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/2/issue" {
			t.Errorf("request %s %s, want POST /rest/api/2/issue", r.Method, r.URL.Path)
		}
		if fail != nil && fail(n, w) {
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":"%d","key":"PAY-%d"}`, 900+n, 100+n)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeJira) setFail(fail func(n int, w http.ResponseWriter) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fail
}

func (f *fakeJira) requests() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.bodies...)
}

// fields is the "fields" object of the n-th request (1-based).
func (f *fakeJira) fields(t *testing.T, n int) map[string]any {
	t.Helper()
	reqs := f.requests()
	if n > len(reqs) {
		t.Fatalf("only %d requests, want the %dth", len(reqs), n)
	}
	fields, _ := reqs[n-1]["fields"].(map[string]any)
	return fields
}

// jiraStory is a spec directory of two tasks - RF01 in billing, RF02 in gateway after it - with
// the story's record, and what a story run needs to create the issues.
type jiraStory struct {
	t        *testing.T
	jira     *fakeJira
	home     string
	specDir  string
	storyDir string
	files    map[string]string
	cfg      config.Config
	parent   string
}

func newJiraStory(t *testing.T) *jiraStory {
	t.Helper()
	t.Setenv("TEST_JIRA_EMAIL", jiraTestEmail)
	t.Setenv("TEST_JIRA_TOKEN", jiraTestToken)
	f := &jiraStory{t: t, jira: newFakeJira(t), home: t.TempDir(), files: map[string]string{}, parent: "PAY-1"}
	f.specDir = filepath.Join(f.home, "specs", "refunds")
	f.storyDir = filepath.Join(f.home, ".orchestrator", "stories", "refunds")
	for _, dir := range []string{filepath.Join(f.specDir, "tasks"), f.storyDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range []story.Task{
		{ID: "RF01", Repo: "billing", Title: "Store the refunded amount"},
		{ID: "RF02", Repo: "gateway", Title: "Accept an amount in the API", DependsOn: []string{"RF01"}},
	} {
		task.Body = "\n## Goal\n\nDo " + task.ID + " with `Refund()`.\n\n## Acceptance\n\n- the change is made\n"
		path := filepath.Join(f.specDir, "tasks", story.TaskFileName(task.ID, task.Title))
		if err := os.WriteFile(path, story.RenderTaskFile(task, "M"), 0o644); err != nil {
			t.Fatal(err)
		}
		f.files[task.ID] = path
	}
	f.cfg = config.Config{Jira: &config.JiraConfig{
		BaseURL: f.jira.URL, Auth: config.JiraAuthBasic, EmailEnv: "TEST_JIRA_EMAIL", TokenEnv: "TEST_JIRA_TOKEN",
		Timeout: 2 * time.Second, MaxRetries: 1,
		Create: &config.JiraCreateConfig{
			IssueType: "Story", Link: config.JiraLinkParent, Labels: []string{"orchestrator"},
			Components: map[string]string{"billing": "Billing"},
		},
	}}
	f.saveRecord(f.baseRecord())
	return f
}

func (f *jiraStory) baseRecord() persistence.StoryRecord {
	approved := time.Now().UTC()
	return persistence.StoryRecord{
		Slug: "refunds", SpecDir: "specs/refunds", CreatedAt: time.Now().UTC(), ApprovedAt: &approved,
		JiraParent: f.parent, Status: story.StatusRunning,
		Services: map[string]string{"billing": f.home + "/billing", "gateway": f.home + "/gateway"},
		Tasks:    map[string]persistence.StoryTask{},
	}
}

func (f *jiraStory) saveRecord(rec persistence.StoryRecord) {
	f.t.Helper()
	if err := persistence.SaveStoryRecord(f.storyDir, rec); err != nil {
		f.t.Fatal(err)
	}
}

// loadRecord is story.json as it is on disk.
func (f *jiraStory) loadRecord() persistence.StoryRecord {
	f.t.Helper()
	rec, err := persistence.LoadStoryRecord(filepath.Join(f.storyDir, persistence.StoryRecordFile))
	if err != nil {
		f.t.Fatal(err)
	}
	return rec
}

// runner is a story run that has read the spec and the record from disk, as run() does after
// load: a new one stands for the next start.
func (f *jiraStory) runner() (*storyRunner, *bytes.Buffer) {
	f.t.Helper()
	spec, err := readStorySpec(f.specDir, "refunds")
	if err != nil {
		f.t.Fatal(err)
	}
	var out bytes.Buffer
	loc := storyLocation{slug: "refunds", home: f.home, specDir: f.specDir, specRel: "specs/refunds", storyDir: f.storyDir}
	r := &storyRunner{
		ctx: context.Background(), cfg: f.cfg, reporter: newReporter(&out, false), stdout: &out, stderr: &out,
		loc: loc, spec: spec, rec: f.loadRecord(), byID: map[string]story.Task{},
	}
	if r.rec.Tasks == nil {
		r.rec.Tasks = map[string]persistence.StoryTask{}
	}
	for _, task := range spec.tasks {
		r.byID[task.ID] = task
	}
	r.order = story.Order(spec.tasks)
	return r, &out
}

func (f *jiraStory) read(id string) string {
	f.t.Helper()
	data, err := os.ReadFile(f.files[id])
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

// withKey is the task file as it was, with the one line SetJira adds.
func withKey(before, id, key string) string {
	return strings.Replace(before, "---\n# "+id, "jira: "+key+"\n---\n# "+id, 1)
}

func TestCreateJiraChildren_CreatesEachMissingIssueOnce(t *testing.T) {
	f := newJiraStory(t)
	before := map[string]string{"RF01": f.read("RF01"), "RF02": f.read("RF02")}
	r, _ := f.runner()

	if err := r.checkJiraChildren(); err != nil {
		t.Fatal(err)
	}
	if err := r.createJiraChildren(); err != nil {
		t.Fatal(err)
	}

	if n := len(f.jira.requests()); n != 2 {
		t.Fatalf("%d requests, want one per task", n)
	}
	for id, key := range map[string]string{"RF01": "PAY-101", "RF02": "PAY-102"} {
		if got, want := f.read(id), withKey(before[id], id, key); got != want {
			t.Errorf("%s task file =\n%q\nwant the old bytes plus the jira line:\n%q", id, got, want)
		}
		rt := f.loadRecord().Tasks[id]
		if rt.Jira != key || rt.JiraIntent {
			t.Errorf("story.json %s = jira %q, intent %v; want %s and no intent", id, rt.Jira, rt.JiraIntent, key)
		}
		if rt.Status != story.TaskPending || rt.Attempt != 0 {
			t.Errorf("story.json %s = %+v: a creation must not make the task look dispatched", id, rt)
		}
		if got := r.byID[id].Jira; got != key {
			t.Errorf("the runner's task %s has jira %q, want %s", id, got, key)
		}
		if got := story.FrozenHash(r.spec.data[id]); got != story.FrozenHash([]byte(f.read(id))) {
			t.Errorf("the runner's copy of %s is not what is on disk", id)
		}
	}

	// The request, link: parent.
	one := f.jira.fields(t, 1)
	if got := fmt.Sprint(one["project"], one["issuetype"], one["parent"], one["labels"], one["components"]); got !=
		"map[key:PAY] map[name:Story] map[key:PAY-1] [orchestrator] [map[name:Billing]]" {
		t.Errorf("RF01 request fields = %s", got)
	}
	if one["summary"] != "RF01 · Store the refunded amount" {
		t.Errorf("summary = %v", one["summary"])
	}
	desc, _ := one["description"].(string)
	for _, want := range []string{"h2. Goal", "{{Refund()}}", "h2. Acceptance", "* the change is made",
		`Repository: billing · Spec: specs/refunds/tasks/TASK\-RF01\-store\-the\-refunded\-amount.md · Depends on: —`} {
		if !strings.Contains(desc, want) {
			t.Errorf("RF01 description lacks %q:\n%s", want, desc)
		}
	}
	two := f.jira.fields(t, 2)
	if _, ok := two["components"]; ok {
		t.Errorf("RF02 (gateway has no component) was sent components %v", two["components"])
	}
	if desc, _ := two["description"].(string); !strings.Contains(desc, `Depends on: PAY\-101`) {
		t.Errorf("RF02 description does not name RF01's key:\n%s", desc)
	}

	// A second start creates nothing.
	again, _ := f.runner()
	if err := again.checkJiraChildren(); err != nil {
		t.Fatal(err)
	}
	if err := again.createJiraChildren(); err != nil {
		t.Fatal(err)
	}
	if n := len(f.jira.requests()); n != 2 {
		t.Errorf("%d requests after the second start, want still 2", n)
	}
}

func TestCreateJiraChildren_FieldLink(t *testing.T) {
	f := newJiraStory(t)
	f.cfg.Jira.Create.Link, f.cfg.Jira.Create.Field = config.JiraLinkField, "customfield_10014"
	r, _ := f.runner()
	if err := r.createJiraChildren(); err != nil {
		t.Fatal(err)
	}
	fields := f.jira.fields(t, 1)
	if fields["customfield_10014"] != "PAY-1" {
		t.Errorf("customfield_10014 = %v, want PAY-1", fields["customfield_10014"])
	}
	if _, ok := fields["parent"]; ok {
		t.Errorf("parent is sent with link: field: %v", fields["parent"])
	}
}

func TestCreateJiraChildren_SkipsTasksWithAKeyOrNone(t *testing.T) {
	f := newJiraStory(t)
	for id, key := range map[string]string{"RF01": "PAY-7", "RF02": story.JiraNone} {
		data, err := story.SetJira([]byte(f.read(id)), key)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.files[id], data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := f.runner()
	if err := r.createJiraChildren(); err != nil {
		t.Fatal(err)
	}
	if n := len(f.jira.requests()); n != 0 {
		t.Errorf("%d requests for tasks that have a key or none", n)
	}
}

func TestCreateJiraChildren_SendsNothingWithoutAParentOrWithoutCreate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(f *jiraStory)
		note   string
	}{
		{"no parent", func(f *jiraStory) {
			rec := f.baseRecord()
			rec.JiraParent = ""
			f.saveRecord(rec)
		}, "no Jira parent"},
		{"no jira.create", func(f *jiraStory) { f.cfg.Jira.Create = nil }, ""},
		{"no jira section", func(f *jiraStory) { f.cfg.Jira = nil }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newJiraStory(t)
			tc.mutate(f)
			before := map[string]string{"RF01": f.read("RF01"), "RF02": f.read("RF02")}
			r, out := f.runner()
			if err := r.checkJiraChildren(); err != nil {
				t.Fatal(err)
			}
			if err := r.createJiraChildren(); err != nil {
				t.Fatal(err)
			}
			if n := len(f.jira.requests()); n != 0 {
				t.Errorf("%d requests", n)
			}
			for id, was := range before {
				if f.read(id) != was {
					t.Errorf("%s was changed", id)
				}
			}
			if tc.note != "" && !strings.Contains(out.String(), tc.note) {
				t.Errorf("no note about it in the output:\n%s", out)
			}
			if tc.note == "" && strings.Contains(out.String(), "Note:") {
				t.Errorf("an unexpected note:\n%s", out)
			}
		})
	}
}

func TestCheckJiraChildren_UnknownComponentServiceFailsBeforeAnyRequest(t *testing.T) {
	f := newJiraStory(t)
	f.cfg.Jira.Create.Components["biling"] = "Billing"
	r, _ := f.runner()
	err := r.checkJiraChildren()
	if err == nil || !strings.Contains(err.Error(), `"biling" is not a service of the story`) {
		t.Fatalf("error = %v, want the unknown service named", err)
	}
	if n := len(f.jira.requests()); n != 0 {
		t.Errorf("%d requests", n)
	}
}

func TestCreateJiraChildren_TimeoutKeepsTheIntentAndTheNextStartStops(t *testing.T) {
	f := newJiraStory(t)
	f.cfg.Jira.Timeout = 100 * time.Millisecond
	f.cfg.Jira.MaxRetries = 3
	f.jira.setFail(func(n int, w http.ResponseWriter) bool {
		time.Sleep(500 * time.Millisecond)
		return true
	})
	before := f.read("RF01")
	r, _ := f.runner()

	err := r.createJiraChildren()
	if err == nil || !strings.Contains(err.Error(), "RF01") || !strings.Contains(err.Error(), "may have been created") {
		t.Fatalf("error = %v, want RF01's failure and the warning that the issue may exist", err)
	}
	if strings.Contains(err.Error(), jiraTestToken) || strings.Contains(err.Error(), jiraTestEmail) {
		t.Errorf("the error shows a credential: %v", err)
	}
	if n := len(f.jira.requests()); n != 1 {
		t.Errorf("%d requests after a timeout, want exactly 1", n)
	}
	rec := f.loadRecord()
	if rt := rec.Tasks["RF01"]; !rt.JiraIntent || rt.Jira != "" {
		t.Errorf("story.json RF01 = %+v, want the intent kept and no key", rt)
	}
	if rec.Status != story.StatusFailed {
		t.Errorf("story status = %s, want failed", rec.Status)
	}
	if f.read("RF01") != before {
		t.Error("the task file was changed")
	}

	// The next start stops, and sends nothing.
	next, _ := f.runner()
	err = next.checkJiraChildren()
	want := "RF01: a Jira issue may already exist; set jira: <KEY> or jira: none in its front matter"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("next start: error = %v, want %q", err, want)
	}
	if n := len(f.jira.requests()); n != 1 {
		t.Errorf("%d requests after the next start, want still 1", n)
	}

	// The operator answers with the key in the front matter, and the story goes on: RF01's
	// intent is no longer in doubt, and only RF02 is created.
	f.jira.setFail(nil)
	data, err := story.SetJira([]byte(f.read("RF01")), "PAY-55")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.files["RF01"], data, 0o644); err != nil {
		t.Fatal(err)
	}
	resumed, _ := f.runner()
	if err := resumed.checkJiraChildren(); err != nil {
		t.Fatal(err)
	}
	if err := resumed.createJiraChildren(); err != nil {
		t.Fatal(err)
	}
	if n := len(f.jira.requests()); n != 2 {
		t.Errorf("%d requests, want RF02's only", n)
	}
	rec = f.loadRecord()
	if rec.Tasks["RF01"].JiraIntent {
		t.Error("RF01's intent is still set after its key was written by hand")
	}
	if got := jiraKey(resumed.byID["RF01"], rec.Tasks["RF01"]); got != "PAY-55" {
		t.Errorf("RF01's key = %q, want PAY-55", got)
	}
	if rec.Tasks["RF02"].Jira != "PAY-102" {
		t.Errorf("RF02's key = %q, want PAY-102", rec.Tasks["RF02"].Jira)
	}
}

func TestCreateJiraChildren_NoneInTheFrontMatterAnswersTheStop(t *testing.T) {
	f := newJiraStory(t)
	rec := f.baseRecord()
	rec.Tasks["RF01"] = persistence.StoryTask{Status: story.TaskPending, Repo: "billing", JiraIntent: true}
	f.saveRecord(rec)

	r, _ := f.runner()
	if err := r.checkJiraChildren(); err == nil {
		t.Fatal("an intent without a key did not stop the story")
	}
	data, err := story.SetJira([]byte(f.read("RF01")), story.JiraNone)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.files["RF01"], data, 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ = f.runner()
	if err := r.checkJiraChildren(); err != nil {
		t.Fatalf("jira: none in the front matter does not answer the stop: %v", err)
	}
	if r.rec.Tasks["RF01"].JiraIntent {
		t.Error("the answered intent is still set")
	}
}

func TestCreateJiraChildren_RejectionClearsTheIntentAndKeepsTheEarlierKeys(t *testing.T) {
	f := newJiraStory(t)
	f.jira.setFail(func(n int, w http.ResponseWriter) bool {
		if n != 2 {
			return false
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"errorMessages":[],"errors":{"components":"Component name 'Billing' is not valid","summary":"echo %s"}}`, jiraTestToken)
		return true
	})
	before := f.read("RF01")
	r, _ := f.runner()

	err := r.createJiraChildren()
	if err == nil || !strings.Contains(err.Error(), "RF02") || !strings.Contains(err.Error(), "components: Component name 'Billing' is not valid") {
		t.Fatalf("error = %v, want RF02's rejection with Jira's words", err)
	}
	if strings.Contains(err.Error(), jiraTestToken) {
		t.Errorf("the error shows the token: %v", err)
	}
	if strings.Contains(err.Error(), "may have been created") {
		t.Errorf("a 400 says the issue may exist: %v", err)
	}

	rec := f.loadRecord()
	if rec.Tasks["RF01"].Jira != "PAY-101" || rec.Tasks["RF01"].JiraIntent {
		t.Errorf("RF01 = %+v, want its key kept", rec.Tasks["RF01"])
	}
	if rt, ok := rec.Tasks["RF02"]; ok {
		t.Errorf("RF02 has a state after a rejection: %+v", rt)
	}
	if f.read("RF01") != withKey(before, "RF01", "PAY-101") {
		t.Error("RF01's file does not carry the key")
	}

	// A new start skips RF01 and creates RF02.
	f.jira.setFail(nil)
	next, _ := f.runner()
	if err := next.checkJiraChildren(); err != nil {
		t.Fatal(err)
	}
	if err := next.createJiraChildren(); err != nil {
		t.Fatal(err)
	}
	if n := len(f.jira.requests()); n != 3 {
		t.Errorf("%d requests, want 3 (RF01, the rejected RF02, RF02 again)", n)
	}
	if got := f.loadRecord().Tasks["RF02"].Jira; got != "PAY-103" {
		t.Errorf("RF02's key = %q, want PAY-103", got)
	}
}

func TestCreateJiraChildren_WritesAKeyThatWasRecordedButNotWrittenToTheFile(t *testing.T) {
	f := newJiraStory(t)
	rec := f.baseRecord()
	rec.Tasks["RF01"] = persistence.StoryTask{Status: story.TaskPending, Repo: "billing", Jira: "PAY-7"}
	f.saveRecord(rec)
	before := f.read("RF01")

	r, _ := f.runner()
	if err := r.checkJiraChildren(); err != nil {
		t.Fatal(err)
	}
	if err := r.createJiraChildren(); err != nil {
		t.Fatal(err)
	}
	if n := len(f.jira.requests()); n != 1 {
		t.Errorf("%d requests, want RF02's only", n)
	}
	if f.read("RF01") != withKey(before, "RF01", "PAY-7") {
		t.Errorf("RF01's file does not carry the recorded key:\n%s", f.read("RF01"))
	}
	if desc, _ := f.jira.fields(t, 1)["description"].(string); !strings.Contains(desc, `Depends on: PAY\-7`) {
		t.Errorf("RF02 does not depend on the recorded key:\n%s", desc)
	}
}

func TestCreateJiraChildren_NeverTouchesADispatchedTask(t *testing.T) {
	f := newJiraStory(t)
	rec := f.baseRecord()
	rec.Tasks["RF01"] = persistence.StoryTask{
		Status: story.TaskFailed, Repo: "billing", Attempt: 1, Branch: "story/refunds/RF01",
		FileSHA256: strings.Repeat("a", 64), BaseRef: strings.Repeat("b", 40),
	}
	f.saveRecord(rec)
	before := f.read("RF01")

	r, _ := f.runner()
	if err := r.createJiraChildren(); err != nil {
		t.Fatal(err)
	}
	if f.read("RF01") != before {
		t.Error("a dispatched task's frozen file was rewritten")
	}
	if n := len(f.jira.requests()); n != 1 {
		t.Errorf("%d requests, want RF02's only", n)
	}
}

func TestCreateJiraChildren_CancelledBeforeTheRequestSendsNothing(t *testing.T) {
	f := newJiraStory(t)
	r, _ := f.runner()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.ctx = ctx

	err := r.createJiraChildren()
	if err == nil {
		t.Fatal("no error after the context was cancelled")
	}
	if n := len(f.jira.requests()); n != 0 {
		t.Errorf("%d requests", n)
	}
	rec := f.loadRecord()
	if rt, ok := rec.Tasks["RF01"]; ok {
		t.Errorf("RF01 has a state after a request that was never sent: %+v", rt)
	}
	if rec.Status != story.StatusStopped {
		t.Errorf("story status = %s, want stopped", rec.Status)
	}
}

func TestSummaryOf(t *testing.T) {
	if got := summaryOf("RF02", "Expose it"); got != "RF02 · Expose it" {
		t.Errorf("summaryOf = %q", got)
	}
	long := summaryOf("RF02", strings.Repeat("é", 400))
	if n := len([]rune(long)); n != maxJiraSummary {
		t.Errorf("a long summary has %d characters, want %d", n, maxJiraSummary)
	}
}
