package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/persistence"
	"orchestrator/internal/story"
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

// Two slots, two independent tasks: both children are running at the same time - each implement
// call waits, with a timeout, for the other's - in two repositories and in one. Both commit on
// their own branches, nothing is reported as a conflict, the logs hold the children's output and
// the console only the start and result lines.
func TestStoryRun_TwoSlotsRunTwoTasksAtOnce(t *testing.T) {
	for _, tc := range []struct {
		name  string
		repos []string
	}{
		{"two repositories", []string{"billing", "gateway"}},
		{"one repository", []string{"billing", "billing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStoryRunFixture(t, withTasks(parallelTasks(tc.repos...)))
			f.writePlanRecord(t)
			t.Setenv("FAKE_RENDEZVOUS_DIR", t.TempDir())
			t.Setenv("FAKE_RENDEZVOUS_ACTION", "implement")
			t.Setenv("FAKE_RENDEZVOUS_COUNT", "2")

			stdout, stderr, err := f.run(t, "--parallel", "2")
			mustSucceed(t, stdout, stderr, err)

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
	assertContains(t, "stdout", stdout, "RF01: failed", "RF02: done", "resumes run "+rec.Tasks["RF01"].RunID, "--retry RF01")
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
