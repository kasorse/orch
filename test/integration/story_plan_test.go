package integration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/story"
)

// story plan (specs/story/tasks/TASK-ST04-story-plan.md): the services a task names are
// surveyed in their own checkouts, specify splits the task, and the spec is written under the
// story home's spec_dir - or, for one task, the command that runs it is printed. The services
// are git repositories under one root, each with its own orchestrator.yaml; the story home is
// another repository, outside the root.

const storySurveyResponse = `{
  "summary": "Refunds live in refund.go.",
  "relevant_files": ["refund.go"],
  "interfaces": ["POST /v1/refunds"],
  "constraints": ["amounts are integer cents"]
}`

// storySpecifyResponse is a valid two-task answer over billing and gateway; the goldens in
// testdata/story_plan are the spec it gives.
const storySpecifyResponse = `{
  "slug": "partial-refunds",
  "prefix": "RF",
  "title": "Partial refunds",
  "spec": {
    "problem": "Refunds are all or nothing.",
    "approach": "Billing stores the refunded amount; the gateway accepts it.",
    "contracts": "POST /v1/refunds takes an optional integer amount in cents.",
    "out_of_scope": "Reports.",
    "risks": ""
  },
  "tasks": [
    {"id": "RF01", "repo": "billing", "title": "Store the refunded amount", "size": "M", "depends_on": [],
     "goal": "Billing records the amount of a partial refund.", "acceptance": ["a refund of 5 of 10 leaves 5"], "notes": "Keep integer cents."},
    {"id": "RF02", "repo": "gateway", "title": "Accept an amount in POST /v1/refunds", "size": "S", "depends_on": ["RF01"],
     "goal": "The gateway passes an amount to billing.", "acceptance": ["the API takes an amount", "a test covers it"], "notes": ""}
  ]
}`

// storyOneTaskResponse is an answer of one task, on the service named repo.
func storyOneTaskResponse(repo string) string {
	return `{
  "slug": "partial-refunds",
  "prefix": "RF",
  "title": "Partial refunds",
  "spec": {"problem": "p", "approach": "a", "contracts": "", "out_of_scope": "", "risks": ""},
  "tasks": [
    {"id": "RF01", "repo": "` + repo + `", "title": "Store the refunded amount", "size": "M", "depends_on": [],
     "goal": "Record the amount of a partial refund.", "acceptance": ["a refund of 5 of 10 leaves 5"], "notes": ""}
  ]
}`
}

// storyRequest names billing and gateway, and no other candidate.
const storyRequest = "Partial refunds: billing stores the amount, gateway accepts it"

type storyFixture struct {
	home, src, cfg, invocationsLog, responses, cwdDir string
}

// newStoryFixture creates <base>/src/<service> for every service and the story home
// <base>/platform, each a git repository with its own orchestrator.yaml; the home's has a story
// section whose root is <base>/src, and jira, when set.
func newStoryFixture(t *testing.T, jira *jiraOptions, services ...string) storyFixture {
	t.Helper()
	installFakeProviders(t, "claude", "codex", "gemini")
	t.Setenv("JEV_API_KEY", "")
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"

	f := storyFixture{home: filepath.Join(base, "platform"), src: filepath.Join(base, "src")}
	newRepo := func(dir string, o configOptions) string {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "refund.go"), []byte("package refund\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg := writeConfig(t, dir, o)
		gitRun(t, dir, "init", "-q")
		gitRun(t, dir, "add", "-A")
		gitRun(t, dir, "commit", "-q", "-m", "initial")
		return cfg
	}
	for _, name := range services {
		newRepo(filepath.Join(f.src, name), opts)
	}
	homeOpts := opts
	homeOpts.extraYAML = "\nstory:\n  roots: [" + yamlQuote(f.src) + "]\n  max_tasks: 12\n"
	homeOpts.jira = jira
	f.cfg = newRepo(f.home, homeOpts)

	f.responses = t.TempDir()
	t.Setenv("FAKE_RESPONSES_DIR", f.responses)
	writeResponse(t, f.responses, "survey", storySurveyResponse)
	writeResponse(t, f.responses, "specify", storySpecifyResponse)
	f.invocationsLog = filepath.Join(t.TempDir(), "invocations.log")
	t.Setenv("INVOCATIONS_LOG", f.invocationsLog)
	f.cwdDir = t.TempDir()
	t.Setenv("FAKE_CWD_DIR", f.cwdDir)
	return f
}

