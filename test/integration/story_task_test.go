package integration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// run --story-task (specs/story/SPEC.md §11, TASK-ST05): a child run of a story gets its task,
// branch, base and review criteria from one validated input file, always runs in a worktree
// with development on, and records everything so that its resume needs no file.

const storyReviewPass = `{"status":"pass","summary":"ok","summary_short":"ok","issues":[],"criteria":[` +
	`{"index":1,"met":true,"evidence":"fixture.go:4"},{"index":2,"met":true,"evidence":"fixture.go:4"}]}`

// storyRunJSON is the part of run.json the story task scenarios assert on.
type storyRunJSON struct {
	worktreeRunJSON
	Task struct {
		Description string   `json:"description"`
		Acceptance  []string `json:"acceptance"`
		Source      *struct {
			Kind       string `json:"kind"`
			Story      string `json:"story"`
			Task       string `json:"task"`
			Attempt    int    `json:"attempt"`
			Jira       string `json:"jira"`
			Branch     string `json:"branch"`
			BaseRef    string `json:"base_ref"`
			BaseBranch string `json:"base_branch"`
		} `json:"source"`
	} `json:"task"`
}

func loadStoryRun(t *testing.T, repoDir string) storyRunJSON {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var run storyRunJSON
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

// storyRun is a setup for a story task: a repository whose HEAD is one commit past the task's
// base, fake providers, and a fake Jev that rates development under its threshold. The config
// says execution.isolation: inplace, which a story task overrides.
type storyRun struct {
	repoDir, cfg, invocationsLog, promptsDir string
	// base is the commit the task input names, the repository's first one; head is HEAD.
	base, head, baseBranch string
	input                  string
	jev                    *fakeJev
}

func newStoryRun(t *testing.T, mutate ...func(in map[string]any)) storyRun {
	t.Helper()
	repoDir := fakeexec.InitGitFixture(t)
	// A story task is a worktree run that pushes its branch: origin answers like GitLab.
	fakeexec.AddGitLabRemote(t, repoDir)
	base, err := gitOutput(repoDir, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "later.txt"), []byte("a later commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, repoDir, "later")
	head, err := gitOutput(repoDir, "rev-parse", "HEAD")
	if err != nil || head == base {
		t.Fatalf("HEAD = %q (%v), want a commit past the base %s", head, err, base)
	}
	baseBranch, err := gitOutput(repoDir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_STATE_HOME", t.TempDir())
	invocationsLog, promptsDir := decisionEnv(t, repoDir)
	// Medium: the template gives planning and review; development is rated 0.1, under its 0.6.
	j := startJev(t, jevScript{
		PProject: 0.9,
		Choice:   [3]float64{0.1, 0.7, 0.2},
		Stages:   map[string]float64{"development": 0.1},
	})
	cfg := jevConfig(t, j)

	t.Setenv("FAKE_CWD_DIR", t.TempDir())
	t.Setenv("FAKE_FIX_TARGET_FILE", "fixture.go")
	t.Setenv("FAKE_IMPLEMENT_TOUCH_FILE", "added.txt")
	responses := os.Getenv("FAKE_RESPONSES_DIR")
	writeResponse(t, responses, "implement", `{
  "summary": "fixed Add to use +",
  "summary_short": "Fix Add to use +",
  "changed_files": ["fixture.go", "added.txt"],
  "tests_added": []
}`)
	writeResponse(t, responses, "review", storyReviewPass)

	in := map[string]any{}
	in["story"], in["task"], in["attempt"] = "refunds", "RF02", 1
	in["title"] = "Fix Add in fixture.go"
	in["branch"], in["base_ref"], in["base_branch"] = "story/refunds/RF02", base, baseBranch
	in["jira"] = "PAY-124"
	in["acceptance"] = []string{"an amount over the payment is a 422", "the amount is optional"}
	in["text"] = "RF02: fix Add in fixture.go so that it adds"
	for _, m := range mutate {
		m(in)
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return storyRun{
		repoDir: repoDir, cfg: cfg, invocationsLog: invocationsLog, promptsDir: promptsDir,
		base: base, head: head, baseBranch: baseBranch, input: writeStoryInput(t, data), jev: j,
	}
}

func writeStoryInput(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "RF02.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// noRunDirectory asserts the refusal left no run behind.
func noRunDirectory(t *testing.T, repoDir string) {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(repoDir, ".orchestrator", "runs", "*"))
	if err != nil || len(dirs) != 0 {
		t.Errorf("a refused run left %v (err=%v)", dirs, err)
	}
}

func TestStoryTask_RunsOnItsBranchAndBaseWithDevelopmentForced(t *testing.T) {
	r := newStoryRun(t)

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--story-task", r.input)
	mustSucceed(t, stdout, stderr, err)

	// Development is under its threshold and still runs; decision.json says why.
	d := readDecision(t, r.repoDir)
	if !equalStrings(d.Workflow, []string{"implement", "test", "review"}) {
		t.Errorf("workflow = %v, want implement, test, review: the task file is the plan", d.Workflow)
	}
	// A story task is never a story: the scope question is not asked (specs/story/SPEC.md §13).
	if d.Scope != nil {
		t.Errorf("decision.json scope = %+v, want none for a story task", d.Scope)
	}
	for i, body := range r.jev.Bodies() {
		if strings.Contains(body, "scope_story") {
			t.Errorf("Jev request %d asks the scope question of a story task:\n%s", i, body)
		}
	}
	for _, s := range d.Stages {
		switch s.Stage {
		case "development":
			if !s.Enabled || !s.Forced || s.Reason != "story task" {
				t.Errorf("development = %+v, want enabled and forced with the reason %q", s, "story task")
			}
		case "testing":
			if !s.Enabled || s.Reason != "story task" {
				t.Errorf("testing = %+v, want enabled with the reason %q", s, "story task")
			}
		case "review":
			if !s.Enabled {
				t.Errorf("review = %+v, want the template's review back", s)
			}
		}
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}

	// run.json: a worktree run on the input's branch, base and base branch.
	run := loadStoryRun(t, r.repoDir)
	if run.Status != "completed" || run.Commit == "" {
		t.Fatalf("status = %s, commit = %q\nstdout:\n%s\nstderr:\n%s", run.Status, run.Commit, stdout, stderr)
	}
	w := run.Worktree
	if w == nil {
		t.Fatal("run.json has no worktree")
	}
	if w.Branch != "story/refunds/RF02" || w.BaseRef != r.base || w.BaseBranch != r.baseBranch {
		t.Errorf("worktree = %+v, want story/refunds/RF02 from %s@%s", *w, r.baseBranch, r.base)
	}
	if run.Options.Isolation != "worktree" || run.PreRunRef != r.base {
		t.Errorf("options.isolation = %q, pre_run_ref = %q; want worktree and %s", run.Options.Isolation, run.PreRunRef, r.base)
	}
	src := run.Task.Source
	if src == nil || src.Kind != "story" || src.Story != "refunds" || src.Task != "RF02" || src.Attempt != 1 || src.Jira != "PAY-124" ||
		src.Branch != "story/refunds/RF02" || src.BaseRef != r.base || src.BaseBranch != r.baseBranch {
		t.Errorf("task.source = %+v", src)
	}
	if !equalStrings(run.Task.Acceptance, []string{"an amount over the payment is a 422", "the amount is optional"}) {
		t.Errorf("task.acceptance = %q", run.Task.Acceptance)
	}
	if run.Task.Description != "RF02: fix Add in fixture.go so that it adds" {
		t.Errorf("task.description = %q", run.Task.Description)
	}

	// The commit is on the given branch, on top of the given base, not on top of HEAD.
	if tip, _ := gitOutput(r.repoDir, "rev-parse", "story/refunds/RF02"); tip != run.Commit {
		t.Errorf("story/refunds/RF02 = %s, want the run's commit %s", tip, run.Commit)
	}
	if parent, _ := gitOutput(r.repoDir, "rev-parse", run.Commit+"^"); parent != r.base {
		t.Errorf("the commit's parent = %s, want the base %s", parent, r.base)
	}
	if got, _ := gitOutput(r.repoDir, "rev-parse", "HEAD"); got != r.head {
		t.Errorf("the operator's HEAD moved to %s", got)
	}
	if _, err := os.Stat(filepath.Join(r.repoDir, "added.txt")); !os.IsNotExist(err) {
		t.Errorf("implement wrote into the operator's tree: %v", err)
	}

	// The commit message: the Jira key as the prefix and the run, decision and story trailers.
	want := "PAY-124: Fix Add to use +\n\nOrchestrator-Run: " + run.ID +
		"\nOrchestrator-Decision: medium/development,testing,review\nOrchestrator-Story: refunds/RF02"
	if msg, _ := gitOutput(r.repoDir, "log", "-1", "--format=%B", run.Commit); msg != want {
		t.Errorf("commit message = %q, want %q", msg, want)
	}

	// The task's criteria are the review's, numbered from 1.
	prompt := readFile(t, filepath.Join(r.promptsDir, "review.txt"))
	assertContains(t, "the review prompt", prompt,
		"1. an amount over the payment is a 422\n2. the amount is optional\n", `"criteria": [`)
	assertContains(t, "stdout", stdout, "Worktree: ")
}

// A child that failed after implement continues, from run.json alone, on the same branch and
// with the same review criteria (SPEC §11.2).
func TestStoryTask_ResumeKeepsTheBranchAndTheCriteria(t *testing.T) {
	r := newStoryRun(t)
	responses := os.Getenv("FAKE_RESPONSES_DIR")
	// One verdict where the task's two criteria need two: the review is malformed and the run fails.
	writeResponse(t, responses, "review", `{"status":"pass","summary":"ok","issues":[],"criteria":[`+
		`{"index":1,"met":true,"evidence":"fixture.go:4"}]}`)

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--story-task", r.input)
	if err == nil {
		t.Fatalf("a run with a malformed review succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	failed := loadStoryRun(t, r.repoDir)
	if failed.Status != "failed" || failed.Worktree == nil || failed.Worktree.Branch != "story/refunds/RF02" {
		t.Fatalf("status = %s, worktree = %+v; want a failed run on its branch", failed.Status, failed.Worktree)
	}
	assertContains(t, "the first review prompt", readFile(t, filepath.Join(r.promptsDir, "review.txt")),
		"1. an amount over the payment is a 422\n2. the amount is optional\n")
	if err := os.Remove(filepath.Join(r.promptsDir, "review.txt")); err != nil {
		t.Fatal(err)
	}

	// The input file is gone: the resume needs none.
	if err := os.Remove(r.input); err != nil {
		t.Fatal(err)
	}
	writeResponse(t, responses, "review", storyReviewPass)
	stdout, stderr, err = resume(t, r.repoDir, r.cfg, failed.ID)
	mustSucceed(t, stdout, stderr, err)

	run := loadStoryRun(t, r.repoDir)
	if run.Status != "completed" || run.Commit == "" {
		t.Fatalf("status = %s, commit = %q\nstdout:\n%s\nstderr:\n%s", run.Status, run.Commit, stdout, stderr)
	}
	if run.Worktree == nil || run.Worktree.Branch != "story/refunds/RF02" || run.Worktree.BaseRef != r.base {
		t.Errorf("worktree = %+v, want the same branch and base", run.Worktree)
	}
	if tip, _ := gitOutput(r.repoDir, "rev-parse", "story/refunds/RF02"); tip != run.Commit {
		t.Errorf("story/refunds/RF02 = %s, want the run's commit %s", tip, run.Commit)
	}
	assertContains(t, "the resumed review prompt", readFile(t, filepath.Join(r.promptsDir, "review.txt")),
		"1. an amount over the payment is a 422\n2. the amount is optional\n")
	if msg, _ := gitOutput(r.repoDir, "log", "-1", "--format=%B", run.Commit); !strings.HasPrefix(msg, "PAY-124: Fix Add to use +") ||
		!strings.Contains(msg, "Orchestrator-Story: refunds/RF02") {
		t.Errorf("commit message = %q", msg)
	}
}

// The input's base branch is where the change finally goes; it need not exist in this
// repository, and a conflict check against nothing is skipped, not a failure of the run.
func TestStoryTask_AMissingBaseBranchSkipsTheConflictCheck(t *testing.T) {
	r := newStoryRun(t, func(in map[string]any) { in["base_branch"] = "release-9" })

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--story-task", r.input)
	mustSucceed(t, stdout, stderr, err)

	run := loadStoryRun(t, r.repoDir)
	if run.Status != "completed" || run.Commit == "" || run.Worktree == nil || run.Worktree.BaseBranch != "release-9" {
		t.Fatalf("status = %s, commit = %q, worktree = %+v", run.Status, run.Commit, run.Worktree)
	}
	if strings.Contains(stdout, "older than 2.38") {
		t.Skip("git has no merge-tree --write-tree")
	}
	assertContains(t, "stdout", stdout, "no conflict check against release-9")
}

func TestStoryTask_RefusalsCreateNoRun(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in map[string]any)
		before func(t *testing.T, r storyRun)
		args   func(r storyRun) []string
		want   string
	}{
		{name: "unknown field", mutate: func(in map[string]any) { in["extra"] = 1 }, want: `"extra" is not a field`},
		{name: "text over 32 KiB", mutate: func(in map[string]any) { in["text"] = strings.Repeat("x", 32<<10+1) }, want: "over the 32768-byte limit"},
		{name: "branch of another task", mutate: func(in map[string]any) { in["branch"] = "story/refunds/RF03" }, want: "branch"},
		{name: "short base_ref", mutate: func(in map[string]any) { in["base_ref"] = "HEAD" }, want: "base_ref"},
		{name: "unknown base_ref", mutate: func(in map[string]any) { in["base_ref"] = strings.Repeat("a", 40) }, want: "not a commit in this repository"},
		{
			name: "existing branch",
			before: func(t *testing.T, r storyRun) {
				if _, err := gitOutput(r.repoDir, "branch", "story/refunds/RF02", r.base); err != nil {
					t.Fatal(err)
				}
			},
			want: "branch story/refunds/RF02 already exists",
		},
		{name: "--stage", args: func(r storyRun) []string { return []string{"--stage", "review"} }, want: "cannot be combined with --stage"},
		{name: "--jira", args: func(r storyRun) []string { return []string{"--jira", "PMC-1"} }, want: "cannot be combined with --jira"},
		{name: "--inplace", args: func(r storyRun) []string { return []string{"--inplace"} }, want: "cannot be combined with --inplace"},
		{name: "--worktree", args: func(r storyRun) []string { return []string{"--worktree"} }, want: "cannot be combined with --worktree"},
		{name: "positional text", args: func(r storyRun) []string { return []string{"fix it"} }, want: "cannot be combined with task text"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newStoryRun(t, func(in map[string]any) {
				if tc.mutate != nil {
					tc.mutate(in)
				}
			})
			if tc.before != nil {
				tc.before(t, r)
			}
			args := []string{"run", "--story-task", r.input}
			if tc.args != nil {
				args = append(args, tc.args(r)...)
			}

			stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, args...)
			var cfgErr *apperr.ConfigError
			if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %T %v, want a config error containing %q\nstdout:\n%s\nstderr:\n%s", err, err, tc.want, stdout, stderr)
			}
			noRunDirectory(t, r.repoDir)
			if got := invocations(t, r.invocationsLog); len(got) != 0 {
				t.Errorf("a refused run called %v", got)
			}
		})
	}
}

func TestStoryTask_OnlyWithRun(t *testing.T) {
	r := newStoryRun(t)
	_, _, err := runOrchestrator(t, r.repoDir, r.cfg, "validate", "--story-task", r.input)
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) || err.Error() != "--story-task is only valid with run" {
		t.Errorf("err = %v", err)
	}
	if _, _, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--story-task"); err == nil {
		t.Error("--story-task without a file was accepted")
	}
	noRunDirectory(t, r.repoDir)
}
