package integration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 26, checks (specs/quality/SPEC.md §5.5): the operator's checks run before
// test.command in the pre-run test. A red check refuses the run like a red suite and names
// itself; with --allow-red-tests the first implement sees the check's output.

// redCheckYAML is a check that fails exactly while the fixture's test fails, so the fake
// implement's fix turns it green.
const redCheckYAML = "\nchecks:\n  - name: lint\n    command: 'go test ./... >/dev/null 2>&1 || { echo lint-finding; exit 1; }'\n"

type checkRecord struct {
	PreRunTest *struct {
		ExitCode    int    `json:"exit_code"`
		Command     string `json:"command"`
		FailedCheck string `json:"failed_check"`
	} `json:"pre_run_test"`
}

func loadCheckRecord(t *testing.T, repoDir string) checkRecord {
	t.Helper()
	data, err := os.ReadFile(runJSONPath(t, repoDir))
	if err != nil {
		t.Fatal(err)
	}
	var rec checkRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestScenario26_RedCheckStopsBeforeImplement(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newPreRunTestRun(t, repoDir, func(o *configOptions) { o.extraYAML = redCheckYAML })

	stdout, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "fix Add in fixture.go")...)
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("expected a WorkflowError (exit 4), got %T: %v", err, err)
	}
	for _, want := range []string{`check "lint" fails before any change`, "pre-run-test.lint.stdout.log", "--allow-red-tests", "test.baseline: off"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q:\n%v", want, err)
		}
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("no provider call may happen before the refusal, got %v", got)
	}
	if !strings.Contains(stdout, "Pre-run test: failed (check lint, exit 1") {
		t.Errorf("the console does not name the check:\n%s", stdout)
	}
	rec := loadCheckRecord(t, repoDir)
	if rec.PreRunTest == nil || rec.PreRunTest.FailedCheck != "lint" || rec.PreRunTest.ExitCode != 1 {
		t.Errorf("run.json pre_run_test = %+v", rec.PreRunTest)
	}
	logPath := filepath.Join(filepath.Dir(runJSONPath(t, repoDir)), "logs", "pre-run-test.lint.stdout.log")
	if data, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(data), "lint-finding") {
		t.Errorf("the check's log: %v %q", err, data)
	}
}

func TestScenario26_RedCheckWithAllowRedTests(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newPreRunTestRun(t, repoDir, func(o *configOptions) { o.extraYAML = redCheckYAML })

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "--allow-red-tests", "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	prompt, err := os.ReadFile(filepath.Join(r.promptsDir, "implement.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`The check "lint"`, "The tests did not run", "lint-finding"} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("implement was not shown the red check (%q missing):\n%s", want, prompt)
		}
	}
	if rec := loadCheckRecord(t, repoDir); rec.PreRunTest == nil || rec.PreRunTest.FailedCheck != "lint" {
		t.Errorf("run.json pre_run_test = %+v", rec.PreRunTest)
	}
}
