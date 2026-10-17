package integration

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/testutil/fakeexec"
)

// Worktree runs (specs/worktree/SPEC.md §7, §14, TASK-WT04): a workflow with implement under
// execution.isolation: worktree runs every step in its own git worktree and leaves the
// operator's tree, index and HEAD exactly as they were; a workflow without implement runs in
// place (O3).

// worktreeRunJSON is the part of run.json the worktree scenarios assert on.
type worktreeRunJSON struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	PreRunRef string `json:"pre_run_ref"`
	Options   struct {
		Isolation string `json:"isolation"`
	} `json:"options"`
	Worktree *struct {
		Path       string `json:"path"`
		Branch     string `json:"branch"`
		BaseRef    string `json:"base_ref"`
		BaseBranch string `json:"base_branch"`
		AdminDir   string `json:"admin_dir"`
	} `json:"worktree"`
	Executions []struct {
		Action string `json:"action"`
		Status string `json:"status"`
	} `json:"executions"`
}

func loadWorktreeRun(t *testing.T, repoDir string) worktreeRunJSON {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var run worktreeRunJSON
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

// newWorktreeRun is a stage run under execution.isolation: worktree, with the worktrees under a
// temporary state directory and the fake CLIs recording their argv and working directory. The
// fake implement fixes fixture.go and writes added.txt relative to its working directory.
func newWorktreeRun(t *testing.T, repoDir string) (r stageRun, argsDir, cwdDir string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	r = newStageRun(t, repoDir, func(o *configOptions) { o.isolation = "worktree" })
	argsDir, cwdDir = t.TempDir(), t.TempDir()
	t.Setenv("FAKE_ARGS_DIR", argsDir)
	t.Setenv("FAKE_CWD_DIR", cwdDir)
	t.Setenv("FAKE_FIX_TARGET_FILE", "fixture.go")
	t.Setenv("FAKE_IMPLEMENT_TOUCH_FILE", "added.txt")
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "implement", `{
  "summary": "fixed Add to use +",
  "summary_short": "Fix Add to use +",
  "changed_files": ["fixture.go", "added.txt"],
  "tests_added": []
}`)
	return r, argsDir, cwdDir
}

// settleTree moves every file of the working tree an hour into the past and lets git refresh
// the index once, so no later git status in the operator's tree has a reason to rewrite it: what
// the run must not change can then be compared byte for byte.
func settleTree(t *testing.T, dir string) {
	t.Helper()
	past := time.Now().Add(-time.Hour)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		return os.Chtimes(p, past, past)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(dir, "status", "--porcelain"); err != nil {
		t.Fatal(err)
	}
}

