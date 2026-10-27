package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/persistence"
	"orchestrator/internal/story"
)

// The spec branch and the merge of several parents (specs/parallel-story/SPEC.md §1, §3). One
// repository, billing, and a spec of three tasks:
//
//	RF01  billing                   based on the story base
//	RF02  billing                   based on the story base
//	RF03  billing  RF01 and RF02    based on story/refunds/spec, where their commits are merged
//
// Each task of the story adds a file of its own (FAKE_IMPLEMENT_BRANCH_DIR), so the merge is clean,
// or writes the same file (FAKE_IMPLEMENT_BRANCH_FILE), so it conflicts.

var storyMergeTasks = []story.Task{
	{ID: "RF01", Repo: "billing", Title: "Store the refunded amount"},
	{ID: "RF02", Repo: "billing", Title: "Round the refunded amount"},
	{ID: "RF03", Repo: "billing", Title: "Report the refunded amounts", DependsOn: []string{"RF01", "RF02"}},
}

// storyMergeImplement names only the file every task of these stories has: the others differ per
// task.
const storyMergeImplement = `{
  "summary": "fixed Add to use +",
  "summary_short": "Fix Add to use +",
  "changed_files": ["fixture.go"],
  "tests_added": []
}`

const specBranch = "story/" + storyRunSlug + "/spec"

// newStoryMergeFixture is a story of storyMergeTasks whose tasks each add a file of their own, so
// that merging their commits is clean.
func newStoryMergeFixture(t *testing.T) *storyRunFixture {
	t.Helper()
	f := newStoryRunFixture(t, func(o *storyRunFixtureOpts) { o.tasks = storyMergeTasks })
	writeResponse(t, f.responses, "implement", storyMergeImplement)
	t.Setenv("FAKE_IMPLEMENT_TOUCH_FILE", "")
	t.Setenv("FAKE_IMPLEMENT_BRANCH_DIR", ".")
	return f
}

// git is git in a repository of the fixture, failing the test on an error.
func (f *storyRunFixture) git(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := gitOutput(f.repos[repo], args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, repo, err)
	}
	return out
}

func TestStoryRun_SeveralParentsAreMergedOnTheSpecBranch(t *testing.T) {
	f := newStoryMergeFixture(t)
	before := f.checkouts(t)
	base := f.head(t, "billing")

	stdout, stderr, err := f.run(t)
	mustSucceed(t, stdout, stderr, err)

	rec := f.record(t)
	if rec.Status != story.StatusCompleted {
		t.Fatalf("status = %s, want completed", rec.Status)
	}
	if b := rec.Bases["billing"]; b.SpecBranch != specBranch || b.SHA != base {
		t.Errorf("bases.billing = %+v, want the spec branch %s on the base %s", b, specBranch, base)
	}

	// RF01 and RF02 have no parent: the story base. RF03 starts from the spec branch's tip, a merge
	// of the two commits.
	for _, id := range []string{"RF01", "RF02"} {
		if in := f.input(t, id); in.BaseRef != base {
			t.Errorf("%s base_ref = %s, want the story base %s", id, in.BaseRef, base)
		}
	}
	rf01, rf02 := rec.Tasks["RF01"].Commit, rec.Tasks["RF02"].Commit
	in := f.input(t, "RF03")
	if tip := f.git(t, "billing", "rev-parse", specBranch); in.BaseRef != tip || rec.Tasks["RF03"].BaseRef != tip {
		t.Errorf("RF03 base_ref = %s (record %s), want the tip of %s: %s", in.BaseRef, rec.Tasks["RF03"].BaseRef, specBranch, tip)
	}
	if in.BaseBranch != specBranch {
		t.Errorf("RF03 base_branch = %s, want its merge request's target, the spec branch %s", in.BaseBranch, specBranch)
	}
	parents := strings.Fields(f.git(t, "billing", "rev-list", "--parents", "-n", "1", in.BaseRef))
	if len(parents) != 3 || parents[1] != rf01 || parents[2] != rf02 {
		t.Errorf("the merge commit %s has the parents %v, want RF01's %s and RF02's %s", in.BaseRef, parents[1:], rf01, rf02)
	}
	assertContains(t, "the merge commit's message", f.git(t, "billing", "log", "-1", "--format=%B", in.BaseRef),
		"Merge story/refunds/RF02 for RF03", "Orchestrator-Story: refunds/RF03")
	if parent := f.git(t, "billing", "rev-parse", rec.Tasks["RF03"].Commit+"^"); parent != in.BaseRef {
		t.Errorf("RF03's commit has the parent %s, want the merge commit %s", parent, in.BaseRef)
	}
	// The merged work is all in RF03's tree.
	for _, file := range []string{"story-refunds-RF01.txt", "story-refunds-RF02.txt", "story-refunds-RF03.txt"} {
		f.git(t, "billing", "cat-file", "-e", rec.Tasks["RF03"].Commit+":"+file)
	}

	// The base and the target of RF03 are the spec branch.
	assertContains(t, "stdout", stdout, "RF03 · billing · story/refunds/RF03 or by billing's rules · from story/refunds/spec (RF01 + RF02)",
		"RF01 · billing · story/refunds/RF01 → story/refunds/spec", "RF03 · billing · story/refunds/RF03 → story/refunds/spec")
	assertContains(t, "RF03 As built", story.AsBuiltText([]byte(readFile(t, f.taskPath("RF03")))),
		"on `story/refunds/spec (RF01 + RF02)`")
	f.assertCheckoutsUnchanged(t, before)
}

