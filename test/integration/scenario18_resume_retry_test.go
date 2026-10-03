package integration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 18, continuing (specs/resume/SPEC.md §7-§8): a resumed run continues where
// its history left off - a failed step is retried with the context it had, limits count
// the whole history, and a run whose process died is continued as "interrupted".

// scriptWithPlan decides analysis -> planning -> development -> testing -> review.
func scriptWithPlan() jevScript {
	return jevScript{
		PProject: 0.9,
		Choice:   [3]float64{0.1, 0.7, 0.2},
		Stages:   map[string]float64{"analysis": 0.9, "planning": 0.9, "development": 0.95, "testing": 0.9, "review": 0.8},
	}
}

// failedAtImplement runs a task whose implement call fails (the fake exits 1) and
// returns the failed run.
func failedAtImplement(t *testing.T, repoDir, cfg string) resumedRun {
	t.Helper()
	t.Setenv("FAKE_FAIL_ACTION", "implement")
	if _, _, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go"); err == nil {
		t.Fatal("expected implement to fail the run")
	}
	t.Setenv("FAKE_FAIL_ACTION", "")
	run := loadResumedRun(t, repoDir)
	if run.Status != "failed" {
		t.Fatalf("run status = %s, want failed", run.Status)
	}
	return run
}

func TestScenario18_ResumeRetriesFailedStep(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, promptsDir := decisionEnv(t, repoDir)
	j := startJev(t, scriptWithPlan())
	cfg := jevConfig(t, j)

	failed := failedAtImplement(t, repoDir, cfg)
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"analyze", "plan", "implement"}) {
		t.Fatalf("first attempt invocations = %v", got)
	}
	if err := os.Remove(filepath.Join(promptsDir, "implement.txt")); err != nil {
		t.Fatal(err)
	}
	decisionCalls := len(j.Bodies())

	stdout, stderr, err := resume(t, repoDir, cfg, failed.ID)
	mustSucceed(t, stdout, stderr, err)
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"analyze", "plan", "implement", "implement", "review"}) {
		t.Errorf("invocations = %v: only implement is retried, then test and review", got)
	}
	if len(j.Bodies()) != decisionCalls {
		t.Error("resume must not decide again")
	}
	for _, want := range []string{"Previous attempt: failed — provider execution failed", "✗ 002 implement", "Next: implement — retry implement"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}

	// The retried implement got the plan the first process produced, from its artifact.
	prompt, err := os.ReadFile(filepath.Join(promptsDir, "implement.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompt), "Plan:\nfix the operator") || !strings.Contains(string(prompt), "Root cause: wrong operator") {
		t.Errorf("the retried implement prompt lacks the plan or analysis:\n%s", prompt)
	}

	run := loadResumedRun(t, repoDir)
	var got []string
	for _, e := range run.Executions {
		got = append(got, e.Action)
	}
	if !equalStrings(got, []string{"analyze", "plan", "implement", "implement", "test", "review"}) {
		t.Fatalf("executions = %v: the history must be appended to, never rewritten", got)
	}
	for i, e := range run.Executions {
		if want := map[bool]int{true: 0, false: 1}[i < 3]; e.Attempt != want {
			t.Errorf("executions[%d] (%s): attempt = %d, want %d", i, e.Action, e.Attempt, want)
		}
	}
	if run.Status != "completed" || run.FailureReason != "" {
		t.Errorf("status = %s, reason = %q", run.Status, run.FailureReason)
	}
	if len(run.Resumes) != 1 || run.Resumes[0].FromStatus != "failed" || run.Resumes[0].Entry != "implement" ||
		!strings.HasPrefix(run.Resumes[0].FromReason, "provider execution failed") {
		t.Errorf("resumes = %+v", run.Resumes)
	}
}

// A run recorded before restore points were pinned has no ref; an approved resume pins it.
func TestScenario18_ResumePinsAnUnpinnedRestorePoint(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	j := startJev(t, scriptWithPlan())
	cfg := jevConfig(t, j)

	failed := failedAtImplement(t, repoDir, cfg)
	assertRestorePointPinned(t, repoDir)
	if _, err := gitOutput(repoDir, "update-ref", "-d", "refs/orchestrator/runs/"+failed.ID); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := resume(t, repoDir, cfg, failed.ID)
	mustSucceed(t, stdout, stderr, err)
	assertRestorePointPinned(t, repoDir)
}

func TestScenario18_ResumeCountsLimitsOverTheWholeHistory(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, promptsDir := decisionEnv(t, repoDir)
	t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", "999") // the tests keep failing
	j := startJev(t, scriptBugfixMedium())
	limitOne := jevConfig(t, j, func(o *configOptions) { o.maxFixIterations = 1 })

	_, _, err := runOrchestrator(t, repoDir, limitOne, "run", "fix Add in fixture.go")
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected the fix-iteration limit, got %T: %v", err, err)
	}
	stopped := loadResumedRun(t, repoDir)
	calls := len(invocations(t, invocationsLog))

	// Same config: the limit is already used up, so nothing is asked or called.
	_, _, err = resume(t, repoDir, limitOne, stopped.ID)
	if !errors.As(err, &limitErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("resume with the same limit: expected exit 4, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "limits.max_fix_iterations") {
		t.Errorf("the refusal must name the key to raise:\n%v", err)
	}
	if got := len(invocations(t, invocationsLog)); got != calls {
		t.Fatalf("a refused resume called a provider (%d calls, was %d)", got, calls)
	}
	if run := loadResumedRun(t, repoDir); len(run.Resumes) != 0 {
		t.Errorf("a refused resume must not record itself: %+v", run.Resumes)
	}
	if err := os.Remove(filepath.Join(promptsDir, "implement.txt")); err != nil {
		t.Fatal(err)
	}

	// Raised: implement runs again, given the failing test output as fenced data.
	limitTwo := jevConfig(t, j, func(o *configOptions) { o.maxFixIterations = 2 })
	_, _, err = resume(t, repoDir, limitTwo, stopped.ID)
	if !errors.As(err, &limitErr) {
		t.Fatalf("the tests still fail, so the raised limit is reached again; got %T: %v", err, err)
	}
	if got := invocations(t, invocationsLog)[calls:]; !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations after raising the limit = %v, want [implement]", got)
	}
	prompt, err := os.ReadFile(filepath.Join(promptsDir, "implement.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"The previous implementation FAILED the test command", "<<<UNTRUSTED-STDOUT-", "FAIL"} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("the implement prompt lacks %q:\n%s", want, prompt)
		}
	}
	if run := loadResumedRun(t, repoDir); run.Status != "stopped" || len(run.Resumes) != 1 {
		t.Errorf("status = %s, resumes = %d", run.Status, len(run.Resumes))
	}
}

func TestScenario18_ResumeInterruptedRun(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j)
	failed := failedAtImplement(t, repoDir, cfg)

	// What a process killed mid-run leaves: status running, no reason, no lock held.
	editRunJSON(t, repoDir, func(run map[string]any) {
		run["status"] = "running"
		delete(run, "failure_reason")
	})

	stdout, stderr, err := resume(t, repoDir, cfg, failed.ID)
	mustSucceed(t, stdout, stderr, err)
	if !strings.Contains(stdout, "Previous attempt: interrupted") {
		t.Errorf("stdout lacks the interrupted header:\n%s", stdout)
	}
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"analyze", "implement", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
	run := loadResumedRun(t, repoDir)
	if run.Status != "completed" || len(run.Resumes) != 1 || run.Resumes[0].FromStatus != "running" {
		t.Errorf("status = %s, resumes = %+v", run.Status, run.Resumes)
	}
}
