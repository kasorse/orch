package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/story"
)

// Parallel dispatch (specs/parallel-story/tasks/TASK-PS03-parallel-dispatch.md): story run --parallel
// N runs up to N ready tasks at once, each as its own child with its output in a log file.

func parallelTasks(repos ...string) []story.Task {
	var tasks []story.Task
	for i, repo := range repos {
		id := "RF0" + string(rune('1'+i))
		tasks = append(tasks, story.Task{ID: id, Repo: repo, Title: "Task " + id})
	}
	return tasks
}

func withTasks(tasks []story.Task) func(*storyRunFixtureOpts) {
	return func(o *storyRunFixtureOpts) { o.tasks = tasks }
}

// holdReview makes task id's review call wait - up to 25 seconds, under the provider's timeout -
// until when holds of the story's record, which the test reads every 100 ms. The record that let
// the review go is returned by the func, false if it never came: what the story run had recorded
// while the task was still running.
func holdReview(t *testing.T, f *storyRunFixture, id string, when func(persistence.StoryRecord) bool) func() (persistence.StoryRecord, bool) {
	t.Helper()
	release := filepath.Join(t.TempDir(), "release")
	t.Setenv("FAKE_SLEEP_ACTION", "review")
	t.Setenv("FAKE_SLEEP_BRANCH", "story/"+storyRunSlug+"/"+id)
	t.Setenv("FAKE_SLEEP_SECONDS", "25")
	t.Setenv("FAKE_SLEEP_UNTIL_FILE", release)

	seen := make(chan persistence.StoryRecord, 1)
	quit, finished := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(quit); <-finished })
	go func() {
		defer close(finished)
		for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			select {
			case <-quit:
				return
			default:
			}
			rec, err := persistence.LoadStoryRecord(filepath.Join(f.storyDir, "story.json"))
			if err == nil && when(rec) {
				seen <- rec
				_ = os.WriteFile(release, nil, 0o644)
				return
			}
		}
	}()
	return func() (persistence.StoryRecord, bool) {
		select {
		case rec := <-seen:
			return rec, true
		default:
			return persistence.StoryRecord{}, false
		}
	}
}

// assertNotStarted asserts that a task got no input, no run and no start line.
func assertNotStarted(t *testing.T, f *storyRunFixture, rec persistence.StoryRecord, stdout, id string) {
	t.Helper()
	if st := rec.Tasks[id].Status; st != "" && st != story.TaskPending {
		t.Errorf("tasks.%s is %s, want it never started", id, st)
	}
	if _, err := os.Stat(filepath.Join(f.storyDir, "tasks", id+".json")); !os.IsNotExist(err) {
		t.Errorf("%s has an input (err=%v)", id, err)
	}
	if strings.Contains(stdout, "▶ "+id) {
		t.Errorf("%s was started:\n%s", id, stdout)
	}
}

// implementCalls is how many implement calls the fake provider has seen.
func implementCalls(t *testing.T, f *storyRunFixture) int {
	t.Helper()
	n := 0
	for _, action := range invocations(t, f.invocationsLog) {
		if action == "implement" {
			n++
		}
	}
	return n
}

