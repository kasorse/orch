package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Headless runs (specs/delivery/SPEC.md §11.2): with --output json stdout is exactly one JSON
// line, written last, and everything a person reads - the reporter, the commit, the push and the
// merge request - goes to stderr.

// outputJSONFields is the field set of the result line.
var outputJSONFields = []string{"v", "run_id", "status", "exit_code", "failure_reason", "error", "workflow", "commit", "branch",
	"mr", "mr_skipped", "mr_error", "review_verdict", "tests_passed_final", "approval_required", "cost_usd", "cost_partial", "run_dir", "report"}

// resultLine checks that stdout is exactly one line of JSON with the whole field set and the exit
// code err maps to, and returns it decoded.
func resultLine(t *testing.T, stdout, stderr string, err error) map[string]any {
	t.Helper()
	if strings.Count(stdout, "\n") != 1 || !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("stdout is not exactly one line (err %v):\n%q\nstderr:\n%s", err, stdout, stderr)
	}
	var m map[string]any
	if decErr := json.Unmarshal([]byte(stdout), &m); decErr != nil {
		t.Fatalf("stdout is not JSON: %v\n%q", decErr, stdout)
	}
	if len(m) != len(outputJSONFields) {
		t.Errorf("the line has %d fields, want %d: %v", len(m), len(outputJSONFields), m)
	}
	for _, f := range outputJSONFields {
		if _, ok := m[f]; !ok {
			t.Errorf("the line has no %q: %v", f, m)
		}
	}
	if m["v"] != float64(1) || m["exit_code"] != float64(exitcode.FromError(err)) {
		t.Errorf("v = %v, exit_code = %v; want 1 and %d (err %v)", m["v"], m["exit_code"], exitcode.FromError(err), err)
	}
	return m
}

// A worktree run that opens its merge request: exit 0, the line names the run, the commit, the
// branch and the merge request; the progress, the commit and the merge request are on stderr.
func TestOutputJSON_CompletedRun(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "--output", "json", "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	run := loadWorktreeRun(t, repoDir)
	m := resultLine(t, stdout, stderr, err)
	const url = "https://" + fakeexec.GitLabHost + "/group/project/-/merge_requests/1"
	mr, _ := m["mr"].(map[string]any)
	if m["run_id"] != run.ID || m["status"] != "completed" || m["error"] != "" || m["commit"] != run.Commit || run.Commit == "" ||
		m["branch"] != "orchestrator/"+run.ID || mr["iid"] != float64(1) || mr["url"] != url || m["mr_skipped"] != "" || m["mr_error"] != "" {
		t.Errorf("line = %v", m)
	}
	if wf, _ := m["workflow"].([]any); len(wf) != 2 || wf[0] != "implement" || wf[1] != "test" {
		t.Errorf("workflow = %v, want [implement test]", m["workflow"])
	}
	if dir, _ := m["run_dir"].(string); filepath.Base(dir) != run.ID {
		t.Errorf("run_dir = %v, want the run's directory", m["run_dir"])
	}
	if report, _ := m["report"].(string); report == "" {
		t.Errorf("report is empty, want report.md")
	} else if !filepath.IsAbs(report) {
		// The runs directory is relative to the repository the run was started in.
		if _, statErr := os.Stat(filepath.Join(repoDir, report)); statErr != nil {
			t.Errorf("report %s: %v", report, statErr)
		}
	} else if _, statErr := os.Stat(report); statErr != nil {
		t.Errorf("report %s: %v", report, statErr)
	}
	assertContains(t, "stderr", stderr, "Run: "+run.ID, "Committed "+run.Commit[:12]+" to orchestrator/"+run.ID, "Merge request !1: "+url+"\n")
}

