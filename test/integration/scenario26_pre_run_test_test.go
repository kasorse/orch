package integration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 26 (specs/hardening/SPEC.md §8): a workflow that implements and tests runs the
// test command once before the first step. The fixture's test fails until Add is fixed, so
// it is a red suite before any change.

// preRunRecord is the part of run.json the pre-run test writes.
type preRunRecord struct {
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
	AllowRedTests bool   `json:"allow_red_tests"`
	PreRunTest    *struct {
		ExitCode int  `json:"exit_code"`
		Success  bool `json:"success"`
	} `json:"pre_run_test"`
	Executions []json.RawMessage `json:"executions"`
}

func loadPreRunRecord(t *testing.T, repoDir string) preRunRecord {
	t.Helper()
	data, err := os.ReadFile(runJSONPath(t, repoDir))
	if err != nil {
		t.Fatal(err)
	}
	var rec preRunRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// newPreRunTestRun is a manual implement+test run with test.baseline at its default.
func newPreRunTestRun(t *testing.T, repoDir string, mutate ...func(*configOptions)) stageRun {
	t.Helper()
	return newStageRun(t, repoDir, append([]func(*configOptions){func(o *configOptions) {
		o.testBaseline = "require_green"
	}}, mutate...)...)
}

var implementAndTest = []string{"run", "--stage", "development", "--stage", "testing"}

// 26a: a red suite stops the run before implement, with the way out named.
func TestScenario26_RedSuiteStopsBeforeImplement(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newPreRunTestRun(t, repoDir)

	stdout, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "fix Add in fixture.go")...)
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("expected a WorkflowError (exit 4), got %T: %v", err, err)
	}
	for _, want := range []string{"tests fail before any change (pre-run test exited 1", "--allow-red-tests", "test.baseline: off"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q:\n%v", want, err)
		}
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("no provider call may happen before the refusal, got %v", got)
	}
	if !strings.Contains(stdout, "Pre-run test: failed (exit 1") {
		t.Errorf("the console does not report the pre-run test:\n%s", stdout)
	}
	rec := loadPreRunRecord(t, repoDir)
	if rec.Status != "failed" || rec.PreRunTest == nil || rec.PreRunTest.ExitCode != 1 || len(rec.Executions) != 0 {
		t.Errorf("run.json = %+v", rec)
	}
	logPath := filepath.Join(filepath.Dir(runJSONPath(t, repoDir)), "logs", "pre-run-test.stdout.log")
	if data, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(data), "FAIL") {
		t.Errorf("pre-run test log: %v %q", err, data)
	}
}

// 26b: --allow-red-tests makes the red suite part of the task: implement sees its output and
// the run must still end green.
func TestScenario26_AllowRedTests(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newPreRunTestRun(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "--allow-red-tests", "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations = %v", got)
	}
	prompt, err := os.ReadFile(filepath.Join(r.promptsDir, "implement.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompt), "The test command already FAILS before any change") || !strings.Contains(string(prompt), "FAIL") {
		t.Errorf("implement was not shown the red suite:\n%s", prompt)
	}
	rec := loadPreRunRecord(t, repoDir)
	if !rec.AllowRedTests || rec.PreRunTest == nil || rec.PreRunTest.Success || rec.Status != "completed" {
		t.Errorf("run.json = %+v", rec)
	}
	if !strings.Contains(stdout, "going on: --allow-red-tests") {
		t.Errorf("the console does not say the run goes on:\n%s", stdout)
	}
}

// 26c: a green suite is recorded and the run goes on as before; test.baseline: off skips it.
func TestScenario26_GreenSuiteAndOff(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	if err := os.WriteFile(filepath.Join(repoDir, "fixture.go"), []byte(fakeexec.FixedFixtureSource), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repoDir, "commit", "-qam", "fix Add")
	r := newPreRunTestRun(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "tidy fixture.go")...)
	mustSucceed(t, stdout, stderr, err)
	if rec := loadPreRunRecord(t, repoDir); rec.PreRunTest == nil || !rec.PreRunTest.Success {
		t.Errorf("run.json = %+v", rec)
	}
	if !strings.Contains(stdout, "Pre-run test: passed") {
		t.Errorf("console:\n%s", stdout)
	}

	repoOff := fakeexec.InitGitFixture(t)
	rOff := newStageRun(t, repoOff) // the helpers' default is test.baseline: off
	stdout, stderr, err = runOrchestrator(t, repoOff, rOff.cfg, append(implementAndTest, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)
	if rec := loadPreRunRecord(t, repoOff); rec.PreRunTest != nil {
		t.Errorf("test.baseline: off ran the pre-run test: %+v", rec.PreRunTest)
	}
}

// 26d: the pre-run test runs under the guard like the test step.
func TestScenario26_GuardCoversThePreRunTest(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newPreRunTestRun(t, repoDir, func(o *configOptions) {
		o.testCommand = "echo '#!/bin/sh' > .git/hooks/pre-commit && go test ./..."
	})
	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "--allow-red-tests", "fix Add in fixture.go")...)
	if err == nil || !strings.Contains(err.Error(), "pre-run test changed protected paths") {
		t.Fatalf("expected a guard violation of the pre-run test, got %v", err)
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("no provider call may happen after the violation, got %v", got)
	}
}

// 26e: a run refused for a red suite resumes once the suite is green, from the first step.
func TestScenario26_ResumeAfterTheSuiteIsFixed(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newPreRunTestRun(t, repoDir)
	if _, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "tidy fixture.go")...); err == nil {
		t.Fatal("expected the red suite to stop the run")
	}
	id := loadResumedRun(t, repoDir).ID

	if err := os.WriteFile(filepath.Join(repoDir, "fixture.go"), []byte(fakeexec.FixedFixtureSource), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repoDir, "commit", "-qam", "fix Add by hand")

	stdout, stderr, err := resume(t, repoDir, r.cfg, id, "--mode", "auto")
	mustSucceed(t, stdout, stderr, err)
	rec := loadPreRunRecord(t, repoDir)
	if rec.Status != "completed" || rec.PreRunTest == nil || !rec.PreRunTest.Success {
		t.Errorf("run.json = %+v", rec)
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations = %v", got)
	}
}