// Two slots, two independent tasks: in two services both children are running at the same time -
// each implement call waits, with a timeout, for the other's; in one service the second starts only
// once the first is done (TODO.md S10). Both commit on their own branches, nothing is reported as a
// conflict, the logs hold the children's output and the console only the start and result lines.
func TestStoryRun_TwoSlotsRunTwoTasksAtOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		repos  []string
		atOnce bool
	}{
		{"two repositories", []string{"billing", "gateway"}, true},
		{"one repository", []string{"billing", "billing"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStoryRunFixture(t, withTasks(parallelTasks(tc.repos...)))
			f.writePlanRecord(t)
			if tc.atOnce {
				t.Setenv("FAKE_RENDEZVOUS_DIR", t.TempDir())
				t.Setenv("FAKE_RENDEZVOUS_ACTION", "implement")
				t.Setenv("FAKE_RENDEZVOUS_COUNT", "2")
			}

			stdout, stderr, err := f.run(t, "--parallel", "2")
			mustSucceed(t, stdout, stderr, err)
			if !tc.atOnce {
				assertAfter(t, stdout, "▶ RF02 · billing · 2/2 · log ", "RF01: done")
			}

			rec := f.record(t)
			if rec.Status != story.StatusCompleted {
				t.Fatalf("status = %s, want completed", rec.Status)
			}
			for i, repo := range tc.repos {
				id := "RF0" + string(rune('1'+i))
				rt := rec.Tasks[id]
				if rt.Status != story.TaskDone || rt.Commit == "" || rt.Branch != "story/"+storyRunSlug+"/"+id {
					t.Fatalf("tasks.%s = %+v, want done on its branch with a commit", id, rt)
				}
				if tip, _ := gitOutput(f.repos[repo], "rev-parse", rt.Branch); tip != rt.Commit {
					t.Errorf("%s: %s is at %s, want %s", id, rt.Branch, tip, rt.Commit)
				}
				run := f.runRecord(t, repo, id, 1)
				if run.Status != domain.RunCompleted || run.Worktree == nil || len(run.Worktree.Conflicts) != 0 {
					t.Errorf("%s: the run is %s, worktree %+v; want completed without conflicts", id, run.Status, run.Worktree)
				}

				log := filepath.Join(f.storyDir, "logs", "task-"+id+".1.log")
				info, err := os.Stat(log)
				if err != nil {
					t.Fatalf("%s: no log: %v", id, err)
				}
				if info.Mode().Perm() != 0o600 {
					t.Errorf("%s: log mode %v, want 0600", id, info.Mode().Perm())
				}
				// The child's output is in its log, its run ID with it, and none of it on the console.
				text := readFile(t, log)
				assertContains(t, id+"'s log", text, run.ID)
				// The guard compares what makes git run code (.git/config, the hooks) and the
				// protected paths before and after each step: a sibling's change would show here.
				for _, s := range []string{"protected paths", "Run failed"} {
					if strings.Contains(text, s) {
						t.Errorf("%s's log reports %q:\n%s", id, s, text)
					}
				}
				if strings.Contains(stdout, run.ID) {
					t.Errorf("the console has %s's output (its run ID %s):\n%s", id, run.ID, stdout)
				}
				assertContains(t, "stdout", stdout, "▶ "+id+" · "+repo+" · "+string(rune('1'+i))+"/2 · log "+log, id+": done")
			}
			for _, s := range []string{"── RF0", "Run ID:", "Run completed"} {
				if strings.Contains(stdout, s) {
					t.Errorf("the console has a child's %q with two slots:\n%s", s, stdout)
				}
			}
		})
	}
}

