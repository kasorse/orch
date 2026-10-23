package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/app"
	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/persistence"
	"orchestrator/internal/story"
	"orchestrator/internal/testutil/fakeexec"
)

// story run (specs/story/tasks/TASK-ST06-story-run.md): the driver of a spec directory. Two
// service repositories, billing and gateway, each with its own orchestrator.yaml, and a story
// home with a hand-written spec of four tasks:
//
//	RF01 billing                  based on billing's story base
//	RF02 billing  depends on RF01 stacked on RF01's commit
//	RF03 gateway  depends on RF01 based on gateway's story base
//	RF04 billing  depends on RF02 stacked on RF02's commit
//
// The children story run starts are this test binary (TestMain answers as the orchestrator under
// storyChildEnv), running against the fake provider CLIs and one fake Jev.

const storyRunSlug = "refunds"

var storyRunTasks = []story.Task{
	{ID: "RF01", Repo: "billing", Title: "Store the refunded amount"},
	{ID: "RF02", Repo: "billing", Title: "Round the refunded amount", DependsOn: []string{"RF01"}},
	{ID: "RF03", Repo: "gateway", Title: "Accept an amount in the API", DependsOn: []string{"RF01"}},
	{ID: "RF04", Repo: "billing", Title: "Report the refunded amounts", DependsOn: []string{"RF02"}},
}

const storyImplementResponse = `{
  "summary": "fixed Add to use +",
  "summary_short": "Fix Add to use +",
  "changed_files": ["fixture.go", "added.txt"],
  "tests_added": []
}`

// storyImplementNoMessage proposes no commit message, so a worktree run completes without a
// commit: the task is blocked.
const storyImplementNoMessage = `{
  "summary": "fixed Add to use +",
  "changed_files": ["fixture.go", "added.txt"],
  "tests_added": []
}`

// storyReviewMalformed gives one verdict where the tasks' two criteria need two: the review is
// refused and the run fails.
const storyReviewMalformed = `{"status":"pass","summary":"ok","issues":[],"criteria":[` +
	`{"index":1,"met":true,"evidence":"fixture.go:4"}]}`

type storyRunFixtureOpts struct {
	maxCostUSD float64
	// jira, when set, is the home's jira section (with jira.create) at a fake Jira.
	jira *jiraOptions
	// tasks, when set, are the spec's tasks in place of storyRunTasks.
	tasks []story.Task
}

type storyRunFixture struct {
	home, specDir, storyDir, cfg string
	repos                        map[string]string
	invocationsLog, responses    string
	tasks                        []story.Task
}

func newStoryRunFixture(t *testing.T, mutate ...func(*storyRunFixtureOpts)) *storyRunFixture {
	t.Helper()
	var o storyRunFixtureOpts
	for _, m := range mutate {
		m(&o)
	}
	t.Setenv(storyChildEnv, "1")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Medium: the template gives analysis and review; development is rated under its
	// threshold and a story task adds it all the same.
	j := startJev(t, jevScript{
		PProject: 0.9,
		Choice:   [3]float64{0.1, 0.7, 0.2},
		Stages:   map[string]float64{"analysis": 0.9, "development": 0.1},
	})
	billing := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, billing)
	gateway := fakeexec.InitGitFixture(t)

	t.Setenv("FAKE_CWD_DIR", t.TempDir())
	t.Setenv("FAKE_FIX_TARGET_FILE", "fixture.go")
	t.Setenv("FAKE_IMPLEMENT_TOUCH_FILE", "added.txt")
	f := &storyRunFixture{repos: map[string]string{}, invocationsLog: invocationsLog, responses: os.Getenv("FAKE_RESPONSES_DIR"), tasks: storyRunTasks}
	if o.tasks != nil {
		f.tasks = o.tasks
	}
	writeResponse(t, f.responses, "implement", storyImplementResponse)
	writeResponse(t, f.responses, "review", storyReviewPass)

	src := filepath.Join(base, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	opts := defaultConfigOptions()
	opts.jevURL = j.URL()
	for name, dir := range map[string]string{"billing": billing, "gateway": gateway} {
		dst := filepath.Join(src, name)
		if err := os.Rename(dir, dst); err != nil {
			t.Fatal(err)
		}
		writeConfig(t, dst, opts)
		commitAll(t, dst, "add the orchestrator config")
		f.repos[name] = dst
	}

	f.home = filepath.Join(base, "platform")
	if err := os.MkdirAll(f.home, 0o755); err != nil {
		t.Fatal(err)
	}
	homeOpts := opts
	homeOpts.jira = o.jira
	homeOpts.extraYAML = "\nstory:\n  roots: [" + yamlQuote(src) + "]\n  max_tasks: 12\n"
	if o.maxCostUSD > 0 {
		homeOpts.extraYAML += fmt.Sprintf("  max_cost_usd: %v\n", o.maxCostUSD)
	}
	f.cfg = writeConfig(t, f.home, homeOpts)
	f.specDir = filepath.Join(f.home, "specs", storyRunSlug)
	f.storyDir = filepath.Join(f.home, ".orchestrator", "stories", storyRunSlug)
	f.writeSpec(t)
	gitRun(t, f.home, "init", "-q")
	gitRun(t, f.home, "add", "-A")
	gitRun(t, f.home, "commit", "-q", "-m", "the spec")
	return f
}

func writeStoryFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeSpec writes the spec directory as an operator would write it by hand: no story.json.
func (f *storyRunFixture) writeSpec(t *testing.T) {
	t.Helper()
	writeStoryFile(t, filepath.Join(f.specDir, "SPEC.md"), story.RenderSpec("Partial refunds", story.SpecText{
		Problem:    "Refunds are all or nothing.",
		Approach:   "Billing stores the amount; the gateway accepts it.",
		Contracts:  "POST /v1/refunds takes an optional amount in cents.",
		OutOfScope: "Reports.",
		Risks:      "None.",
	}))
	var tasks []story.Task
	for _, task := range f.tasks {
		task.Body = "\n## Goal\n\nDo " + task.ID + ".\n\n## Acceptance\n\n- the change is made\n- a test covers it\n"
		writeStoryFile(t, f.taskPath(task.ID), story.RenderTaskFile(task, "M"))
		tasks = append(tasks, task)
	}
	writeStoryFile(t, filepath.Join(f.specDir, "README.md"), []byte(story.RenderREADME("Partial refunds", story.StatusPlanned, tasks, nil)))
}

func (f *storyRunFixture) taskPath(id string) string {
	for _, task := range f.tasks {
		if task.ID == id {
			return filepath.Join(f.specDir, "tasks", story.TaskFileName(id, task.Title))
		}
	}
	panic("no such task " + id)
}

// writePlanRecord writes the story.json story plan would have: the services and the cost of the
// planning calls, and nothing of a run.
func (f *storyRunFixture) writePlanRecord(t *testing.T) {
	t.Helper()
	f.writePlanRecordFrom(t, "")
}

// writePlanRecordFrom is writePlanRecord for a story planned from the Jira issue parent.
func (f *storyRunFixture) writePlanRecordFrom(t *testing.T, parent string) {
	t.Helper()
	if err := os.MkdirAll(f.storyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := persistence.StoryRecord{
		Slug: storyRunSlug, SpecDir: "specs/" + storyRunSlug, CreatedAt: time.Now().UTC(),
		Services:   map[string]string{"billing": f.repos["billing"], "gateway": f.repos["gateway"]},
		Status:     story.StatusPlanned,
		JiraParent: parent,
		Calls:      map[string]*domain.Usage{"specify": {CostUSD: 0.05}},
	}
	if err := persistence.SaveStoryRecord(f.storyDir, rec); err != nil {
		t.Fatal(err)
	}
}

// run is `orchestrator story run <spec dir> args...`.
func (f *storyRunFixture) run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	stdout, stderr, err = runOrchestrator(t, f.home, f.cfg, append([]string{"story", "run", f.specDir}, args...)...)
	if stderr != "" {
		t.Logf("stderr:\n%s", stderr)
	}
	return stdout, stderr, err
}