// operatorState is everything of the operator's checkout a worktree run must leave alone: every
// working-tree file, the index, HEAD and what git status says. The run directory is the one
// thing the run writes there (SPEC.md §7: the store stays in the operator's repository).
func operatorState(t *testing.T, dir string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel == ".git" || rel == ".orchestrator" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		state[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".git/index", ".git/HEAD"} {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		state[f] = string(data)
	}
	for name, args := range map[string][]string{
		"rev-parse HEAD": {"rev-parse", "HEAD"},
		"git status":     {"status", "--porcelain", "--untracked-files=all", "--", ".", ":(exclude).orchestrator"},
	} {
		out, err := gitOutput(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		state[name] = out
	}
	return state
}

func readTrimmed(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func resolved(t *testing.T, path string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWorktree_ImplementAndTestLeaveTheOperatorsTreeAlone(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	// The operator's work in progress: a change to a tracked file and an untracked file.
	// Neither is part of the run.
	f, err := os.OpenFile(filepath.Join(repoDir, "fixture.go"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n// the operator's work in progress\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.WriteFile(filepath.Join(repoDir, "notes.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, argsDir, cwdDir := newWorktreeRun(t, repoDir)
	settleTree(t, repoDir)
	head, err := gitOutput(repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	branch, err := gitOutput(repoDir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	before := operatorState(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	if after := operatorState(t, repoDir); !maps.Equal(after, before) {
		for k, v := range after {
			if before[k] != v {
				t.Errorf("the operator's %s changed:\nbefore: %q\nafter:  %q", k, before[k], v)
			}
		}
		for k := range before {
			if _, ok := after[k]; !ok {
				t.Errorf("the operator's %s is gone", k)
			}
		}
	}

	run := loadWorktreeRun(t, repoDir)
	if run.Status != "completed" || len(run.Executions) != 2 {
		t.Fatalf("status = %s, executions = %+v", run.Status, run.Executions)
	}
	w := run.Worktree
	if w == nil {
		t.Fatal("run.json has no worktree")
	}
	if want := "orchestrator/" + run.ID; w.Branch != want || w.BaseRef != head || w.BaseBranch != branch || w.AdminDir == "" {
		t.Errorf("worktree = %+v, want %s from %s@%s", *w, want, branch, head)
	}
	if run.Options.Isolation != "worktree" || run.PreRunRef != head {
		t.Errorf("options.isolation = %q, pre_run_ref = %q; want worktree and the base %s", run.Options.Isolation, run.PreRunRef, head)
	}
	if !strings.HasPrefix(w.Path, os.Getenv("XDG_STATE_HOME")) {
		t.Errorf("worktree path %s is not under the state directory", w.Path)
	}

	// The change is in the worktree, on top of HEAD: the fix, the new file, and not the
	// operator's work in progress.
	if got := readTrimmed(t, filepath.Join(w.Path, "fixture.go")); got != strings.TrimSpace(fakeexec.FixedFixtureSource) {
		t.Errorf("worktree fixture.go = %q, want the fix", got)
	}
	if got := readTrimmed(t, filepath.Join(w.Path, "added.txt")); got != "-- implement call 1" {
		t.Errorf("worktree added.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "notes.txt")); !os.IsNotExist(err) {
		t.Errorf("the operator's untracked file reached the worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "added.txt")); !os.IsNotExist(err) {
		t.Errorf("implement wrote into the operator's tree: %v", err)
	}

	// The provider ran in the worktree, with no deny rule for a runs directory it cannot reach.
	if got, want := readTrimmed(t, filepath.Join(cwdDir, "implement.cwd")), resolved(t, w.Path); got != want {
		t.Errorf("implement ran in %s, want the worktree %s", got, want)
	}
	assertNotContains(t, "implement's argv", readTrimmed(t, filepath.Join(argsDir, "implement.args")), ".orchestrator/runs")

	assertContains(t, "stdout", stdout,
		"Worktree: "+w.Path+" (orchestrator/"+run.ID+" from "+branch+"@"+head[:7]+")",
		"Restore point: "+head,
		"The changes are uncommitted in the worktree "+w.Path)
	assertContains(t, "stderr", stderr, "are not part of it (commit them first, or run with --inplace).")
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations = %v, want [implement]", got)
	}
}

// O3: review alone under worktree reviews the operator's uncommitted change, in place.
func TestWorktree_ReadOnlyWorkflowRunsInPlace(t *testing.T) {
	repoDir := dirtyFixture(t)
	r, _, cwdDir := newWorktreeRun(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "review", "review my change")
	mustSucceed(t, stdout, stderr, err)

	run := loadWorktreeRun(t, repoDir)
	if run.Status != "completed" || run.Worktree != nil || run.Options.Isolation != "inplace" {
		t.Errorf("status = %s, worktree = %+v, options.isolation = %q; want completed, none, inplace",
			run.Status, run.Worktree, run.Options.Isolation)
	}
	if got, want := readTrimmed(t, filepath.Join(cwdDir, "review.cwd")), resolved(t, repoDir); got != want {
		t.Errorf("review ran in %s, want the operator's tree %s", got, want)
	}
	if strings.Contains(stdout, "Worktree: ") {
		t.Errorf("a read-only run announced a worktree:\n%s", stdout)
	}
	if list, err := gitOutput(repoDir, "worktree", "list", "--porcelain"); err != nil || strings.Count(list, "worktree ") != 1 {
		t.Errorf("worktrees = %q, %v; want only the operator's", list, err)
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"review"}) {
		t.Errorf("invocations = %v, want [review]", got)
	}
}