// Two slots, one task fails while the other runs: the other runs to its end and is settled, no
// third task starts, the story is failed, and the hints of the failed task are printed.
func TestStoryRun_AFailingTaskLetsTheRunningOneEnd(t *testing.T) {
	f := newStoryRunFixture(t, withTasks(parallelTasks("billing", "gateway", "billing")))
	f.writePlanRecord(t)
	t.Setenv("FAKE_FAIL_ACTION", "implement")
	t.Setenv("FAKE_FAIL_BRANCH", "story/"+storyRunSlug+"/RF01")
	// RF02 waits in its review until RF01 is recorded failed, so RF02 is still running then.
	released := holdReview(t, f, "RF02", func(rec persistence.StoryRecord) bool { return rec.Tasks["RF01"].Status == story.TaskFailed })

	stdout, stderr, err := f.run(t, "--parallel", "2")
	if err == nil {
		t.Fatalf("a story with a failing task succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	assertWorkflowError(t, err)

	if at, ok := released(); !ok || at.Tasks["RF02"].Status != story.TaskRunning {
		t.Fatalf("RF02 was not running when RF01 was recorded failed (seen %v): %+v", ok, at.Tasks["RF02"])
	}
	rec := f.record(t)
	if rec.Status != story.StatusFailed || rec.Tasks["RF01"].Status != story.TaskFailed || rec.Tasks["RF02"].Status != story.TaskDone {
		t.Fatalf("status = %s, RF01 = %+v, RF02 = %+v; want a failed story, RF01 failed and RF02 done", rec.Status, rec.Tasks["RF01"], rec.Tasks["RF02"])
	}
	assertNotStarted(t, f, rec, stdout, "RF03")
	if run := f.runRecord(t, "gateway", "RF02", 1); run.Status != domain.RunCompleted {
		t.Errorf("RF02's run is %s, want completed", run.Status)
	}
	assertContains(t, "stdout", stdout, "RF01: failed", "RF02: done", "resumes run "+rec.Tasks["RF01"].RunID, "--retry RF01",
		"Note: not started because of the stop: RF03 - ready, the next story run starts them")
}

// The budget reached while one child runs: every implement costs $1 and story.max_cost_usd is
// $1.50. RF01 ends done with $1.05 recorded (the planning calls' $0.05 with it), under the budget;
// RF02 is in its review then, its $1 implement in its run.json, so the story has cost $2.05 and
// RF03 is not started. RF02 runs to its end, and the story stops after it.
func TestStoryRun_TheBudgetReachedWhileATaskRunsStartsNothingNew(t *testing.T) {
	f := newStoryRunFixture(t, withTasks(parallelTasks("billing", "gateway", "billing")), func(o *storyRunFixtureOpts) { o.maxCostUSD = 1.5 })
	f.writePlanRecord(t)
	writeResponse(t, f.responses, "implement", streamResult(t, storyImplementResponse, 1))
	// Both reach their review, each past its implement; RF02's waits there until RF01 is done.
	t.Setenv("FAKE_RENDEZVOUS_DIR", t.TempDir())
	t.Setenv("FAKE_RENDEZVOUS_ACTION", "review")
	released := holdReview(t, f, "RF02", func(rec persistence.StoryRecord) bool { return rec.Tasks["RF01"].Status == story.TaskDone })

	stdout, _, err := f.run(t, "--parallel", "2")
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("err = %T %v, want the budget refusal\nstdout:\n%s", err, err, stdout)
	}
	assertContains(t, "the error", err.Error(), "the story has cost $2.05", "story.max_cost_usd is $1.50")

	if at, ok := released(); !ok || at.Tasks["RF02"].Status != story.TaskRunning {
		t.Fatalf("RF02 was not running when RF01 was recorded done (seen %v): %+v", ok, at.Tasks["RF02"])
	}
	rec := f.record(t)
	if rec.Status != story.StatusStopped || rec.Tasks["RF01"].Status != story.TaskDone || rec.Tasks["RF02"].Status != story.TaskDone {
		t.Fatalf("status = %s, RF01 = %+v, RF02 = %+v; want a stopped story with RF01 and RF02 done", rec.Status, rec.Tasks["RF01"], rec.Tasks["RF02"])
	}
	if got := rec.TotalCostUSD(); got < 2.04 || got > 2.06 {
		t.Errorf("the recorded cost is $%.2f, want $2.05", got)
	}
	assertNotStarted(t, f, rec, stdout, "RF03")
	assertContains(t, "stdout", stdout, "RF01: done", "RF02: done")
}

// startedPair runs a story of two independent tasks with two slots until both implement calls are
// running, then cancels the context, and returns the error of the story run.
func startedPair(t *testing.T, f *storyRunFixture) error {
	t.Helper()
	t.Setenv("FAKE_SLEEP_ACTION", "implement")
	t.Setenv("FAKE_SLEEP_SECONDS", "60")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		defer cancel()
		for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if implementCalls(t, f) >= 2 {
				time.Sleep(500 * time.Millisecond)
				return
			}
		}
	}()
	stdout, stderr, err := runOrchestratorCtx(t, ctx, f.home, f.cfg, "story", "run", f.specDir, "--parallel", "2")
	if !errors.Is(err, context.Canceled) || exitcode.FromError(err) != exitcode.UserCancellation {
		t.Fatalf("err = %v (exit code %d), want a cancellation\nstdout:\n%s\nstderr:\n%s", err, exitcode.FromError(err), stdout, stderr)
	}
	return err
}