func (f *storyRunFixture) record(t *testing.T) persistence.StoryRecord {
	t.Helper()
	rec, err := persistence.LoadStoryRecord(filepath.Join(f.storyDir, "story.json"))
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// input is the task input story run wrote for the task's latest dispatch.
func (f *storyRunFixture) input(t *testing.T, id string) story.TaskInput {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.storyDir, "tasks", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	in, err := story.ParseTaskInput(data)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func (f *storyRunFixture) inputFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(f.storyDir, "tasks", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// runRecord is the run of attempt n of a task, from its repository's runs directory.
func (f *storyRunFixture) runRecord(t *testing.T, repo, task string, attempt int) domain.Run {
	t.Helper()
	for _, found := range f.runs(t, repo) {
		if s := found.Run.Task.Source; s != nil && s.Task == task && s.Attempt == attempt {
			return found.Run
		}
	}
	t.Fatalf("no run of %s attempt %d in %s", task, attempt, repo)
	return domain.Run{}
}

func (f *storyRunFixture) runs(t *testing.T, repo string) []persistence.FoundRun {
	t.Helper()
	found, bad, err := persistence.LoadRunDirs(filepath.Join(f.repos[repo], ".orchestrator", "runs"))
	if err != nil || len(bad) != 0 {
		t.Fatalf("runs of %s: %v, unreadable: %v", repo, err, bad)
	}
	return found
}

func (f *storyRunFixture) runCount(t *testing.T) int {
	t.Helper()
	return len(f.runs(t, "billing")) + len(f.runs(t, "gateway"))
}

func (f *storyRunFixture) baseBranch(t *testing.T, repo string) string {
	t.Helper()
	branch, err := gitOutput(f.repos[repo], "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return branch
}

func (f *storyRunFixture) head(t *testing.T, repo string) string {
	t.Helper()
	sha, err := gitOutput(f.repos[repo], "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

// checkoutState is what a story run must leave alone in an operator's checkout: HEAD, the
// checked-out branch, the index and every tracked file, except what is below skip.
func checkoutState(t *testing.T, dir, skip string) string {
	t.Helper()
	head, _ := gitOutput(dir, "rev-parse", "HEAD")
	branch, _ := gitOutput(dir, "symbolic-ref", "-q", "HEAD")
	var b strings.Builder
	fmt.Fprintf(&b, "HEAD %s\nbranch %s\n", head, branch)
	cmd := exec.Command("git", "ls-files", "-s", "-z")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files in %s: %v", dir, err)
	}
	for _, entry := range strings.Split(string(out), "\x00") {
		if entry == "" {
			continue
		}
		meta, path, _ := strings.Cut(entry, "\t")
		if skip != "" && strings.HasPrefix(path, skip) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			fmt.Fprintf(&b, "%s\t%s\tunreadable\n", meta, path)
			continue
		}
		fmt.Fprintf(&b, "%s\t%s\t%x\n", meta, path, sha256.Sum256(data))
	}
	return b.String()
}

// checkouts are the operator checkouts: the services, and the story home but for its spec.
func (f *storyRunFixture) checkouts(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"billing": checkoutState(t, f.repos["billing"], ""),
		"gateway": checkoutState(t, f.repos["gateway"], ""),
		"home":    checkoutState(t, f.home, "specs/"+storyRunSlug+"/"),
	}
}

func (f *storyRunFixture) assertCheckoutsUnchanged(t *testing.T, before map[string]string) {
	t.Helper()
	for name, state := range f.checkouts(t) {
		if state != before[name] {
			t.Errorf("the operator's checkout %s changed:\n--- before\n%s--- after\n%s", name, before[name], state)
		}
	}
}

// dirState is every file under dir with its size, modification time and content, for asserting
// that a command wrote nothing.
func dirState(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			fmt.Fprintf(&b, "d %s\n", rel)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "f %s %d %d %x\n", rel, info.Size(), info.ModTime().UnixNano(), sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// runOrchestratorCtx is runOrchestrator with a context the test can cancel.
func runOrchestratorCtx(t *testing.T, ctx context.Context, repoDir, configPath string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	origWD, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("getwd: %v", wdErr)
	}
	if chdirErr := os.Chdir(repoDir); chdirErr != nil {
		t.Fatalf("chdir %s: %v", repoDir, chdirErr)
	}
	defer func() {
		if chdirErr := os.Chdir(origWD); chdirErr != nil {
			t.Fatalf("restore chdir: %v", chdirErr)
		}
	}()
	var outBuf, errBuf bytes.Buffer
	err = app.Run(ctx, append([]string{"--config", configPath, "--mode", "auto"}, args...), strings.NewReader(""), &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), err
}

func assertWorkflowError(t *testing.T, err error) {
	t.Helper()
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) {
		t.Fatalf("err = %T %v, want a workflow error", err, err)
	}
}

func assertConfigError(t *testing.T, err error, wants ...string) {
	t.Helper()
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("err = %T %v, want a config error", err, err)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q lacks %q", err.Error(), want)
		}
	}
}