// The spec branch exists from the first start, at the story base commit, whatever the tasks do.
func TestStoryRun_SpecBranchExistsFromTheFirstStart(t *testing.T) {
	f := newStoryMergeFixture(t)
	base := f.head(t, "billing")
	writeResponse(t, f.responses, "review_1", storyReviewMalformed)

	if _, _, err := f.run(t); err == nil {
		t.Fatal("a story with a failing child succeeded")
	}
	rec := f.record(t)
	if rec.Tasks["RF01"].Status != story.TaskFailed {
		t.Fatalf("RF01 = %+v, want failed", rec.Tasks["RF01"])
	}
	if got := rec.Bases["billing"].SpecBranch; got != specBranch {
		t.Errorf("bases.billing.spec_branch = %q, want %s", got, specBranch)
	}
	if tip := f.git(t, "billing", "rev-parse", specBranch); tip != base {
		t.Errorf("%s is at %s, want the story base %s", specBranch, tip, base)
	}
}

// A record from before the spec branch continues: the branch is created at the base, with a note,
// before anything is dispatched. A branch the record names and git lacks stops the story.
func TestStoryRun_ContinuesARecordWithoutASpecBranch(t *testing.T) {
	f := newStoryMergeFixture(t)
	base := f.head(t, "billing")
	writeResponse(t, f.responses, "review_1", storyReviewMalformed)
	if _, _, err := f.run(t); err == nil {
		t.Fatal("a story with a failing child succeeded")
	}

	// The record names the spec branch, git lacks it: nothing is dispatched.
	f.git(t, "billing", "branch", "-D", specBranch)
	calls, runs := invocations(t, f.invocationsLog), f.runCount(t)
	_, _, err := f.run(t)
	assertConfigError(t, err, "the spec branch "+specBranch, "does not exist any more")
	if got := invocations(t, f.invocationsLog); len(got) != len(calls) || f.runCount(t) != runs {
		t.Errorf("a story without its spec branch ran: %v after %v", got, calls)
	}

	// A record from before: no spec_branch in it.
	rec := f.record(t)
	b := rec.Bases["billing"]
	b.SpecBranch, b.SpecPush = "", nil
	rec.Bases["billing"] = b
	if err := persistence.SaveStoryRecord(f.storyDir, rec); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := f.run(t)
	mustSucceed(t, stdout, stderr, err)
	assertContains(t, "stdout", stdout, "created the spec branch "+specBranch)
	rec = f.record(t)
	if rec.Status != story.StatusCompleted || rec.Bases["billing"].SpecBranch != specBranch {
		t.Fatalf("status = %s, bases.billing = %+v; want completed with the spec branch", rec.Status, rec.Bases["billing"])
	}
	// It was created at the story base, and RF03 merged its parents on it.
	if !strings.HasPrefix(f.git(t, "billing", "log", "-1", "--format=%B", specBranch), "Merge story/refunds/RF02 for RF03") {
		t.Errorf("the spec branch's tip is not RF03's merge")
	}
	if err := exec.Command("git", "-C", f.repos["billing"], "merge-base", "--is-ancestor", base, specBranch).Run(); err != nil {
		t.Errorf("the spec branch does not descend from the story base: %v", err)
	}
}