// A run whose implement fails: the line carries the failed status and the reason, and the exit
// code is the run's.
func TestOutputJSON_FailedRun(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r, _, _ := newWorktreeRun(t, repoDir)
	t.Setenv("FAKE_FAIL_ACTION", "implement")

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "--output", "json", "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	if err == nil {
		t.Fatalf("a run whose implement fails succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}

	run := loadWorktreeRun(t, repoDir)
	m := resultLine(t, stdout, stderr, err)
	if run.Status != "failed" || m["run_id"] != run.ID || m["status"] != "failed" || m["failure_reason"] == "" || m["error"] == "" ||
		m["commit"] != "" || m["mr"] != nil {
		t.Errorf("status = %s, line = %v", run.Status, m)
	}
	assertContains(t, "stderr", stderr, "Run: "+run.ID)
}

// A config error before any run exists: the error line, with no run, and the validation progress
// on stderr.
func TestOutputJSON_ConfigErrorBeforeTheRun(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newStageRun(t, repoDir)
	missing := filepath.Join(t.TempDir(), "none.yaml")

	stdout, stderr, err := runOrchestrator(t, repoDir, missing, "--output", "json", "run", "--stage", "development", "fix Add in fixture.go")
	if exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Fatalf("exit code %d (%v), want %d", exitcode.FromError(err), err, exitcode.ConfigOrDependency)
	}

	m := resultLine(t, stdout, stderr, err)
	if m["run_id"] != "" || m["status"] != "error" || !strings.Contains(m["error"].(string), "failed to read config file") ||
		m["run_dir"] != "" || m["mr"] != nil {
		t.Errorf("line = %v", m)
	}
	assertContains(t, "stderr", stderr, "Validating configuration...")
	if _, statErr := os.Stat(filepath.Join(repoDir, ".orchestrator", "runs")); !os.IsNotExist(statErr) {
		t.Errorf("a run directory exists after a config error: %v", statErr)
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("invocations = %v, want none", got)
	}
}

// A completed run that delivered nothing needs a person: exit 6, the status stays completed, and
// the line says why in mr_skipped (the commit failed) or mr_error (the push was refused).
func TestOutputJSON_NeedsAPerson(t *testing.T) {
	t.Run("skipped", func(t *testing.T) {
		repoDir := fakeexec.InitGitFixture(t)
		hook := filepath.Join(repoDir, ".git", "hooks", "pre-commit")
		if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'commits are refused here' >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		r, _, _ := newWorktreeRun(t, repoDir)

		stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "--output", "json", "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
		if exitcode.FromError(err) != exitcode.NeedsPerson {
			t.Fatalf("exit code %d (%v), want %d\nstdout:\n%s\nstderr:\n%s", exitcode.FromError(err), err, exitcode.NeedsPerson, stdout, stderr)
		}

		run := loadWorktreeRun(t, repoDir)
		m := resultLine(t, stdout, stderr, err)
		if m["run_id"] != run.ID || m["status"] != "completed" || m["commit"] != "" || m["mr"] != nil || m["mr_error"] != "" ||
			!strings.HasPrefix(m["mr_skipped"].(string), "the commit failed: ") || m["error"] == "" {
			t.Errorf("line = %v", m)
		}
		assertContains(t, "stderr", stderr, "Commit failed: ", "No push and no merge request: the commit failed: ")
	})
	t.Run("error", func(t *testing.T) {
		repoDir := fakeexec.InitGitFixture(t)
		r, _, _ := newWorktreeRun(t, repoDir)
		gitRun(t, r.remote, "config", "receive.advertisePushOptions", "false")

		stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "--output", "json", "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
		if exitcode.FromError(err) != exitcode.NeedsPerson {
			t.Fatalf("exit code %d (%v), want %d\nstdout:\n%s\nstderr:\n%s", exitcode.FromError(err), err, exitcode.NeedsPerson, stdout, stderr)
		}

		run := loadWorktreeRun(t, repoDir)
		m := resultLine(t, stdout, stderr, err)
		if m["run_id"] != run.ID || m["status"] != "completed" || m["commit"] != run.Commit || run.Commit == "" || m["mr"] != nil ||
			m["mr_skipped"] != "" || !strings.Contains(m["mr_error"].(string), "does not support push options") {
			t.Errorf("line = %v", m)
		}
		assertContains(t, "stderr", stderr, "To push it yourself: git push -u origin orchestrator/"+run.ID+"\n")
	})
}
