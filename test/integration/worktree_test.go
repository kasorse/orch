package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
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
	Commit    string `json:"commit"`
	Options   struct {
		Isolation string `json:"isolation"`
	} `json:"options"`
	Worktree *struct {
		Path       string   `json:"path"`
		Branch     string   `json:"branch"`
		BaseRef    string   `json:"base_ref"`
		BaseBranch string   `json:"base_branch"`
		AdminDir   string   `json:"admin_dir"`
		Conflicts  []string `json:"conflicts"`
		Removed    bool     `json:"removed"`
	} `json:"worktree"`
	MR *struct {
		State   string `json:"state"`
		Remote  string `json:"remote"`
		Source  string `json:"source"`
		Target  string `json:"target"`
		IID     int    `json:"iid"`
		URL     string `json:"url"`
		Skipped string `json:"skipped"`
		Error   string `json:"error"`
	} `json:"mr"`
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

// newWorktreeRun is a stage run under the default execution.isolation (worktree: the config does
// not say), with the worktrees under a temporary state directory and the fake CLIs recording
// their argv and working directory. The fake implement fixes fixture.go and writes added.txt
// relative to its working directory. mutate changes the rest of the config.
func newWorktreeRun(t *testing.T, repoDir string, mutate ...func(*configOptions)) (r stageRun, argsDir, cwdDir string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	r = newStageRun(t, repoDir, append([]func(*configOptions){func(o *configOptions) { o.isolation = isolationAbsent }}, mutate...)...)
	// The run ends by pushing its branch: origin is a bare repository that answers like GitLab.
	r.remote = fakeexec.AddGitLabRemote(t, repoDir)
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

// resolvedGone is resolved for a path that may no longer exist, such as a removed worktree:
// it resolves the nearest existing ancestor and appends the rest.
func resolvedGone(t *testing.T, path string) string {
	t.Helper()
	dir, rest := filepath.Clean(path), ""
	for {
		if _, err := os.Lstat(dir); err == nil {
			return filepath.Join(resolved(t, dir), rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no part of %s exists", path)
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
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

	// The change is committed on the run's branch, on top of HEAD: the fix, the new file, and
	// not the operator's work in progress (SPEC.md §10.1).
	tip, err := gitOutput(repoDir, "rev-parse", w.Branch)
	if err != nil || run.Commit == "" || tip != run.Commit {
		t.Fatalf("%s = %q (%v), run.json commit = %q; want the run's commit", w.Branch, tip, err, run.Commit)
	}
	if parent, _ := gitOutput(repoDir, "rev-parse", run.Commit+"^"); parent != head {
		t.Errorf("the commit's parent = %s, want the base %s", parent, head)
	}
	if msg, _ := gitOutput(repoDir, "log", "-1", "--format=%B", run.Commit); msg != "Fix Add to use +\n\nOrchestrator-Run: "+run.ID+"\nOrchestrator-Decision: manual/development,testing" {
		t.Errorf("commit message = %q", msg)
	}
	if got, _ := gitOutput(repoDir, "show", run.Commit+":fixture.go"); got != strings.TrimSpace(fakeexec.FixedFixtureSource) {
		t.Errorf("committed fixture.go = %q, want the fix", got)
	}
	if got, _ := gitOutput(repoDir, "show", run.Commit+":added.txt"); got != "-- implement call 1" {
		t.Errorf("committed added.txt = %q", got)
	}
	if files, _ := gitOutput(repoDir, "show", "--name-only", "--format=", run.Commit); files != "added.txt\nfixture.go" {
		t.Errorf("committed files = %q, want added.txt and fixture.go only", files)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "added.txt")); !os.IsNotExist(err) {
		t.Errorf("implement wrote into the operator's tree: %v", err)
	}
	if got, _ := gitOutput(repoDir, "symbolic-ref", "--short", "HEAD"); got != branch {
		t.Errorf("the operator's branch = %s, want %s", got, branch)
	}

	// After the commit the worktree is gone and the branch stays (SPEC.md §10.4).
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Errorf("the worktree %s is still there: %v", w.Path, err)
	}
	if list, _ := gitOutput(repoDir, "worktree", "list", "--porcelain"); strings.Contains(list, w.Path) {
		t.Errorf("git still lists the worktree:\n%s", list)
	}
	if !w.Removed || len(w.Conflicts) != 0 {
		t.Errorf("worktree.removed = %v, conflicts = %q; want removed, none", w.Removed, w.Conflicts)
	}
	summary, _ := runFile(t, repoDir, "summary.md")
	assertContains(t, "summary.md", summary,
		"| Worktree | orchestrator/"+run.ID+" from "+branch+"@"+head[:7]+"; commit "+run.Commit[:7]+" |")

	// The provider ran in the worktree, with no deny rule for a runs directory it cannot reach.
	if got, want := readTrimmed(t, filepath.Join(cwdDir, "implement.cwd")), resolvedGone(t, w.Path); got != want {
		t.Errorf("implement ran in %s, want the worktree %s", got, want)
	}
	assertNotContains(t, "implement's argv", readTrimmed(t, filepath.Join(argsDir, "implement.args")), ".orchestrator/runs")

	assertContains(t, "stdout", stdout,
		"Worktree: "+w.Path+" (orchestrator/"+run.ID+" from "+branch+"@"+head[:7]+")",
		"Restore point: "+head,
		"Committed "+run.Commit[:12]+" to orchestrator/"+run.ID+": Fix Add to use +\n"+
			"Based on "+branch+"@"+head[:7]+". To take it:\n  git merge orchestrator/"+run.ID+"\n")
	assertNotContains(t, "stdout", stdout, "The worktree stays at")
	assertContains(t, "stderr", stderr, "are not part of it (commit them first, or run with --inplace).")
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations = %v, want [implement]", got)
	}
}

// A review that keeps failing stops the run at max_fix_iterations under --mode auto: nothing is
// committed, and the worktree stays for a resume, its path printed (SPEC.md §10.1, §10.4).
func TestWorktree_FailingReviewKeepsTheWorktree(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "review", reviewFailResponse)
	head, err := gitOutput(repoDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "--stage", "review", "fix Add in fixture.go")
	if err == nil {
		t.Fatalf("a run whose review keeps failing succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}

	run := loadWorktreeRun(t, repoDir)
	w := run.Worktree
	if w == nil {
		t.Fatal("run.json has no worktree")
	}
	if run.Status == "completed" || run.Commit != "" || w.Removed {
		t.Errorf("status = %s, commit = %q, removed = %v; want no commit and the worktree kept", run.Status, run.Commit, w.Removed)
	}
	if _, err := os.Stat(w.Path); err != nil {
		t.Errorf("the worktree is gone: %v", err)
	}
	if tip, _ := gitOutput(repoDir, "rev-parse", w.Branch); tip != head {
		t.Errorf("%s = %s, want the base %s", w.Branch, tip, head)
	}
	assertContains(t, "stdout", stdout, "The worktree stays at "+w.Path+"\n")
	assertNotContains(t, "stdout", stdout, "Committed ")
}

// The operator's branch moves during the run with a change to the same line: the conflict
// check names the file and run.json records it (SPEC.md §10.3). The test command plays the
// operator, committing on their branch while the run is in its test step.
func TestWorktree_ConflictWithTheMovedBranchIsReported(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	moved := filepath.Join(t.TempDir(), "moved.go")
	if err := os.WriteFile(moved, []byte("package fixture\n\nfunc Add(a, b int) int {\n\treturn b - a\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testCommand := "cp '" + moved + "' '" + filepath.Join(repoDir, "fixture.go") + "' && git -C '" + repoDir +
		"' commit -qam 'the operator moves on' && go test ./..."
	r, _, _ := newWorktreeRun(t, repoDir, func(o *configOptions) { o.testCommand = testCommand })
	branch, err := gitOutput(repoDir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)
	if strings.Contains(stdout, "older than 2.38") {
		t.Skip("git has no merge-tree --write-tree")
	}

	run := loadWorktreeRun(t, repoDir)
	w := run.Worktree
	if run.Status != "completed" || run.Commit == "" || w == nil {
		t.Fatalf("status = %s, commit = %q, worktree = %+v", run.Status, run.Commit, w)
	}
	if !equalStrings(w.Conflicts, []string{"fixture.go"}) {
		t.Errorf("worktree.conflicts = %q, want [fixture.go]", w.Conflicts)
	}
	assertContains(t, "stdout", stdout,
		"Warning: merging orchestrator/"+run.ID+" into "+branch+" will conflict in 1 file:\n  fixture.go\n")
	summary, _ := runFile(t, repoDir, "summary.md")
	assertContains(t, "summary.md", summary, "; conflicts: fixture.go")
}

// A pre-commit hook that refuses the commit: the run stays completed with no commit, and the
// worktree with its changes stays (SPEC.md §10.1, §10.4). Under --mode auto nobody declined, so
// nothing is pushed and the exit code is 6 (specs/delivery/SPEC.md §7.4).
func TestWorktree_CommitRefusedByAHookKeepsTheWorktree(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	hook := filepath.Join(repoDir, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'commits are refused here' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, _, _ := newWorktreeRun(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	if exitcode.FromError(err) != exitcode.NeedsPerson {
		t.Fatalf("exit code %d (%v), want %d: completed, but needs a person\nstdout:\n%s\nstderr:\n%s", exitcode.FromError(err), err, exitcode.NeedsPerson, stdout, stderr)
	}

	run := loadWorktreeRun(t, repoDir)
	w := run.Worktree
	if run.Status != "completed" || run.Commit != "" || w == nil || w.Removed {
		t.Fatalf("status = %s, commit = %q, worktree = %+v; want completed, no commit, kept", run.Status, run.Commit, w)
	}
	if run.MR == nil || run.MR.State != "" || !strings.HasPrefix(run.MR.Skipped, "the commit failed: ") {
		t.Errorf("mr = %+v, want skipped for the failed commit", run.MR)
	}
	if got, _ := gitOutput(r.remote, "branch", "--list"); got != "" {
		t.Errorf("the remote has branches %q after a run that committed nothing", got)
	}
	assertContains(t, "stdout", stdout, "No push and no merge request: the commit failed: ")
	if got := readTrimmed(t, filepath.Join(w.Path, "fixture.go")); got != strings.TrimSpace(fakeexec.FixedFixtureSource) {
		t.Errorf("worktree fixture.go = %q, want the fix", got)
	}
	assertContains(t, "stdout", stdout, "Commit failed: ", "commits are refused here", "The worktree stays at "+w.Path+"\n")
	summary, _ := runFile(t, repoDir, "summary.md")
	assertContains(t, "summary.md", summary, "; kept at "+w.Path+" |")
}

// A worktree run that stopped at its test step resumes in its worktree (SPEC.md §11,
// TASK-WT07): meanwhile the operator switched branches, committed, and left their tree dirty;
// the resume completes and commits on the run's branch and leaves that tree byte for byte as
// it was.
func TestWorktree_ResumeContinuesInTheWorktree(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	// The tests fail until the marker, outside both trees, exists.
	marker := filepath.Join(t.TempDir(), "tests-may-pass")
	testCommand := "test -f '" + marker + "' && go test ./..."
	r, _, cwdDir := newWorktreeRun(t, repoDir, func(o *configOptions) {
		o.testCommand = testCommand
		o.maxFixIterations = 1
	})
	branch, err := gitOutput(repoDir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	if err == nil {
		t.Fatalf("a run whose tests keep failing succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	stopped := loadWorktreeRun(t, repoDir)
	w := stopped.Worktree
	if w == nil || stopped.Status == "completed" || w.Removed || len(stopped.Executions) == 0 ||
		stopped.Executions[len(stopped.Executions)-1].Action != "test" {
		t.Fatalf("status = %s, worktree = %+v, executions = %+v; want a run stopped at test with its worktree", stopped.Status, w, stopped.Executions)
	}
	assertContains(t, "stdout", stdout, "The worktree stays at "+w.Path+"\n")

	// The operator moves on in their own tree: another branch, a commit, and work in progress.
	gitRun(t, repoDir, "switch", "-q", "-c", "other")
	if err := os.WriteFile(filepath.Join(repoDir, "operator.txt"), []byte("the operator's own work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repoDir, "add", "operator.txt")
	gitRun(t, repoDir, "commit", "-qm", "the operator's commit")
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
	settleTree(t, repoDir)
	before := operatorState(t, repoDir)

	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	raised := jevConfig(t, r.jev, func(o *configOptions) {
		o.isolation = "worktree"
		o.testCommand = testCommand
		o.maxFixIterations = 3
	})
	stdout, stderr, err = resume(t, repoDir, raised, stopped.ID)
	mustSucceed(t, stdout, stderr, err)

	if after := operatorState(t, repoDir); !maps.Equal(after, before) {
		for k, v := range after {
			if before[k] != v {
				t.Errorf("the resume changed the operator's %s:\nbefore: %q\nafter:  %q", k, before[k], v)
			}
		}
		for k := range before {
			if _, ok := after[k]; !ok {
				t.Errorf("the resume removed the operator's %s", k)
			}
		}
	}

	run := loadWorktreeRun(t, repoDir)
	if run.Status != "completed" || len(run.Executions) <= len(stopped.Executions) {
		t.Fatalf("status = %s, executions = %+v; want completed with the history appended", run.Status, run.Executions)
	}
	if resumed := loadResumedRun(t, repoDir); len(resumed.Resumes) != 1 {
		t.Errorf("resumes = %+v, want one", resumed.Resumes)
	}
	rw := run.Worktree
	if rw == nil || rw.Path != w.Path || !rw.Removed || len(rw.Conflicts) != 0 {
		t.Fatalf("worktree = %+v, want the same one, removed, no conflicts", rw)
	}
	tip, err := gitOutput(repoDir, "rev-parse", rw.Branch)
	if err != nil || run.Commit == "" || tip != run.Commit {
		t.Fatalf("%s = %q (%v), run.json commit = %q; want the run's commit", rw.Branch, tip, err, run.Commit)
	}
	if parent, _ := gitOutput(repoDir, "rev-parse", run.Commit+"^"); parent != rw.BaseRef {
		t.Errorf("the commit's parent = %s, want the base %s", parent, rw.BaseRef)
	}
	if got, _ := gitOutput(repoDir, "show", run.Commit+":fixture.go"); got != strings.TrimSpace(fakeexec.FixedFixtureSource) {
		t.Errorf("committed fixture.go = %q, want the fix without the operator's work", got)
	}
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Errorf("the worktree %s is still there: %v", w.Path, err)
	}
	// The resumed implement ran in the worktree, not in the operator's tree.
	if got, want := readTrimmed(t, filepath.Join(cwdDir, "implement.cwd")), resolvedGone(t, w.Path); got != want {
		t.Errorf("the resumed implement ran in %s, want the worktree %s", got, want)
	}
	assertContains(t, "stdout", stdout,
		"Worktree: "+w.Path+" (orchestrator/"+run.ID+" from "+branch+"@"+rw.BaseRef[:7]+")",
		"Committed "+run.Commit[:12]+" to orchestrator/"+run.ID+": Fix Add to use +\n")
	// The operator's fingerprint, taken at the resume, lets the conflict check run.
	assertNotContains(t, "stdout", stdout, "no conflict check")
}

// --mode auto asks nothing: the run commits, pushes its branch with the merge request's options
// to a remote that answers like GitLab, records the merge request and removes the worktree
// (specs/delivery/SPEC.md §7). The remote receives exactly the options Go built.
func TestWorktree_AutoPushesAndOpensTheMergeRequest(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)
	branch, err := gitOutput(repoDir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	run := loadWorktreeRun(t, repoDir)
	const url = "https://" + fakeexec.GitLabHost + "/group/project/-/merge_requests/1"
	if run.Status != "completed" || run.Commit == "" || run.Worktree == nil || !run.Worktree.Removed {
		t.Fatalf("status = %s, commit = %q, worktree = %+v; want completed, committed, removed", run.Status, run.Commit, run.Worktree)
	}
	mr := run.MR
	if mr == nil || mr.State != "created" || mr.IID != 1 || mr.URL != url || mr.Remote != "origin" || mr.Source != run.Worktree.Branch ||
		mr.Target != branch || mr.Skipped != "" || mr.Error != "" {
		t.Fatalf("mr = %+v, want created as !1 at %s from %s to %s", mr, url, run.Worktree.Branch, branch)
	}
	if tip, err := gitOutput(r.remote, "rev-parse", run.Worktree.Branch); err != nil || tip != run.Commit {
		t.Errorf("the remote's %s = %q (%v), want the run's commit %s", run.Worktree.Branch, tip, err, run.Commit)
	}

	options := fakeexec.GitLabPushOptions(t, r.remote)
	if len(options) != 4 || options[0] != "merge_request.create" || options[1] != "merge_request.target="+branch ||
		options[2] != "merge_request.title=Fix Add to use +" || !strings.HasPrefix(options[3], `merge_request.description=## fix Add in fixture.go\n\n`) {
		t.Fatalf("the remote received %q", options)
	}
	description := strings.ReplaceAll(strings.TrimPrefix(options[3], "merge_request.description="), `\n`, "\n")
	assertContains(t, "the description", description, "**Passed stages:** development, testing", "Run `"+run.ID+"`", "commit `"+run.Commit[:7]+"`")
	assertNotContains(t, "the description", description, "merge request [")

	assertContains(t, "stdout", stdout, "Merge request !1: "+url+"\n")
	report, _ := runFile(t, repoDir, "report.md")
	assertContains(t, "report.md", report, "merge request [!1]("+url+")")
	summary, _ := runFile(t, repoDir, "summary.md")
	assertContains(t, "summary.md", summary, "| Merge request | !1 "+url+" |")
}

// Any other mode asks before the commit, the push and the merge request: yes, yes, no is a push
// without options, a branch on the remote and no merge request, and exit 0.
func TestWorktree_AskedYesYesNoPushesWithoutAMergeRequest(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)

	stdout, stderr, err := runOrchestratorInteractive(t, repoDir, r.cfg, "a\ny\ny\nn\n",
		"--mode", "accept-edits", "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	run := loadWorktreeRun(t, repoDir)
	if run.Status != "completed" || run.Commit == "" || run.Worktree == nil || !run.Worktree.Removed {
		t.Fatalf("status = %s, commit = %q, worktree = %+v", run.Status, run.Commit, run.Worktree)
	}
	if mr := run.MR; mr == nil || mr.State != "pushed" || mr.Skipped != "declined at merge request" || mr.Error != "" || mr.URL != "" {
		t.Errorf("mr = %+v, want pushed, declined at merge request", mr)
	}
	if tip, err := gitOutput(r.remote, "rev-parse", run.Worktree.Branch); err != nil || tip != run.Commit {
		t.Errorf("the remote's %s = %q (%v), want the run's commit %s", run.Worktree.Branch, tip, err, run.Commit)
	}
	if options := fakeexec.GitLabPushOptions(t, r.remote); len(options) != 0 {
		t.Errorf("the remote received push options %q", options)
	}
	assertContains(t, "stdout", stdout, "Push "+run.Worktree.Branch+" to origin? [y]es / [N]o: ",
		"Open a merge request "+run.Worktree.Branch+" → ", "Pushed "+run.Worktree.Branch+" to origin.\n")
}

// A no to the commit question: nothing is pushed, the worktree stays with its changes, exit 0.
func TestWorktree_AskedNoToTheCommitPushesNothing(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)

	stdout, stderr, err := runOrchestratorInteractive(t, repoDir, r.cfg, "a\nn\n",
		"--mode", "accept-edits", "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	run := loadWorktreeRun(t, repoDir)
	if run.Status != "completed" || run.Commit != "" || run.Worktree == nil || run.Worktree.Removed {
		t.Fatalf("status = %s, commit = %q, worktree = %+v; want completed, no commit, kept", run.Status, run.Commit, run.Worktree)
	}
	if mr := run.MR; mr == nil || mr.State != "" || mr.Skipped != "declined at commit" {
		t.Errorf("mr = %+v, want skipped: declined at commit", mr)
	}
	if got, _ := gitOutput(r.remote, "branch", "--list"); got != "" {
		t.Errorf("the remote has branches %q", got)
	}
	if _, err := os.Stat(run.Worktree.Path); err != nil {
		t.Errorf("the worktree is gone: %v", err)
	}
	assertNotContains(t, "stdout", stdout, "Push ", "No push and no merge request")
}

// A bare repository that takes no push options refuses the push: the intent stays, the error and
// the command to push by hand are shown, the exit code is 6, the run stays completed.
func TestWorktree_PushOptionsRefusedNeedsAPerson(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)
	gitRun(t, r.remote, "config", "receive.advertisePushOptions", "false")

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	if exitcode.FromError(err) != exitcode.NeedsPerson {
		t.Fatalf("exit code %d (%v), want %d\nstdout:\n%s\nstderr:\n%s", exitcode.FromError(err), err, exitcode.NeedsPerson, stdout, stderr)
	}

	run := loadWorktreeRun(t, repoDir)
	if run.Status != "completed" || run.Commit == "" || run.Worktree == nil {
		t.Fatalf("status = %s, commit = %q, worktree = %+v; want completed and committed", run.Status, run.Commit, run.Worktree)
	}
	if mr := run.MR; mr == nil || mr.State != "intent" || !strings.Contains(mr.Error, "does not support push options") || mr.URL != "" {
		t.Errorf("mr = %+v, want the intent kept with git's error", mr)
	}
	assertContains(t, "stdout", stdout, "does not support push options", "To push it yourself: git push -u origin "+run.Worktree.Branch+"\n")
	if got, _ := gitOutput(r.remote, "branch", "--list"); got != "" {
		t.Errorf("the remote has branches %q", got)
	}
	if tip, err := gitOutput(repoDir, "rev-parse", run.Worktree.Branch); err != nil || tip != run.Commit {
		t.Errorf("%s = %q (%v), want the run's commit: the branch holds the result", run.Worktree.Branch, tip, err)
	}
}

// A worktree that may not be the run's own is never resumed (SPEC.md §11): a rewritten .git
// pointer, a run.json without the worktree, a removed directory. Each refusal leaves run.json
// byte for byte as it was and calls no provider.
func TestWorktree_ResumeRefusesAWorktreeItCannotTrust(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)
	t.Setenv("FAKE_FAIL_ACTION", "implement")
	if _, _, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go"); err == nil {
		t.Fatal("expected implement to fail the run")
	}
	t.Setenv("FAKE_FAIL_ACTION", "")
	failed := loadWorktreeRun(t, repoDir)
	w := failed.Worktree
	if failed.Status != "failed" || w == nil || len(failed.Executions) == 0 {
		t.Fatalf("status = %s, worktree = %+v, executions = %+v", failed.Status, w, failed.Executions)
	}
	calls := len(invocations(t, r.invocationsLog))
	path := runJSONPath(t, repoDir)

	refused := func(what, want string) {
		t.Helper()
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = resume(t, repoDir, r.cfg, failed.ID)
		var rerr *apperr.RunStateError
		if !errors.As(err, &rerr) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: got %T %v, want a RunStateError naming %q", what, err, err, want)
		}
		if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
			t.Errorf("%s: the refused resume changed run.json", what)
		}
		if got := len(invocations(t, r.invocationsLog)); got != calls {
			t.Errorf("%s: the refused resume called a provider (%d calls, was %d)", what, got, calls)
		}
	}

	pointerFile := filepath.Join(w.Path, ".git")
	pointer, err := os.ReadFile(pointerFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pointerFile, []byte("gitdir: "+filepath.Join(repoDir, ".git", "worktrees", "elsewhere")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refused("pointer rewritten", ".git (worktree pointer)")
	if err := os.WriteFile(pointerFile, pointer, 0o644); err != nil {
		t.Fatal(err)
	}

	original := editRunJSON(t, repoDir, func(run map[string]any) { delete(run, "worktree") })
	refused("no worktree recorded", "no worktree is recorded")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(w.Path); err != nil {
		t.Fatal(err)
	}
	refused("directory removed", "the run's worktree "+w.Path+" is gone; start a new run")
}

// A setup that fails stops the run before any step: no model ran, the output is in
// logs/setup.*.log and the worktree stays (SPEC.md §9).
func TestWorktree_FailingSetupStopsBeforeAnyStep(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir, func(o *configOptions) {
		o.extraYAML = "\nworktree:\n  setup: \"echo setup-broke >&2; exit 3\"\n"
	})

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	if err == nil {
		t.Fatalf("a run whose setup fails succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	assertContains(t, "error", err.Error(), "worktree setup failed (exit 3)")

	run := loadWorktreeRun(t, repoDir)
	if run.Status == "completed" || run.Worktree == nil || len(run.Executions) != 0 {
		t.Fatalf("status = %s, worktree = %+v, executions = %d; want a failed run with no step", run.Status, run.Worktree, len(run.Executions))
	}
	if log, _ := runFile(t, repoDir, "logs/setup.stderr.log"); !strings.Contains(log, "setup-broke") {
		t.Errorf("logs/setup.stderr.log = %q, want the setup's output", log)
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("invocations = %v, want none", got)
	}
}

// A setup that changes the shared git config fails the run: the config and hooks are the
// operator's (SPEC.md §9, Guard).
func TestWorktree_SetupChangingGitConfigFailsTheRun(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir, func(o *configOptions) {
		o.extraYAML = "\nworktree:\n  setup: \"git config core.hooksPath /tmp/planted\"\n"
	})

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	if err == nil {
		t.Fatalf("a setup that changed .git/config succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("invocations = %v, want none", got)
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

// run --worktree makes one run a worktree run under execution.isolation: inplace, and the
// operator's tree keeps none of the change.
func TestWorktree_FlagOverridesInplaceConfig(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir, func(o *configOptions) { o.isolation = "inplace" })
	settleTree(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--worktree", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	run := loadWorktreeRun(t, repoDir)
	if run.Status != "completed" || run.Options.Isolation != "worktree" || run.Worktree == nil {
		t.Fatalf("status = %s, options.isolation = %q, worktree = %+v; want completed, worktree, recorded",
			run.Status, run.Options.Isolation, run.Worktree)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "added.txt")); !os.IsNotExist(err) {
		t.Errorf("the change landed in the operator's tree: %v", err)
	}
}

// run --inplace makes one run an in-place run under the default isolation: the change lands in
// the operator's tree and no worktree is created (specs/delivery/SPEC.md §6).
func TestWorktree_InplaceFlagOptsOutOfTheDefault(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--inplace", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	run := loadWorktreeRun(t, repoDir)
	if run.Status != "completed" || run.Options.Isolation != "inplace" || run.Worktree != nil {
		t.Errorf("status = %s, options.isolation = %q, worktree = %+v; want completed, inplace, none",
			run.Status, run.Options.Isolation, run.Worktree)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "added.txt")); err != nil {
		t.Errorf("the change did not land in the operator's tree: %v", err)
	}
}

// A worktree run that could not deliver is refused before its worktree: exit 2 naming --inplace,
// no worktree, no step, and with --stage no provider call (specs/delivery/SPEC.md §8).
func TestWorktree_RefusedBeforeTheWorktreeWhenItCouldNotDeliver(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, repoDir string)
		mutate func(o *configOptions)
		want   string
	}{
		{"no remote", func(t *testing.T, repoDir string) { gitRun(t, repoDir, "remote", "remove", "origin") }, nil, `remote "origin"`},
		{"detached HEAD without a target", func(t *testing.T, repoDir string) { gitRun(t, repoDir, "checkout", "-q", "--detach") }, nil, "delivery.target_branch"},
		{"secret scan off", func(t *testing.T, repoDir string) {},
			func(o *configOptions) { o.security += "  secret_scan:\n    mode: \"off\"\n" }, "security.secret_scan.mode is off"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := fakeexec.InitGitFixture(t)
			var mutate []func(*configOptions)
			if tc.mutate != nil {
				mutate = append(mutate, tc.mutate)
			}
			r, _, _ := newWorktreeRun(t, repoDir, mutate...)
			tc.setup(t, repoDir)

			stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
			if err == nil {
				t.Fatalf("a run that could not deliver succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			var cfgErr *apperr.ConfigError
			if !errors.As(err, &cfgErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
				t.Fatalf("got %T (exit %d) %v, want a config error with exit 2", err, exitcode.FromError(err), err)
			}
			assertContains(t, "error", err.Error(), tc.want, "--inplace")

			run := loadWorktreeRun(t, repoDir)
			if run.Worktree != nil || len(run.Executions) != 0 {
				t.Errorf("worktree = %+v, executions = %d; want neither", run.Worktree, len(run.Executions))
			}
			if list, _ := gitOutput(repoDir, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
				t.Errorf("a worktree was added:\n%s", list)
			}
			if got := invocations(t, r.invocationsLog); len(got) != 0 {
				t.Errorf("invocations = %v, want none", got)
			}
			// Not even the clarity question a --stage run asks the decision provider.
			if bodies := r.jev.Bodies(); len(bodies) != 0 {
				t.Errorf("Jev got %d requests %v, want none", len(bodies), bodies)
			}
		})
	}
}

// A detached HEAD delivers when delivery.target_branch names the target.
func TestWorktree_DetachedHeadWithATargetBranchRuns(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir, func(o *configOptions) { o.extraYAML += "delivery:\n  target_branch: main\n" })
	gitRun(t, repoDir, "checkout", "-q", "--detach")

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)
	if run := loadWorktreeRun(t, repoDir); run.Status != "completed" || run.Worktree == nil {
		t.Errorf("status = %s, worktree = %+v; want completed in a worktree", run.Status, run.Worktree)
	}
	// The counter the refusals above read: a run that passes the checks asks the clarity question.
	if bodies := r.jev.Bodies(); len(bodies) != 1 || !clarityOnly(bodies[0]) {
		t.Errorf("Jev got %v, want the clarity question alone", bodies)
	}
}

// A workflow without implement runs in place, so a repository without a remote is fine.
func TestWorktree_ReadOnlyWorkflowNeedsNoRemote(t *testing.T) {
	repoDir := dirtyFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)
	gitRun(t, repoDir, "remote", "remove", "origin")

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "review", "review my change")
	mustSucceed(t, stdout, stderr, err)
	if run := loadWorktreeRun(t, repoDir); run.Status != "completed" || run.Worktree != nil {
		t.Errorf("status = %s, worktree = %+v; want completed in place", run.Status, run.Worktree)
	}
}

// In a worktree run "{source}" in extensions.mcp names the operator's repository, where an
// ignored file such as a generated knowledge graph lies, and "{repo}" the worktree.
func TestWorktree_SourceNamesTheOperatorsRepository(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, argsDir, _ := newWorktreeRun(t, repoDir, func(o *configOptions) {
		o.extraYAML += "extensions:\n  mcp:\n    graph:\n      command: true\n" +
			"      args: [\"{source}/graphify-out/graph.json\", \"{repo}\"]\n" +
			"      tools: [query_graph]\n      actions: [implement]\n"
	})
	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	run := loadWorktreeRun(t, repoDir)
	if run.Worktree == nil {
		t.Fatal("the run has no worktree")
	}
	args := readTrimmed(t, filepath.Join(argsDir, "implement.args"))
	want, _ := json.Marshal([]string{filepath.Join(repoDir, "graphify-out", "graph.json"), run.Worktree.Path})
	if !strings.Contains(args, `"args":`+string(want)) {
		t.Errorf("implement's --mcp-config does not name the operator's graph and the worktree %s:\n%s", want, args)
	}
}