// Order and stacking, As built and README, and the end: the whole story runs; billing's second
// task is based on the first's commit, gateway's task on gateway's base; every task file ends in
// an As built section and README.md ticks every task; the operator's checkouts are untouched.
func TestStoryRun_OrderStackingAsBuiltAndTheEnd(t *testing.T) {
	f := newStoryRunFixture(t)
	f.writePlanRecord(t)
	before := f.checkouts(t)
	bases := map[string]string{"billing": f.head(t, "billing"), "gateway": f.head(t, "gateway")}

	stdout, stderr, err := f.run(t)
	mustSucceed(t, stdout, stderr, err)

	rec := f.record(t)
	if rec.Status != story.StatusCompleted || rec.ApprovedAt == nil {
		t.Fatalf("status = %s, approved_at = %v; want completed and approved", rec.Status, rec.ApprovedAt)
	}
	if rec.Calls["specify"] == nil || rec.Calls["specify"].CostUSD != 0.05 || rec.Services["billing"] != f.repos["billing"] {
		t.Errorf("story plan's record was not kept: calls = %+v, services = %v", rec.Calls, rec.Services)
	}
	for name, sha := range bases {
		if b := rec.Bases[name]; b.SHA != sha || b.Branch != f.baseBranch(t, name) {
			t.Errorf("bases.%s = %+v, want %s@%s", name, b, f.baseBranch(t, name), sha)
		}
	}

	for _, task := range storyRunTasks {
		id := task.ID
		rt := rec.Tasks[id]
		if rt.Status != story.TaskDone || rt.Commit == "" || rt.Attempt != 1 || rt.RunID == "" || rt.Branch != "story/"+storyRunSlug+"/"+id {
			t.Errorf("tasks.%s = %+v, want done on its branch with a commit", id, rt)
			continue
		}
		repo := f.repos[rt.Repo]
		if tip, _ := gitOutput(repo, "rev-parse", rt.Branch); tip != rt.Commit {
			t.Errorf("%s: %s is at %s, want the recorded commit %s", id, rt.Branch, tip, rt.Commit)
		}
		// One commit on top of the base the task was given.
		if parent, _ := gitOutput(repo, "rev-parse", rt.Commit+"^"); parent != rt.BaseRef {
			t.Errorf("%s: the commit's parent is %s, want its base_ref %s", id, parent, rt.BaseRef)
		}
		if in := f.input(t, id); in.BaseRef != rt.BaseRef || in.Branch != rt.Branch || in.Attempt != 1 || in.Story != storyRunSlug {
			t.Errorf("%s: task input %+v does not match the record %+v", id, in, rt)
		}

		// As built is appended below the frozen part, which is as it was dispatched.
		data := readFile(t, f.taskPath(id))
		if !strings.Contains(data, "\n## As built (") {
			t.Errorf("%s: the task file has no As built section:\n%s", id, data)
		}
		if got := story.FrozenHash([]byte(data)); got != rt.FileSHA256 {
			t.Errorf("%s: the frozen hash is %s, the record has %s", id, got, rt.FileSHA256)
		}
		asBuilt := story.AsBuiltText([]byte(data))
		assertContains(t, id+" As built", asBuilt, "Run `"+rt.RunID+"` in "+rt.Repo+": completed",
			"Branch `"+rt.Branch+"`", "commit "+rt.Commit[:7], "Conflicts with "+rec.Bases[rt.Repo].Branch+": none")
	}

	// Stacking: billing's RF02 on RF01's commit, RF04 on RF02's; gateway's RF03 on gateway's base.
	if in := f.input(t, "RF01"); in.BaseRef != bases["billing"] || in.BaseBranch != f.baseBranch(t, "billing") {
		t.Errorf("RF01 input = %+v, want billing's base %s", in, bases["billing"])
	}
	if in := f.input(t, "RF02"); in.BaseRef != rec.Tasks["RF01"].Commit || in.BaseRef == bases["billing"] {
		t.Errorf("RF02 base_ref = %s, want RF01's commit %s", in.BaseRef, rec.Tasks["RF01"].Commit)
	}
	if in := f.input(t, "RF03"); in.BaseRef != rec.Bases["gateway"].SHA || in.BaseRef != bases["gateway"] {
		t.Errorf("RF03 base_ref = %s, want gateway's base %s", in.BaseRef, bases["gateway"])
	}
	if in := f.input(t, "RF04"); in.BaseRef != rec.Tasks["RF02"].Commit {
		t.Errorf("RF04 base_ref = %s, want RF02's commit %s", in.BaseRef, rec.Tasks["RF02"].Commit)
	}
	// A dependency's As built reaches its dependents as context, across repositories too.
	in := f.input(t, "RF03")
	assertContains(t, "RF03 text", in.Text, "Story: Partial refunds", "Task RF03 (3 of 4) in gateway", "Do RF03.",
		"POST /v1/refunds takes an optional amount in cents.", "### RF01 (billing)", "story/refunds/RF01")
	if len(in.Acceptance) != 2 || in.Acceptance[0] != "the change is made" {
		t.Errorf("RF03 acceptance = %q", in.Acceptance)
	}

	readme := readFile(t, filepath.Join(f.specDir, "README.md"))
	assertContains(t, "README.md", readme, "Status: **completed** (4 of 4 tasks done)")
	if strings.Count(readme, "| [x] |") != 4 || strings.Contains(readme, "| [ ] |") {
		t.Errorf("README.md does not tick every task:\n%s", readme)
	}

	assertContains(t, "stdout", stdout, "Story plan: 4 tasks in 2 repositories", "── RF01 · billing · 1/4 ──",
		"── RF03 · gateway · 3/4 ──", "✓ Story completed: all 4 tasks done",
		"RF02 · billing · story/refunds/RF02 → story/refunds/RF01",
		"RF03 · gateway · story/refunds/RF03 → story/refunds/spec")
	f.assertCheckoutsUnchanged(t, before)

	// story status reads: nothing in the story or spec directory changes, and it shows the README.
	state := dirState(t, f.storyDir) + dirState(t, f.specDir)
	statusOut, statusErr, err := runOrchestrator(t, f.home, f.cfg, "story", "status", f.specDir)
	mustSucceed(t, statusOut, statusErr, err)
	if statusOut != readme {
		t.Errorf("story status printed\n%s\nwant the README\n%s", statusOut, readme)
	}
	if after := dirState(t, f.storyDir) + dirState(t, f.specDir); after != state {
		t.Errorf("story status wrote:\n--- before\n%s--- after\n%s", state, after)
	}

	// A story that is completed starts nothing.
	calls := invocations(t, f.invocationsLog)
	runs := f.runCount(t)
	stdout, stderr, err = f.run(t)
	mustSucceed(t, stdout, stderr, err)
	assertContains(t, "stdout", stdout, "✓ Story completed")
	if got := invocations(t, f.invocationsLog); len(got) != len(calls) || f.runCount(t) != runs {
		t.Errorf("a completed story ran again: %d calls (was %d), %d runs (was %d)", len(got), len(calls), f.runCount(t), runs)
	}
}