func (f storyFixture) plan(t *testing.T, args ...string) (string, error) {
	t.Helper()
	stdout, stderr, err := runOrchestrator(t, f.home, f.cfg, append([]string{"story", "plan"}, args...)...)
	if stderr != "" {
		t.Logf("stderr:\n%s", stderr)
	}
	return stdout, err
}

// planDirs are the story directories that have no spec.
func (f storyFixture) planDirs(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(f.home, ".orchestrator", "stories", persistence.StoryPlanPrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return dirs
}

func (f storyFixture) assertNoSpecDir(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(f.home, "specs")); !os.IsNotExist(err) {
		t.Errorf("specs/ exists (err=%v)", err)
	}
}

func (f storyFixture) assertNoStoryDir(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(f.home, ".orchestrator", "stories")); !os.IsNotExist(err) {
		t.Errorf(".orchestrator/stories exists (err=%v)", err)
	}
}

func (f storyFixture) cwdOf(t *testing.T, action string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.cwdDir, action+".cwd"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

// planningArtifact is artifacts/<call>.json as the scenarios read it.
type planningArtifact struct {
	Answer json.RawMessage `json:"answer"`
	Error  string          `json:"error"`
	Output string          `json:"output"`
	Prompt string          `json:"prompt"`
}

func readPlanningArtifact(t *testing.T, path string) planningArtifact {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var a planningArtifact
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return a
}

// Two of three services named, a valid answer: two surveys, each in its service's checkout,
// one specify, the spec on disk as the goldens, and the story directory under its slug.
func TestStoryPlan_WritesSpec(t *testing.T) {
	f := newStoryFixture(t, nil, "billing", "gateway", "ledger")

	stdout, err := f.plan(t, storyRequest)
	mustSucceed(t, stdout, "", err)

	if got, want := invocations(t, f.invocationsLog), []string{"survey", "survey", "specify"}; !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v", got, want)
	}
	if got := f.cwdOf(t, "survey"); got != filepath.Join(f.src, "gateway") {
		t.Errorf("the last survey ran in %s, want the gateway checkout", got)
	}
	services := strings.Index(stdout, "Services: billing (")
	if services < 0 || !strings.Contains(stdout, "gateway (") || strings.Contains(stdout, "ledger") ||
		services > strings.Index(stdout, "Survey of billing") {
		t.Errorf("want the Services line, naming billing and gateway, before the first survey:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Spec written to specs/partial-refunds (2 tasks)") {
		t.Errorf("stdout lacks the spec line:\n%s", stdout)
	}

	specDir := filepath.Join(f.home, "specs", "partial-refunds")
	golden := filepath.Join("testdata", "story_plan")
	var written []string
	err = filepath.WalkDir(specDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(specDir, path)
		written = append(written, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"README.md", "SPEC.md",
		"tasks/TASK-RF01-store-the-refunded-amount.md", "tasks/TASK-RF02-accept-an-amount-in-post-v1-refunds.md",
	}
	if !slices.Equal(written, want) {
		t.Fatalf("spec files = %v, want %v", written, want)
	}
	for _, name := range want {
		got, err := os.ReadFile(filepath.Join(specDir, name))
		if err != nil {
			t.Fatal(err)
		}
		exp, err := os.ReadFile(filepath.Join(golden, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(exp) {
			t.Errorf("%s differs from its golden:\n--- got\n%s\n--- want\n%s", name, got, exp)
		}
	}
	// A task file Go wrote reads back as a task (ST01).
	data, err := os.ReadFile(filepath.Join(specDir, want[3]))
	if err != nil {
		t.Fatal(err)
	}
	if task, err := story.ParseTaskFile(filepath.Base(want[3]), data); err != nil || task.ID != "RF02" || task.Repo != "gateway" {
		t.Errorf("ParseTaskFile = %+v, %v", task, err)
	}

	storyDir := filepath.Join(f.home, ".orchestrator", "stories", "partial-refunds")
	record, err := persistence.LoadStoryRecord(filepath.Join(storyDir, "story.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantServices := map[string]string{"billing": filepath.Join(f.src, "billing"), "gateway": filepath.Join(f.src, "gateway")}
	if record.Status != story.StatusPlanned || record.Slug != "partial-refunds" || record.SpecDir != "specs/partial-refunds" ||
		record.JiraParent != "" || len(record.Services) != 2 ||
		record.Services["billing"] != wantServices["billing"] || record.Services["gateway"] != wantServices["gateway"] {
		t.Errorf("story.json = %+v", record)
	}
	for _, call := range []string{"survey-billing", "survey-gateway", "specify"} {
		if _, ok := record.Calls[call]; !ok {
			t.Errorf("story.json has no usage entry for %s: %v", call, record.Calls)
		}
		a := readPlanningArtifact(t, filepath.Join(storyDir, "artifacts", call+".json"))
		if string(a.Answer) == "null" || a.Error != "" || !strings.HasPrefix(a.Prompt, "ACTION: ") {
			t.Errorf("artifacts/%s.json = %+v", call, a)
		}
		if _, err := os.Stat(filepath.Join(storyDir, "logs", call+".stdout.log")); err != nil {
			t.Errorf("no log of %s: %v", call, err)
		}
	}
	if dirs := f.planDirs(t); len(dirs) != 0 {
		t.Errorf("a .plan directory is left: %v", dirs)
	}
}

// An answer whose task names a repository that is not a service: nothing under spec_dir, the
// answer kept in the plan's artifacts, and an error naming the field.
func TestStoryPlan_InvalidAnswer(t *testing.T) {
	f := newStoryFixture(t, nil, "billing", "gateway", "ledger")
	answer := strings.Replace(storySpecifyResponse, `"repo": "billing"`, `"repo": "ledger"`, 1)
	writeResponse(t, f.responses, "specify", answer)

	_, err := f.plan(t, storyRequest)
	var invalid *apperr.InvalidResponseError
	if !errors.As(err, &invalid) || invalid.Field != "tasks[0].repo" || !strings.Contains(err.Error(), "tasks[0].repo") {
		t.Fatalf("err = %v, want an invalid response naming tasks[0].repo", err)
	}
	if code := exitcode.FromError(err); code == exitcode.OK {
		t.Errorf("exit code = %d, want a failure", code)
	}
	f.assertNoSpecDir(t)

	dirs := f.planDirs(t)
	if len(dirs) != 1 {
		t.Fatalf("want the one .plan directory kept, got %v", dirs)
	}
	a := readPlanningArtifact(t, filepath.Join(dirs[0], "artifacts", "specify.json"))
	if strings.TrimSpace(a.Output) != strings.TrimSpace(answer) || string(a.Answer) != "null" || !strings.Contains(a.Error, "tasks[0].repo") {
		t.Errorf("artifacts/specify.json = %+v", a)
	}
	if _, err := os.Stat(filepath.Join(dirs[0], "story.json")); !os.IsNotExist(err) {
		t.Errorf("story.json written for a refused answer (err=%v)", err)
	}
}

// One task is not a story: the command that runs it, and nothing under spec_dir.
func TestStoryPlan_OneTask(t *testing.T) {
	f := newStoryFixture(t, nil, "billing", "gateway")
	writeResponse(t, f.responses, "specify", storyOneTaskResponse("billing"))

	stdout, err := f.plan(t, storyRequest)
	mustSucceed(t, stdout, "", err)
	if !strings.Contains(stdout, "cd "+filepath.Join(f.src, "billing")+" && orchestrator run 'Store the refunded amount'") {
		t.Errorf("stdout lacks the run command:\n%s", stdout)
	}
	f.assertNoSpecDir(t)
}

// A task that names no service: the §7.1 message, no call, no story directory.
func TestStoryPlan_NoServiceNamed(t *testing.T) {
	f := newStoryFixture(t, nil, "billing", "gateway")

	_, err := f.plan(t, "Make refunds partial")
	var cerr *apperr.ConfigError
	if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "the task names no service under story.roots") {
		t.Fatalf("err = %v, want the §7.1 refusal", err)
	}
	if got := invocations(t, f.invocationsLog); len(got) != 0 {
		t.Errorf("calls were made: %v", got)
	}
	f.assertNoStoryDir(t)
}

// --repo names a directory that is not a service: a config error before any call.
func TestStoryPlan_UnknownRepo(t *testing.T) {
	f := newStoryFixture(t, nil, "billing", "gateway")

	_, err := f.plan(t, "--repo", "unknown", storyRequest)
	var cerr *apperr.ConfigError
	if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "--repo unknown is not a service") {
		t.Fatalf("err = %v, want a config error about --repo", err)
	}
	if code := exitcode.FromError(err); code != exitcode.ConfigOrDependency {
		t.Errorf("exit code = %d, want %d", code, exitcode.ConfigOrDependency)
	}
	if got := invocations(t, f.invocationsLog); len(got) != 0 {
		t.Errorf("calls were made: %v", got)
	}
	f.assertNoStoryDir(t)
}

// A spec of the answer's slug exists already: refused before anything is written, and the
// existing directory is left as it was.
func TestStoryPlan_ExistingSpecRefused(t *testing.T) {
	f := newStoryFixture(t, nil, "billing", "gateway")
	existing := filepath.Join(f.home, "specs", "partial-refunds")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "notes.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := f.plan(t, storyRequest)
	if err == nil || !strings.Contains(err.Error(), "exists already") {
		t.Fatalf("err = %v, want the existing spec refused", err)
	}
	entries, err := os.ReadDir(existing)
	if err != nil || len(entries) != 1 || entries[0].Name() != "notes.md" {
		t.Errorf("the existing spec directory changed: %v %v", entries, err)
	}
	if data, err := os.ReadFile(filepath.Join(existing, "notes.md")); err != nil || string(data) != "mine\n" {
		t.Errorf("notes.md = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".orchestrator", "stories", "partial-refunds")); !os.IsNotExist(err) {
		t.Errorf("the story directory took the slug (err=%v)", err)
	}
}

// A ticket whose component is a service: that service is surveyed, though the text names
// none, and ticket.json records the components.
func TestStoryPlan_JiraComponent(t *testing.T) {
	jira := startJira(t, map[string]string{
		"PAY-7": jiraIssue(t, "PAY-7", "Partial refunds", "Refund part of a payment.",
			map[string]any{"components": []map[string]string{{"name": "gateway"}}}),
	})
	f := newStoryFixture(t, &jiraOptions{baseURL: jira.URL()}, "billing", "gateway")
	writeResponse(t, f.responses, "specify", storyOneTaskResponse("gateway"))

	stdout, err := f.plan(t, "--jira", "PAY-7")
	mustSucceed(t, stdout, "", err)

	reqs := jira.Requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0].Query, "components") {
		t.Fatalf("Jira requests = %+v, want one asking for the components", reqs)
	}
	if got, want := invocations(t, f.invocationsLog), []string{"survey", "specify"}; !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v", got, want)
	}
	if got := f.cwdOf(t, "survey"); got != filepath.Join(f.src, "gateway") {
		t.Errorf("the survey ran in %s, want the gateway checkout", got)
	}
	if !strings.Contains(stdout, "Services: gateway (") {
		t.Errorf("stdout lacks the Services line:\n%s", stdout)
	}

	dirs := f.planDirs(t)
	if len(dirs) != 1 {
		t.Fatalf("want one .plan directory, got %v", dirs)
	}
	var ticket struct {
		Key        string   `json:"key"`
		Components []string `json:"components"`
	}
	data, err := os.ReadFile(filepath.Join(dirs[0], "ticket.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &ticket); err != nil || ticket.Key != "PAY-7" || !slices.Equal(ticket.Components, []string{"gateway"}) {
		t.Errorf("ticket.json = %+v (%v)", ticket, err)
	}
}