// Cancel with two children running: both get SIGINT, both are settled as stopped, and the story is
// stopped.
func TestStoryRun_CancelStopsBothRunningTasks(t *testing.T) {
	f := newStoryRunFixture(t, withTasks(parallelTasks("billing", "gateway")))
	f.writePlanRecord(t)
	startedPair(t, f)

	rec := f.record(t)
	if rec.Status != story.StatusStopped {
		t.Fatalf("status = %s, want stopped", rec.Status)
	}
	for _, id := range []string{"RF01", "RF02"} {
		if rt := rec.Tasks[id]; rt.Status != story.TaskStopped || rt.RunID == "" {
			t.Errorf("tasks.%s = %+v, want stopped with its run", id, rt)
		}
	}
}

// A task recorded as running whose run directory is locked by another process stops the story run
// before the checks and before anything is started.
func TestStoryRun_StopsWhileARunningTasksRunIsLocked(t *testing.T) {
	f := newStoryRunFixture(t, withTasks(parallelTasks("billing", "gateway")))
	f.writePlanRecord(t)
	startedPair(t, f)

	rec := f.record(t)
	rt := rec.Tasks["RF01"]
	rt.Status = story.TaskRunning
	rec.Tasks["RF01"] = rt
	if err := persistence.SaveStoryRecord(f.storyDir, rec); err != nil {
		t.Fatal(err)
	}
	unlock, err := persistence.NewStore(filepath.Join(f.repos["billing"], ".orchestrator", "runs")).Lock(rt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	calls, runs := len(invocations(t, f.invocationsLog)), f.runCount(t)
	t.Setenv("FAKE_SLEEP_ACTION", "")
	stdout, _, err := f.run(t, "--parallel", "2")
	assertConfigError(t, err, "RF01: run "+rt.RunID+" is still running")
	if strings.Contains(stdout, "Checking") {
		t.Errorf("the checks ran:\n%s", stdout)
	}
	if got := len(invocations(t, f.invocationsLog)); got != calls || f.runCount(t) != runs {
		t.Errorf("%d calls (was %d), %d runs (was %d): nothing may start", got, calls, f.runCount(t), runs)
	}
}

// --parallel is 1 to 8 and belongs to story run; story.max_parallel has the same range.
func TestStoryRun_ParallelUsageErrors(t *testing.T) {
	f := newStoryRunFixture(t)
	for _, args := range [][]string{
		{"story", "run", f.specDir, "--parallel", "0"},
		{"story", "run", f.specDir, "--parallel", "9"},
		{"story", "plan", "--parallel", "2", "Partial refunds"},
	} {
		_, _, err := runOrchestrator(t, f.home, f.cfg, args...)
		assertConfigError(t, err, "--parallel")
	}
	f.assertNothingStarted(t)
}

// The end-to-end scenarios of the parallel story (specs/parallel-story/tasks/TASK-PS04-scenarios-and-docs.md).
// Two repositories and four tasks:
//
//	RF01 billing                   based on billing's story base
//	RF02 billing                   based on billing's story base
//	RF03 billing  RF01 and RF02    based on story/refunds/spec, where their commits are merged
//	RF04 gateway                   based on gateway's story base
//
// Each task adds a file of its own (FAKE_IMPLEMENT_BRANCH_DIR), so merging RF01 and RF02 is clean,
// or writes the same file (FAKE_IMPLEMENT_BRANCH_FILE), so it conflicts.

var storyDiamondTasks = []story.Task{
	{ID: "RF01", Repo: "billing", Title: "Store the refunded amount"},
	{ID: "RF02", Repo: "billing", Title: "Round the refunded amount"},
	{ID: "RF03", Repo: "billing", Title: "Report the refunded amounts", DependsOn: []string{"RF01", "RF02"}},
	{ID: "RF04", Repo: "gateway", Title: "Accept an amount in the API"},
}

func newDiamondFixture(t *testing.T) *storyRunFixture {
	t.Helper()
	f := newStoryRunFixture(t, withTasks(storyDiamondTasks))
	f.writePlanRecord(t)
	writeResponse(t, f.responses, "implement", storyMergeImplement)
	t.Setenv("FAKE_IMPLEMENT_TOUCH_FILE", "")
	t.Setenv("FAKE_IMPLEMENT_BRANCH_DIR", ".")
	return f
}

// assertMergeOf asserts that ref, a commit of the repository, is a merge of exactly the commits
// parents, in that order.
func (f *storyRunFixture) assertMergeOf(t *testing.T, repo, ref string, parents ...string) {
	t.Helper()
	got := strings.Fields(f.git(t, repo, "rev-list", "--parents", "-n", "1", ref))
	if len(got) != len(parents)+1 || strings.Join(got[1:], " ") != strings.Join(parents, " ") {
		t.Errorf("%s has the parents %v, want %v", ref, got[1:], parents)
	}
}

// assertAfter asserts that stdout has each of befores ahead of after.
func assertAfter(t *testing.T, stdout, after string, befores ...string) {
	t.Helper()
	at := strings.Index(stdout, after)
	if at < 0 {
		t.Errorf("stdout lacks %q:\n%s", after, stdout)
		return
	}
	for _, before := range befores {
		if i := strings.Index(stdout, before); i < 0 || i > at {
			t.Errorf("%q is at %d in stdout, want it before %q at %d:\n%s", before, i, after, at, stdout)
		}
	}
}

// Scenario 1: RF01 and RF04, of two services, run at once - each implement call waits for the
// other's - and RF02, of RF01's service, starts once RF01 is done (TODO.md S10). RF03 starts after
// RF01 and RF02 are done, from the merge commit the parent made on the spec branch, and the story
// completes. Every target in the end table is the spec branch.
func TestStoryRun_ParallelDiamondRunsAtOnceAndMergesTheParents(t *testing.T) {
	f := newDiamondFixture(t)
	before := f.checkouts(t)
	bases := map[string]string{"billing": f.head(t, "billing"), "gateway": f.head(t, "gateway")}
	t.Setenv("FAKE_RENDEZVOUS_DIR", t.TempDir())
	t.Setenv("FAKE_RENDEZVOUS_ACTION", "implement")
	t.Setenv("FAKE_RENDEZVOUS_COUNT", "2")

	stdout, stderr, err := f.run(t, "--parallel", "3")
	mustSucceed(t, stdout, stderr, err)

	rec := f.record(t)
	if rec.Status != story.StatusCompleted {
		t.Fatalf("status = %s, want completed\n%s", rec.Status, stdout)
	}
	for _, task := range storyDiamondTasks {
		if rt := rec.Tasks[task.ID]; rt.Status != story.TaskDone || rt.Commit == "" {
			t.Errorf("tasks.%s = %+v, want done with a commit", task.ID, rt)
		}
	}
	// The spec branch is in both repositories; gateway's has nothing merged on it.
	for name, sha := range bases {
		if b := rec.Bases[name]; b.SpecBranch != specBranch || b.SHA != sha {
			t.Errorf("bases.%s = %+v, want the spec branch %s on the base %s", name, b, specBranch, sha)
		}
	}
	if tip := f.git(t, "gateway", "rev-parse", specBranch); tip != bases["gateway"] {
		t.Errorf("gateway's %s is at %s, want its base %s", specBranch, tip, bases["gateway"])
	}

	// A task without a parent starts from the story base, not from the spec tip.
	for _, id := range []string{"RF01", "RF02", "RF04"} {
		if in := f.input(t, id); in.BaseRef != bases[rec.Tasks[id].Repo] {
			t.Errorf("%s base_ref = %s, want the story base %s", id, in.BaseRef, bases[rec.Tasks[id].Repo])
		}
	}
	// RF03 starts from the merge commit of its parents' commits.
	rf01, rf02 := rec.Tasks["RF01"].Commit, rec.Tasks["RF02"].Commit
	in := f.input(t, "RF03")
	if tip := f.git(t, "billing", "rev-parse", specBranch); in.BaseRef != tip || rec.Tasks["RF03"].BaseRef != tip {
		t.Errorf("RF03 base_ref = %s (record %s), want the tip of %s: %s", in.BaseRef, rec.Tasks["RF03"].BaseRef, specBranch, tip)
	}
	f.assertMergeOf(t, "billing", in.BaseRef, rf01, rf02)
	if parent := f.git(t, "billing", "rev-parse", rec.Tasks["RF03"].Commit+"^"); parent != in.BaseRef {
		t.Errorf("RF03's commit has the parent %s, want the merge commit %s", parent, in.BaseRef)
	}
	if in.BaseBranch != specBranch {
		t.Errorf("RF03 base_branch = %s, want its merge request's target, the spec branch %s", in.BaseBranch, specBranch)
	}

	// RF04 took the slot RF02 had to leave while RF01 ran; RF02 started after RF01 was done, and RF03
	// after RF01 and RF02.
	assertAfter(t, stdout, "▶ RF04 · gateway · 4/4 · log ", "▶ RF01 · billing · 1/4 · log ")
	assertAfter(t, stdout, "▶ RF02 · billing · 2/4 · log ", "RF01: done")
	assertAfter(t, stdout, "▶ RF03 · billing · 3/4 · log ", "RF01: done", "RF02: done")
	assertContains(t, "stdout", stdout, "✓ Story completed: all 4 tasks done",
		"RF01 · billing · story/refunds/RF01 → story/refunds/spec",
		"RF02 · billing · story/refunds/RF02 → story/refunds/spec",
		"RF03 · billing · story/refunds/RF03 → story/refunds/spec",
		"RF04 · gateway · story/refunds/RF04 → story/refunds/spec")
	assertContains(t, "RF03 As built", story.AsBuiltText([]byte(readFile(t, f.taskPath("RF03")))), "on `story/refunds/spec (RF01 + RF02)`")
	f.assertCheckoutsUnchanged(t, before)
}

// Scenario 2: RF01 and RF02 conflict. RF04 is done, RF03 is conflict and not started, the story is
// blocked and the hint names the files. After a manual merge on the spec branch the next story run
// completes the story, running RF03 only.
func TestStoryRun_ParallelConflictThenAManualMerge(t *testing.T) {
	f := newDiamondFixture(t)
	t.Setenv("FAKE_IMPLEMENT_BRANCH_DIR", "")
	t.Setenv("FAKE_IMPLEMENT_BRANCH_FILE", "shared.txt") // RF01 and RF02 add it with different content

	stdout, stderr, err := f.run(t, "--parallel", "2")
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
	for _, id := range []string{"RF01", "RF02", "RF04"} {
		if st := rec.Tasks[id].Status; st != story.TaskDone {
			t.Errorf("tasks.%s is %s, want done: the conflict stops only what is not started", id, st)
		}
	}
	if rt.RunID != "" || rt.Attempt != 0 || strings.Contains(stdout, "▶ RF03") {
		t.Errorf("RF03 = %+v was started:\n%s", rt, stdout)
	}
	if _, err := os.Stat(filepath.Join(f.storyDir, "tasks", "RF03.json")); !os.IsNotExist(err) {
		t.Errorf("RF03 has an input (err=%v)", err)
	}
	assertContains(t, "stdout", stdout, "RF03: conflict (RF01 + RF02 do not merge cleanly on story/refunds/spec)", "shared.txt",
		"git merge story/refunds/RF01", "git merge story/refunds/RF02", "orchestrator story run")
	assertContains(t, "README.md", readFile(t, filepath.Join(f.specDir, "README.md")), "Status: **blocked**", "| conflict |")
	implements := implementCalls(t, f)
	if implements != 3 {
		t.Fatalf("%d implement calls, want RF01's, RF02's and RF04's", implements)
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

	stdout, stderr, err = f.run(t, "--parallel", "2")
	mustSucceed(t, stdout, stderr, err)
	rec = f.record(t)
	rt = rec.Tasks["RF03"]
	if rec.Status != story.StatusCompleted || rt.Status != story.TaskDone || rt.Conflict != nil {
		t.Fatalf("status = %s, RF03 = %+v; want completed with RF03 done", rec.Status, rt)
	}
	if in := f.input(t, "RF03"); in.BaseRef != merged || rt.BaseRef != merged {
		t.Errorf("RF03 base_ref = %s (record %s), want the operator's merge %s", in.BaseRef, rt.BaseRef, merged)
	}
	if got := implementCalls(t, f); got != implements+1 {
		t.Errorf("%d implement calls, want %d: only RF03 runs after the merge", got, implements+1)
	}
	assertContains(t, "stdout", stdout, "✓ Story completed: all 4 tasks done", "RF03 · billing · story/refunds/RF03 → story/refunds/spec")
}

// Scenario 3: RF04 fails while RF01 runs; RF02, of RF01's service, waits for it (TODO.md S10). RF01
// is settled, RF02 and RF03 are never started and the story is failed. The next story run resumes
// RF04's run, runs RF02, and starts RF03 only once RF02 is done.
func TestStoryRun_ParallelFailureThenResumeRunsTheMergeTask(t *testing.T) {
	f := newDiamondFixture(t)
	t.Setenv("FAKE_FAIL_ACTION", "implement")
	t.Setenv("FAKE_FAIL_BRANCH", "story/"+storyRunSlug+"/RF04")
	// RF01 waits in its review until RF04 is recorded failed, so RF01 is still running then.
	released := holdReview(t, f, "RF01", func(rec persistence.StoryRecord) bool { return rec.Tasks["RF04"].Status == story.TaskFailed })

	stdout, stderr, err := f.run(t, "--parallel", "2")
	if err == nil {
		t.Fatalf("a story with a failing task succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	assertWorkflowError(t, err)
	if at, ok := released(); !ok || at.Tasks["RF01"].Status != story.TaskRunning {
		t.Fatalf("RF01 was not running when RF04 was recorded failed (seen %v): %+v", ok, at.Tasks["RF01"])
	}
	rec := f.record(t)
	failed := rec.Tasks["RF04"]
	if rec.Status != story.StatusFailed || failed.Status != story.TaskFailed || failed.RunID == "" || rec.Tasks["RF01"].Status != story.TaskDone {
		t.Fatalf("status = %s, RF01 = %+v, RF04 = %+v; want a failed story, RF01 done and RF04 failed with its run", rec.Status, rec.Tasks["RF01"], failed)
	}
	assertNotStarted(t, f, rec, stdout, "RF02")
	assertNotStarted(t, f, rec, stdout, "RF03")
	// RF02 was ready once RF01 was done; RF03 waits for it and is not named.
	assertContains(t, "stdout", stdout, "RF01: done", "RF04: failed", "resumes run "+failed.RunID,
		"Note: not started because of the stop: RF02 - ready, the next story run starts them")
	rf01 := rec.Tasks["RF01"]
	runs := f.runCount(t)

	// The next run resumes RF04's run, with nothing failing any more.
	t.Setenv("FAKE_FAIL_ACTION", "")
	t.Setenv("FAKE_SLEEP_ACTION", "")
	stdout, stderr, err = f.run(t, "--parallel", "2")
	mustSucceed(t, stdout, stderr, err)
	rec = f.record(t)
	if rec.Status != story.StatusCompleted {
		t.Fatalf("status = %s, want completed\n%s", rec.Status, stdout)
	}
	if rt := rec.Tasks["RF04"]; rt.Status != story.TaskDone || rt.RunID != failed.RunID || rt.Attempt != 1 {
		t.Errorf("RF04 = %+v, want done on the same run %s and attempt 1", rt, failed.RunID)
	}
	if rt := rec.Tasks["RF01"]; rt.RunID != rf01.RunID || rt.Commit != rf01.Commit {
		t.Errorf("RF01 = %+v, was %+v: a done task must not run again", rt, rf01)
	}
	// RF02 and RF03 made the only new runs: RF04 was resumed, RF01 not touched.
	if got := f.runCount(t); got != runs+2 {
		t.Errorf("%d runs, want %d", got, runs+2)
	}
	if resumed := f.runRecord(t, "gateway", "RF04", 1); resumed.Status != domain.RunCompleted || len(resumed.Resumes) != 1 {
		t.Errorf("RF04's run = %s with %d resumes, want completed with one", resumed.Status, len(resumed.Resumes))
	}
	f.assertMergeOf(t, "billing", f.input(t, "RF03").BaseRef, rec.Tasks["RF01"].Commit, rec.Tasks["RF02"].Commit)
	assertAfter(t, stdout, "▶ RF03 · billing · 3/4 · log ", "RF02: done")
	assertContains(t, "stdout", stdout, "✓ Story completed: all 4 tasks done")
}

// Scenario 4: with one slot the story of TestStoryRun_OrderStackingAsBuiltAndTheEnd prints and
// stacks as before - the children under their headers, no start lines, no logs - apart from the
// spec branch, which is made in each repository and is the target of the tasks without a parent.
func TestStoryRun_OneSlotIsAsBefore(t *testing.T) {
	f := newStoryRunFixture(t)
	f.writePlanRecord(t)
	bases := map[string]string{"billing": f.head(t, "billing"), "gateway": f.head(t, "gateway")}

	stdout, stderr, err := f.run(t, "--parallel", "1")
	mustSucceed(t, stdout, stderr, err)

	rec := f.record(t)
	if rec.Status != story.StatusCompleted {
		t.Fatalf("status = %s, want completed\n%s", rec.Status, stdout)
	}
	if in := f.input(t, "RF01"); in.BaseRef != bases["billing"] {
		t.Errorf("RF01 base_ref = %s, want billing's base %s", in.BaseRef, bases["billing"])
	}
	if in := f.input(t, "RF02"); in.BaseRef != rec.Tasks["RF01"].Commit {
		t.Errorf("RF02 base_ref = %s, want RF01's commit %s", in.BaseRef, rec.Tasks["RF01"].Commit)
	}
	if in := f.input(t, "RF03"); in.BaseRef != bases["gateway"] {
		t.Errorf("RF03 base_ref = %s, want gateway's base %s", in.BaseRef, bases["gateway"])
	}
	if in := f.input(t, "RF04"); in.BaseRef != rec.Tasks["RF02"].Commit {
		t.Errorf("RF04 base_ref = %s, want RF02's commit %s", in.BaseRef, rec.Tasks["RF02"].Commit)
	}
	for name, sha := range bases {
		if tip := f.git(t, name, "rev-parse", specBranch); tip != sha || rec.Bases[name].SpecBranch != specBranch {
			t.Errorf("%s's %s is at %s (record %q), want its base %s: nothing is merged on it", name, specBranch, tip, rec.Bases[name].SpecBranch, sha)
		}
	}
	if merges := f.git(t, "billing", "rev-list", "--merges", "--all"); merges != "" {
		t.Errorf("a story without several parents made merge commits: %s", merges)
	}

	assertContains(t, "stdout", stdout, "Story plan: 4 tasks in 2 repositories", "── RF01 · billing · 1/4 ──", "── RF02 · billing · 2/4 ──",
		"── RF03 · gateway · 3/4 ──", "── RF04 · billing · 4/4 ──", "✓ Story completed: all 4 tasks done",
		"RF01 · billing · story/refunds/RF01 → story/refunds/spec",
		"RF02 · billing · story/refunds/RF02 → story/refunds/RF01",
		"RF03 · gateway · story/refunds/RF03 → story/refunds/spec",
		"RF04 · billing · story/refunds/RF04 → story/refunds/RF02")
	if strings.Contains(stdout, "▶ RF0") {
		t.Errorf("a start line with one slot:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(f.storyDir, "logs")); !os.IsNotExist(err) {
		t.Errorf("logs/ exists with one slot (err=%v): the children print on the console", err)
	}
}