// A failing child stops the story; the next story run resumes that same run and goes on.
func TestStoryRun_FailThenResume(t *testing.T) {
	f := newStoryRunFixture(t)
	before := f.checkouts(t)
	writeResponse(t, f.responses, "review_1", storyReviewMalformed)

	stdout, stderr, err := f.run(t)
	if err == nil {
		t.Fatalf("a story with a failing child succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	assertWorkflowError(t, err)
	rec := f.record(t)
	rt := rec.Tasks["RF01"]
	if rec.Status != story.StatusFailed || rt.Status != story.TaskFailed || rt.RunID == "" {
		t.Fatalf("status = %s, RF01 = %+v; want a failed story and a failed task with its run", rec.Status, rt)
	}
	if _, started := rec.Tasks["RF02"]; started || len(f.inputFiles(t)) != 1 {
		t.Errorf("a task after the failed one was started: %v, inputs %v", rec.Tasks, f.inputFiles(t))
	}
	if run := f.runRecord(t, "billing", "RF01", 1); run.ID != rt.RunID || run.Status != domain.RunFailed {
		t.Errorf("run = %s %s, want the failed %s", run.ID, run.Status, rt.RunID)
	}
	assertContains(t, "stdout", stdout, "RF01: failed", "resumes run "+rt.RunID, "--retry RF01")
	assertContains(t, "README.md", readFile(t, filepath.Join(f.specDir, "README.md")), "Status: **failed** (0 of 4 tasks done)")
	if strings.Contains(readFile(t, f.taskPath("RF01")), "## As built") {
		t.Error("a failed task got an As built section")
	}

	// story status shows it, without writing.
	state := dirState(t, f.storyDir) + dirState(t, f.specDir)
	statusOut, _, err := runOrchestrator(t, f.home, f.cfg, "story", "status", f.specDir)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "story status", statusOut, "Status: **failed**", "| [ ] | RF01 |", "| failed |")
	if after := dirState(t, f.storyDir) + dirState(t, f.specDir); after != state {
		t.Errorf("story status wrote:\n--- before\n%s--- after\n%s", state, after)
	}

	// The next story run resumes that run (its second review is a good one) and completes it.
	runs := f.runCount(t)
	stdout, stderr, err = f.run(t)
	mustSucceed(t, stdout, stderr, err)
	assertContains(t, "stdout", stdout, "RF01: resuming run "+rt.RunID, "── RF01 · billing · 1/4 ──", "✓ Story completed")
	rec = f.record(t)
	if rec.Status != story.StatusCompleted || rec.Tasks["RF01"].Status != story.TaskDone || rec.Tasks["RF01"].RunID != rt.RunID {
		t.Fatalf("status = %s, RF01 = %+v; want RF01 done on the same run %s", rec.Status, rec.Tasks["RF01"], rt.RunID)
	}
	// RF01 was not dispatched again: only the three later tasks made runs.
	if got := f.runCount(t); got != runs+3 {
		t.Errorf("%d runs, want %d: the resume must not start a run", got, runs+3)
	}
	resumed := f.runRecord(t, "billing", "RF01", 1)
	if resumed.Status != domain.RunCompleted || len(resumed.Resumes) != 1 || resumed.Commit != rec.Tasks["RF01"].Commit {
		t.Errorf("RF01's run = %s with %d resumes and commit %s", resumed.Status, len(resumed.Resumes), resumed.Commit)
	}
	f.assertCheckoutsUnchanged(t, before)
}

// A task whose run completed without a commit is blocked, and counts as done once its branch has
// a commit beyond its base.
func TestStoryRun_BlockedCountsDoneOnceItsBranchHasACommit(t *testing.T) {
	f := newStoryRunFixture(t)
	before := f.checkouts(t)
	writeResponse(t, f.responses, "implement", storyImplementNoMessage)

	stdout, stderr, err := f.run(t)
	if err == nil {
		t.Fatalf("a story with a task without a commit succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	assertWorkflowError(t, err)
	rec := f.record(t)
	rt := rec.Tasks["RF01"]
	if rec.Status != story.StatusBlocked || rt.Status != story.TaskBlocked || rt.RunID == "" || rt.Commit != "" {
		t.Fatalf("status = %s, RF01 = %+v; want a blocked story and task, with a run and no commit", rec.Status, rt)
	}
	assertContains(t, "stdout", stdout, "RF01: blocked", "Commit the work on story/refunds/RF01 in ")

	// Nothing committed yet: it stays blocked, and nothing is dispatched.
	calls := invocations(t, f.invocationsLog)
	stdout, _, err = f.run(t)
	if err == nil {
		t.Fatalf("a blocked task counted as done without a commit\nstdout:\n%s", stdout)
	}
	if got := invocations(t, f.invocationsLog); len(got) != len(calls) {
		t.Errorf("a blocked story called %v after %v", got, calls)
	}
	if rec := f.record(t); rec.Status != story.StatusBlocked || rec.Tasks["RF01"].Status != story.TaskBlocked {
		t.Errorf("status = %s, RF01 = %s", rec.Status, rec.Tasks["RF01"].Status)
	}

	// The operator commits in the kept worktree, and the story goes on.
	run := f.runRecord(t, "billing", "RF01", 1)
	if run.Worktree == nil || run.Worktree.Removed {
		t.Fatalf("worktree = %+v, want it kept", run.Worktree)
	}
	gitRun(t, run.Worktree.Path, "add", "-A")
	gitRun(t, run.Worktree.Path, "commit", "-q", "-m", "operator's commit")
	writeResponse(t, f.responses, "implement", storyImplementResponse)

	stdout, stderr, err = f.run(t)
	mustSucceed(t, stdout, stderr, err)
	rec = f.record(t)
	rt = rec.Tasks["RF01"]
	tip, _ := gitOutput(f.repos["billing"], "rev-parse", "story/refunds/RF01")
	if rec.Status != story.StatusCompleted || rt.Status != story.TaskDone || rt.Commit != tip || rt.Commit == rt.BaseRef {
		t.Fatalf("status = %s, RF01 = %+v, branch tip %s; want done at the operator's commit", rec.Status, rt, tip)
	}
	if got := rec.Tasks["RF02"].BaseRef; got != tip {
		t.Errorf("RF02 is based on %s, want RF01's commit %s", got, tip)
	}
	assertContains(t, "RF01 As built", story.AsBuiltText([]byte(readFile(t, f.taskPath("RF01")))), "commit "+tip[:7])
	f.assertCheckoutsUnchanged(t, before)
}

// --retry abandons a failed task's run and runs it again on <branch>.2, re-reading its file; the
// tasks stacked on it follow it there.
func TestStoryRun_RetryDispatchesTheNextAttempt(t *testing.T) {
	f := newStoryRunFixture(t)
	before := f.checkouts(t)
	writeResponse(t, f.responses, "review_2", storyReviewMalformed) // RF01 passes, RF02's review fails

	stdout, stderr, err := f.run(t)
	if err == nil {
		t.Fatalf("a story with a failing child succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	rec := f.record(t)
	failed := rec.Tasks["RF02"]
	if rec.Tasks["RF01"].Status != story.TaskDone || failed.Status != story.TaskFailed || failed.Branch != "story/refunds/RF02" {
		t.Fatalf("RF01 = %+v, RF02 = %+v; want RF01 done and RF02 failed", rec.Tasks["RF01"], failed)
	}

	// A retry is the one way to change a dispatched task.
	path := f.taskPath("RF02")
	edited := readFile(t, path) + "\nAlso keep the amount rounded.\n"
	writeStoryFile(t, path, []byte(edited))

	stdout, stderr, err = f.run(t, "--retry", "RF02")
	mustSucceed(t, stdout, stderr, err)
	assertContains(t, "stdout", stdout, "RF02: run "+failed.RunID+" is abandoned", "attempt 2 goes on story/refunds/RF02.2")

	rec = f.record(t)
	rt := rec.Tasks["RF02"]
	if rec.Status != story.StatusCompleted || rt.Status != story.TaskDone || rt.Attempt != 2 || rt.Branch != "story/refunds/RF02.2" || rt.RunID == failed.RunID {
		t.Fatalf("status = %s, RF02 = %+v; want done on attempt 2 and story/refunds/RF02.2 with a new run", rec.Status, rt)
	}
	if tip, _ := gitOutput(f.repos["billing"], "rev-parse", "story/refunds/RF02.2"); tip != rt.Commit {
		t.Errorf("story/refunds/RF02.2 is at %s, want the commit %s", tip, rt.Commit)
	}
	if _, err := gitOutput(f.repos["billing"], "rev-parse", "--verify", "story/refunds/RF02"); err != nil {
		t.Errorf("the abandoned attempt's branch is gone: %v", err)
	}
	if old := f.runRecord(t, "billing", "RF02", 1); old.ID != failed.RunID || old.Status != domain.RunFailed {
		t.Errorf("the abandoned run = %s %s, want %s left failed as it was", old.ID, old.Status, failed.RunID)
	}

	in := f.input(t, "RF02")
	if in.Attempt != 2 || in.Branch != "story/refunds/RF02.2" || in.BaseRef != rec.Tasks["RF01"].Commit {
		t.Errorf("RF02 input = %+v, want attempt 2 on story/refunds/RF02.2 from RF01's commit", in)
	}
	assertContains(t, "RF02 text", in.Text, "Also keep the amount rounded.")
	// Its dependent is stacked on the retry's commit.
	if in := f.input(t, "RF04"); in.BaseRef != rt.Commit {
		t.Errorf("RF04 base_ref = %s, want the retry's commit %s", in.BaseRef, rt.Commit)
	}
	if parent, _ := gitOutput(f.repos["billing"], "rev-parse", rec.Tasks["RF04"].Commit+"^"); parent != rt.Commit {
		t.Errorf("RF04's commit has the parent %s, want %s", parent, rt.Commit)
	}
	data := readFile(t, path)
	if got := story.FrozenHash([]byte(data)); got != rt.FileSHA256 || !strings.Contains(data, "Branch `story/refunds/RF02.2`") {
		t.Errorf("RF02's file: hash %s, record %s:\n%s", got, rt.FileSHA256, data)
	}
	f.assertCheckoutsUnchanged(t, before)
}

// An edit above ## As built to a task that was dispatched is refused, naming it, and nothing new is
// written or started.
func TestStoryRun_RefusesAnEditedDispatchedTask(t *testing.T) {
	f := newStoryRunFixture(t)
	writeResponse(t, f.responses, "review_2", storyReviewMalformed)
	if _, _, err := f.run(t); err == nil {
		t.Fatal("a story with a failing child succeeded")
	}

	inputs, calls, runs := f.inputFiles(t), invocations(t, f.invocationsLog), f.runCount(t)
	recBefore := readFile(t, filepath.Join(f.storyDir, "story.json"))
	assertNothingNew := func() {
		t.Helper()
		if got := f.inputFiles(t); strings.Join(got, "|") != strings.Join(inputs, "|") {
			t.Errorf("task inputs %v, were %v", got, inputs)
		}
		if got := invocations(t, f.invocationsLog); len(got) != len(calls) || f.runCount(t) != runs {
			t.Errorf("a refused story ran: %v after %v, %d runs (was %d)", got, calls, f.runCount(t), runs)
		}
		if got := readFile(t, filepath.Join(f.storyDir, "story.json")); got != recBefore {
			t.Errorf("a refused story rewrote story.json:\n%s\nwas\n%s", got, recBefore)
		}
	}

	// RF01 is done: only reverting the change helps.
	original := readFile(t, f.taskPath("RF01"))
	edited := strings.Replace(original, "Do RF01.", "Do RF01 differently.", 1)
	if edited == original {
		t.Fatal("the edit changed nothing")
	}
	writeStoryFile(t, f.taskPath("RF01"), []byte(edited))
	_, _, err := f.run(t)
	assertConfigError(t, err, "RF01", "was changed after the task was dispatched")
	if strings.Contains(err.Error(), "--retry") {
		t.Errorf("a done task cannot be retried, but the error says so: %v", err)
	}
	assertNothingNew()
	writeStoryFile(t, f.taskPath("RF01"), []byte(original))

	// RF02 failed: a retry re-reads it.
	original = readFile(t, f.taskPath("RF02"))
	writeStoryFile(t, f.taskPath("RF02"), []byte(strings.Replace(original, "Do RF02.", "Do RF02 differently.", 1)))
	_, _, err = f.run(t)
	assertConfigError(t, err, "RF02", "was changed after the task was dispatched", "--retry RF02")
	assertNothingNew()

	// Below ## As built the file is the operator's: a note there is no change.
	writeStoryFile(t, f.taskPath("RF02"), []byte(original))
}

// A repository whose orchestrator validate fails stops the story before anything is written or
// started.
func TestStoryRun_ValidateFailureStopsBeforeTheFirstDispatch(t *testing.T) {
	f := newStoryRunFixture(t)
	writeStoryFile(t, filepath.Join(f.repos["gateway"], "orchestrator.yaml"), []byte("version: [\n"))
	before := f.checkouts(t)
	spec := dirState(t, f.specDir)

	stdout, _, err := f.run(t)
	assertConfigError(t, err, "orchestrator validate failed in service gateway", "no task was started")
	assertContains(t, "stdout", stdout, "Checking billing", "Checking gateway")
	f.assertNothingStarted(t)
	if after := dirState(t, f.specDir); after != spec {
		t.Errorf("the spec directory changed:\n--- before\n%s--- after\n%s", spec, after)
	}
	f.assertCheckoutsUnchanged(t, before)
}

// assertNothingStarted asserts that no task input, record or run exists and no provider was called.
func (f *storyRunFixture) assertNothingStarted(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(f.storyDir, "tasks")); !os.IsNotExist(err) {
		t.Errorf("tasks/ exists (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(f.storyDir, "story.json")); !os.IsNotExist(err) {
		t.Errorf("story.json exists (err=%v)", err)
	}
	if got := invocations(t, f.invocationsLog); len(got) != 0 {
		t.Errorf("a refused story called %v", got)
	}
	if n := f.runCount(t); n != 0 {
		t.Errorf("a refused story made %d runs", n)
	}
}

// What the checks of the first start refuse, each before anything is started.
func TestStoryRun_RefusalsBeforeTheFirstDispatch(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f *storyRunFixture)
		want  string
	}{
		{"a story branch the record does not know", func(t *testing.T, f *storyRunFixture) {
			gitRun(t, f.repos["billing"], "branch", "story/refunds/RF09")
		}, "story/refunds/RF09"},
		{"submodules", func(t *testing.T, f *storyRunFixture) {
			writeStoryFile(t, filepath.Join(f.repos["gateway"], ".gitmodules"), []byte("[submodule \"x\"]\n"))
		}, "has submodules"},
		{"a detached HEAD", func(t *testing.T, f *storyRunFixture) {
			gitRun(t, f.repos["gateway"], "checkout", "-q", "--detach")
		}, "is not on a branch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStoryRunFixture(t)
			tc.setup(t, f)
			before := f.checkouts(t)
			_, _, err := f.run(t)
			assertConfigError(t, err, tc.want)
			f.assertNothingStarted(t)
			f.assertCheckoutsUnchanged(t, before)
		})
	}
}

// story.max_cost_usd: the cost recorded so far - the planning calls' - is over it.
func TestStoryRun_RefusesOverTheBudget(t *testing.T) {
	f := newStoryRunFixture(t, func(o *storyRunFixtureOpts) { o.maxCostUSD = 0.01 })
	f.writePlanRecord(t) // the planning calls cost $0.05

	_, _, err := f.run(t)
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) || !strings.Contains(err.Error(), "story.max_cost_usd") {
		t.Fatalf("err = %T %v, want the budget refusal", err, err)
	}
	if exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Errorf("exit code = %d", exitcode.FromError(err))
	}
	if files := f.inputFiles(t); len(files) != 0 {
		t.Errorf("a story over its budget wrote %v", files)
	}
	if got := invocations(t, f.invocationsLog); len(got) != 0 {
		t.Errorf("a story over its budget called %v", got)
	}
}

