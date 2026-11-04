package integration

import (
	"errors"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 18 (specs/resume/SPEC.md §6, §10): the resume command itself - its usage, a
// run refused for a dirty tree that is resumable once the tree is clean, and a plan-mode
// run executed later (R08 item 3), where a plan-mode resume leaves it as it found it.
// Resume never decides again.

func TestScenario18_ResumeUsage(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	cfg := writeConfig(t, t.TempDir(), defaultConfigOptions())

	for _, args := range [][]string{{"resume"}, {"resume", "0123abcd", "extra"}} {
		_, _, err := runOrchestrator(t, repoDir, cfg, args...)
		var cfgErr *apperr.ConfigError
		if !errors.As(err, &cfgErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
			t.Errorf("%v: expected a usage error (exit 2), got %T: %v", args, err, err)
		} else if !strings.Contains(err.Error(), "resume <run-id>") {
			t.Errorf("%v: the usage must name the argument: %v", args, err)
		}
	}
}

func TestScenario18_DirtyRefusalIsResumable(t *testing.T) {
	repoDir := dirtyFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j)

	_, _, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected the dirty-tree refusal, got %T: %v", err, err)
	}
	refused := loadResumedRun(t, repoDir)
	if refused.Status != "failed" || refused.Workflow == nil || len(refused.Workflow.Steps) == 0 {
		t.Fatalf("a refused run must keep its workflow: %+v", refused)
	}
	if _, ok := runFile(t, repoDir, "decision.json"); !ok {
		t.Error("a refused run must keep its decision.json")
	}

	// Still dirty: the resume applies the same rule, before any call.
	_, _, err = runOrchestrator(t, repoDir, cfg, "resume", refused.ID)
	if !errors.As(err, &cfgErr) {
		t.Fatalf("resume on the dirty tree: expected the refusal, got %T: %v", err, err)
	}
	if got := invocations(t, invocationsLog); len(got) != 0 {
		t.Fatalf("no step may run on a refusal, got %v", got)
	}

	gitRun(t, repoDir, "add", "notes.txt")
	gitRun(t, repoDir, "commit", "-qm", "notes")
	decisionCalls := len(j.Bodies())

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "resume", refused.ID[:8])
	mustSucceed(t, stdout, stderr, err)
	if got := len(j.Bodies()); got != decisionCalls {
		t.Errorf("resume must not decide again: %d decision calls, was %d", got, decisionCalls)
	}
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"investigate", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
	for _, want := range []string{"Resuming run " + refused.ID, "Next: investigate — start", "Remaining workflow: investigate → implement → test → review", "Restore point:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}

	run := loadResumedRun(t, repoDir)
	head, _ := gitOutput(repoDir, "rev-parse", "HEAD")
	if run.Status != "completed" || run.FailureReason != "" || run.PreRunRef != head || run.HeadRef != head || len(run.ResumeBaseline) == 0 {
		t.Errorf("run after resume: status=%s reason=%q pre_run_ref=%s head_ref=%s baseline=%d bytes (HEAD %s)",
			run.Status, run.FailureReason, run.PreRunRef, run.HeadRef, len(run.ResumeBaseline), head)
	}
	if len(run.Resumes) != 1 || run.Resumes[0].FromStatus != "failed" || !strings.Contains(run.Resumes[0].FromReason, "uncommitted changes") ||
		run.Resumes[0].Entry != "investigate" || run.Resumes[0].Mode != "auto" {
		t.Errorf("resumes = %+v", run.Resumes)
	}
	for _, e := range run.Executions {
		if e.Attempt != 1 {
			t.Errorf("execution %s: attempt = %d, want 1", e.Action, e.Attempt)
		}
	}
}

func TestScenario18_PlanModeResumeChangesNothing(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j)

	stdout, stderr, err := runOrchestratorInteractive(t, repoDir, cfg, "a\n", "--mode", "plan", "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)
	planned := loadResumedRun(t, repoDir)
	if planned.Status != "pending" || planned.Mode != "plan" || planned.PreRunRef != "" {
		t.Fatalf("plan-mode run: %+v", planned)
	}
	before, _ := runFile(t, repoDir, "run.json")

	// Approve, then an empty answer: a persisted plan is never the default, so it asks
	// again; choosing plan previews without touching the run.
	stdout, stderr, err = runOrchestratorInteractive(t, repoDir, cfg, "a\n\nplan\n", "resume", planned.ID)
	mustSucceed(t, stdout, stderr, err)
	if !strings.Contains(stdout, "unrecognized option") || !strings.Contains(stdout, "Not executing (plan mode)") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if after, _ := runFile(t, repoDir, "run.json"); after != before {
		t.Errorf("a plan-mode resume must leave run.json unchanged:\nbefore %s\nafter  %s", before, after)
	}

	// Cancelling at approval leaves it unchanged too.
	_, _, err = runOrchestratorInteractive(t, repoDir, cfg, "c\n", "resume", planned.ID)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("cancel: got %v", err)
	}
	if after, _ := runFile(t, repoDir, "run.json"); after != before {
		t.Errorf("a cancelled resume must leave run.json unchanged")
	}
	if got := invocations(t, invocationsLog); len(got) != 0 {
		t.Fatalf("no step may run, got %v", got)
	}

	stdout, stderr, err = runOrchestrator(t, repoDir, cfg, "resume", planned.ID)
	mustSucceed(t, stdout, stderr, err)
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"investigate", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
	// The restore point, head_ref and baseline are recorded when the run first executes,
	// and it runs from the first step.
	run := loadResumedRun(t, repoDir)
	head, _ := gitOutput(repoDir, "rev-parse", "HEAD")
	if run.Status != "completed" || run.PreRunRef != head || run.HeadRef != head || len(run.ResumeBaseline) == 0 {
		t.Errorf("run after resume: status=%s pre_run_ref=%s head_ref=%s baseline=%d bytes (HEAD %s)",
			run.Status, run.PreRunRef, run.HeadRef, len(run.ResumeBaseline), head)
	}
	if len(run.Resumes) != 1 || run.Resumes[0].FromStatus != "pending" || run.Resumes[0].Entry != "investigate" {
		t.Errorf("resumes = %+v", run.Resumes)
	}
	if len(run.Executions) == 0 || run.Executions[0].Action != "investigate" {
		t.Errorf("executions = %+v, want the first step first", run.Executions)
	}

	// A completed run: nothing runs, exit 0.
	n := len(invocations(t, invocationsLog))
	stdout, stderr, err = runOrchestrator(t, repoDir, cfg, "resume", planned.ID)
	mustSucceed(t, stdout, stderr, err)
	if !strings.Contains(stdout, "already completed") || len(invocations(t, invocationsLog)) != n {
		t.Errorf("resuming a completed run must report it and call nothing:\n%s", stdout)
	}
}