// Parents that do not merge stop the story with the task in the conflict status, and nothing is
// dispatched for it. After the operator merges them on the spec branch the next story run goes
// on from that tip.
func TestStoryRun_ConflictStopsForAManualMerge(t *testing.T) {
	f := newStoryMergeFixture(t)
	t.Setenv("FAKE_IMPLEMENT_BRANCH_DIR", "")
	t.Setenv("FAKE_IMPLEMENT_BRANCH_FILE", "shared.txt") // RF01 and RF02 add it with different content

	stdout, stderr, err := f.run(t)
	if err == nil {
		t.Fatalf("a story with parents that conflict succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	assertWorkflowError(t, err)
	rec := f.record(t)
	rt := rec.Tasks["RF03"]
	if rec.Status != story.StatusBlocked || rt.Status != story.TaskConflict || rt.Conflict == nil {
		t.Fatalf("status = %s, RF03 = %+v; want a blocked story and a conflict task", rec.Status, rt)
	}
	if strings.Join(rt.Conflict.Parents, ",") != "RF01,RF02" || strings.Join(rt.Conflict.Files, ",") != "shared.txt" {
		t.Errorf("RF03's conflict = %+v, want RF01 and RF02 in shared.txt", *rt.Conflict)
	}
	if rt.RunID != "" || rt.Attempt != 0 || rt.BaseRef != "" {
		t.Errorf("RF03 = %+v: a conflict task has no attempt, run or base", rt)
	}
	for _, in := range f.inputFiles(t) {
		if strings.HasSuffix(in, "RF03.json") {
			t.Errorf("an input was written for the conflict task: %s", in)
		}
	}
	if n := f.runCount(t); n != 2 {
		t.Errorf("%d runs, want RF01's and RF02's", n)
	}
	assertContains(t, "stdout", stdout, "RF03: conflict (RF01 + RF02 do not merge cleanly on story/refunds/spec)", "shared.txt",
		"worktree add <dir> story/refunds/spec", "git merge story/refunds/RF01", "git merge story/refunds/RF02", "worktree remove <dir>", "orchestrator story run")
	assertContains(t, "README.md", readFile(t, filepath.Join(f.specDir, "README.md")), "Status: **blocked**", "| conflict |")
	// RF01's commit was fast-forwarded onto the spec branch; the conflicting merge wrote nothing.
	if tip := f.git(t, "billing", "rev-parse", specBranch); tip != rec.Tasks["RF01"].Commit {
		t.Errorf("%s is at %s, want RF01's commit %s", specBranch, tip, rec.Tasks["RF01"].Commit)
	}

	// Run again without merging: the same conflict, nothing dispatched.
	calls := invocations(t, f.invocationsLog)
	if stdout, _, err = f.run(t); err == nil {
		t.Fatalf("a conflict that was not merged succeeded\nstdout:\n%s", stdout)
	}
	assertContains(t, "stdout", stdout, "RF03: conflict")
	if got := invocations(t, f.invocationsLog); len(got) != len(calls) {
		t.Errorf("a conflict was dispatched: %v after %v", got, calls)
	}
	if rec := f.record(t); rec.Status != story.StatusBlocked || rec.Tasks["RF03"].Status != story.TaskConflict {
		t.Errorf("status = %s, RF03 = %s", rec.Status, rec.Tasks["RF03"].Status)
	}

	// The operator's merge, as the hint says: a worktree of the spec branch, git merge, a commit.
	wt := filepath.Join(t.TempDir(), "spec")
	gitRun(t, f.repos["billing"], "worktree", "add", "-q", wt, specBranch)
	gitRun(t, wt, "merge", "story/refunds/RF01") // already in the spec branch
	merge := exec.Command("git", "merge", "story/refunds/RF02")
	merge.Dir = wt
	if out, err := merge.CombinedOutput(); err == nil {
		t.Fatalf("git merge of the conflicting parents succeeded:\n%s", out)
	}
	writeStoryFile(t, filepath.Join(wt, "shared.txt"), []byte("resolved\n"))
	gitRun(t, wt, "add", "-A")
	gitRun(t, wt, "commit", "-q", "-m", "merge RF01 and RF02 by hand")
	gitRun(t, f.repos["billing"], "worktree", "remove", wt)
	merged := f.git(t, "billing", "rev-parse", specBranch)

	stdout, stderr, err = f.run(t)
	mustSucceed(t, stdout, stderr, err)
	rec = f.record(t)
	rt = rec.Tasks["RF03"]
	if rec.Status != story.StatusCompleted || rt.Status != story.TaskDone || rt.Conflict != nil {
		t.Fatalf("status = %s, RF03 = %+v; want completed with RF03 done", rec.Status, rt)
	}
	if in := f.input(t, "RF03"); in.BaseRef != merged || rt.BaseRef != merged {
		t.Errorf("RF03 base_ref = %s (record %s), want the operator's merge %s", in.BaseRef, rt.BaseRef, merged)
	}
	if tip := f.git(t, "billing", "rev-parse", specBranch); tip != merged {
		t.Errorf("%s is at %s, want it left at the operator's merge %s: Go made no merge of its own", specBranch, tip, merged)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the operator's worktree is still there: %v", err)
	}
}

// One parent is stacked on its commit, as before: no merge commit is made and the spec branch
// stays at the base.
func TestStoryRun_AChainIsStackedWithoutAMerge(t *testing.T) {
	chain := []story.Task{
		{ID: "RF01", Repo: "billing", Title: "Store the refunded amount"},
		{ID: "RF02", Repo: "billing", Title: "Round the refunded amount", DependsOn: []string{"RF01"}},
	}
	f := newStoryRunFixture(t, func(o *storyRunFixtureOpts) { o.tasks = chain })
	base := f.head(t, "billing")

	stdout, stderr, err := f.run(t)
	mustSucceed(t, stdout, stderr, err)

	rec := f.record(t)
	if in := f.input(t, "RF02"); in.BaseRef != rec.Tasks["RF01"].Commit {
		t.Errorf("RF02 base_ref = %s, want RF01's commit %s", in.BaseRef, rec.Tasks["RF01"].Commit)
	}
	if merges := f.git(t, "billing", "rev-list", "--merges", "--all"); merges != "" {
		t.Errorf("a chain made merge commits: %s", merges)
	}
	if tip := f.git(t, "billing", "rev-parse", specBranch); tip != base {
		t.Errorf("%s is at %s, want it untouched at the base %s", specBranch, tip, base)
	}
	assertContains(t, "stdout", stdout, "RF02 · billing · story/refunds/RF02 → story/refunds/RF01")
}