// Only one story run owns a story: a second is refused while the lock is held.
func TestStoryRun_RefusesWhileTheLockIsHeld(t *testing.T) {
	f := newStoryRunFixture(t)
	unlock, err := persistence.LockStory(f.storyDir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	_, _, err = f.run(t)
	assertConfigError(t, err, "being run by another orchestrator process")
	if exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Errorf("exit code = %d", exitcode.FromError(err))
	}
	f.assertNothingStarted(t)
}

// Cancelling the context (Ctrl-C) while a child works reaches the child and leaves the task and the
// story stopped.
func TestStoryRun_CancelLeavesTheTaskStopped(t *testing.T) {
	f := newStoryRunFixture(t)
	before := f.checkouts(t)
	t.Setenv("FAKE_SLEEP_ACTION", "implement")
	t.Setenv("FAKE_SLEEP_SECONDS", "60")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		defer cancel()
		for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if data, err := os.ReadFile(f.invocationsLog); err == nil && strings.Contains(string(data), "implement") {
				time.Sleep(500 * time.Millisecond)
				return
			}
		}
	}()

	stdout, stderr, err := runOrchestratorCtx(t, ctx, f.home, f.cfg, "story", "run", f.specDir)
	if !errors.Is(err, context.Canceled) || exitcode.FromError(err) != exitcode.UserCancellation {
		t.Fatalf("err = %v (exit code %d), want a cancellation\nstdout:\n%s\nstderr:\n%s", err, exitcode.FromError(err), stdout, stderr)
	}
	rec := f.record(t)
	if rec.Status != story.StatusStopped || rec.Tasks["RF01"].Status != story.TaskStopped || rec.Tasks["RF01"].RunID == "" {
		t.Fatalf("status = %s, RF01 = %+v; want a stopped story and task with a run", rec.Status, rec.Tasks["RF01"])
	}
	if _, started := rec.Tasks["RF02"]; started {
		t.Error("a task after the stopped one was started")
	}
	assertContains(t, "stdout", stdout, "RF01: stopped", "resumes run "+rec.Tasks["RF01"].RunID)
	assertContains(t, "README.md", readFile(t, filepath.Join(f.specDir, "README.md")), "Status: **stopped**", "| stopped |")
	f.assertCheckoutsUnchanged(t, before)
}
