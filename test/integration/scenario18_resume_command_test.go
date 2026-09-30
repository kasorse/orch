package integration

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 18 (specs/resume/SPEC.md §6, §10; TASK-R06): the resume command itself - its
// usage, a run refused for a dirty tree that is resumable once the tree is clean, and a
// plan-mode resume that leaves the run as it found it. Resume never decides again.

type resumedRun struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
	Mode          string `json:"mode"`
	PreRunRef     string `json:"pre_run_ref"`
	HeadRef       string `json:"head_ref"`
	Workflow      *struct {
		Steps []struct {
			Action string `json:"action"`
		} `json:"steps"`
	} `json:"workflow"`
	ResumeBaseline json.RawMessage `json:"resume_baseline"`
	Resumes        []struct {
		FromStatus string `json:"from_status"`
		FromReason string `json:"from_reason"`
		Entry      string `json:"entry"`
		Why        string `json:"why"`
		Mode       string `json:"mode"`
	} `json:"resumes"`
	Executions []struct {
		Action  string `json:"action"`
		Attempt int    `json:"attempt"`
	} `json:"executions"`
}

func loadResumedRun(t *testing.T, repoDir string) resumedRun {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var run resumedRun
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

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
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"analyze", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
	for _, want := range []string{"Resuming run " + refused.ID, "Next: analyze — start", "Remaining workflow: analyze → implement → test → review", "Restore point:"} {
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
		run.Resumes[0].Entry != "analyze" || run.Resumes[0].Mode != "auto" {
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
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"analyze", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
	run := loadResumedRun(t, repoDir)
	if run.Status != "completed" || run.PreRunRef == "" || len(run.Resumes) != 1 || run.Resumes[0].FromStatus != "pending" {
		t.Errorf("run after resume: %+v", run)
	}

	// A completed run: nothing runs, exit 0.
	n := len(invocations(t, invocationsLog))
	stdout, stderr, err = runOrchestrator(t, repoDir, cfg, "resume", planned.ID)
	mustSucceed(t, stdout, stderr, err)
	if !strings.Contains(stdout, "already completed") || len(invocations(t, invocationsLog)) != n {
		t.Errorf("resuming a completed run must report it and call nothing:\n%s", stdout)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
