package integration

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 18, continuing (specs/resume/SPEC.md §6.3, §9): before anything runs on a tree
// a model last touched, resume compares it with the run's start - git fingerprint first,
// then the protected paths, then HEAD - and refuses what it reads back from the run
// directory when that is inconsistent. No refusal calls a provider or records a resume.

// assertRefusedUnchanged checks that a refused resume called nothing and left run.json
// as it was.
func assertRefusedUnchanged(t *testing.T, repoDir, invocationsLog string, calls int, before []byte) {
	t.Helper()
	if got := len(invocations(t, invocationsLog)); got != calls {
		t.Errorf("a refused resume called a provider (%d calls, was %d)", got, calls)
	}
	if after, err := os.ReadFile(runJSONPath(t, repoDir)); err != nil || string(after) != string(before) {
		t.Errorf("a refused resume changed run.json (err=%v)", err)
	}
}

func TestScenario18_ResumeRefusesProtectedPathChange(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	// Empty directories are invisible to git, so the tree stays clean.
	if err := os.MkdirAll(filepath.Join(repoDir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	invocationsLog, _ := decisionEnv(t, repoDir)
	settings := filepath.Join(repoDir, ".claude", "settings.json")
	t.Setenv("FAKE_FIX_TARGET_FILE", settings)
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j)

	_, _, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) {
		t.Fatalf("expected the guard to fail implement, got %T: %v", err, err)
	}
	failed := loadResumedRun(t, repoDir)
	calls := len(invocations(t, invocationsLog))
	before, _ := os.ReadFile(runJSONPath(t, repoDir))

	// The violating file is still there: nothing may run on it.
	_, _, err = resume(t, repoDir, cfg, failed.ID)
	if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("expected exit 4, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), ".claude/settings.json") || !strings.Contains(err.Error(), "since the run started") {
		t.Errorf("the refusal must name the path:\n%v", err)
	}
	assertRefusedUnchanged(t, repoDir, invocationsLog, calls, before)

	// Reverted: implement is retried, and this time it fixes the code.
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_FIX_TARGET_FILE", filepath.Join(repoDir, "fixture.go"))
	stdout, stderr, err := resume(t, repoDir, cfg, failed.ID)
	mustSucceed(t, stdout, stderr, err)
	if got := invocations(t, invocationsLog)[calls:]; !equalStrings(got, []string{"implement", "review"}) {
		t.Errorf("invocations after reverting = %v, want [implement review]", got)
	}
}

// recordGit puts a git on PATH that appends each argv to a log before running the real
// git, and returns the log.
func recordGit(t *testing.T) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "git-argv.log")
	dir := t.TempDir()
	fakeexec.WriteExecutable(t, dir, "git", "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+log+"\"\nexec \""+realGit+"\" \"$@\"\n")
	fakeexec.PrependPath(t, dir)
	return log
}

func TestScenario18_ResumeRefusesPlantedFsmonitor(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	gitLog := recordGit(t)
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j)
	failed := failedAtImplement(t, repoDir, cfg)
	calls := len(invocations(t, invocationsLog))
	before, _ := os.ReadFile(runJSONPath(t, repoDir))

	// Between the runs, something plants a core.fsmonitor: git status would run it.
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	hook := fakeexec.WriteExecutable(t, t.TempDir(), "fsmonitor", "#!/bin/sh\ntouch \""+marker+"\"\nexit 1\n")
	f, err := os.OpenFile(filepath.Join(repoDir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("[core]\n\tfsmonitor = " + hook + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Truncate(gitLog, 0); err != nil {
		t.Fatal(err)
	}

	_, _, err = resume(t, repoDir, cfg, failed.ID)
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("expected exit 4, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), ".git (config or hooks)") || !strings.Contains(err.Error(), "Do not run git") {
		t.Errorf("refusal message:\n%v", err)
	}
	argv, err := os.ReadFile(gitLog)
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) == 0 {
		t.Fatal("the git wrapper recorded nothing; is it on PATH?")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(argv)), "\n") {
		if strings.HasPrefix(line, "status") || strings.HasPrefix(line, "diff") {
			t.Errorf("git %s ran before the fingerprint was compared; all git calls:\n%s", line, argv)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the planted fsmonitor ran")
	}
	assertRefusedUnchanged(t, repoDir, invocationsLog, calls, before)
}

func TestScenario18_ResumeRefusesMovedHead(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j)
	failed := failedAtImplement(t, repoDir, cfg)
	calls := len(invocations(t, invocationsLog))
	before, _ := os.ReadFile(runJSONPath(t, repoDir))

	gitRun(t, repoDir, "commit", "-q", "--allow-empty", "-m", "moved on")
	head, _ := gitOutput(repoDir, "rev-parse", "HEAD")

	_, _, err := resume(t, repoDir, cfg, failed.ID)
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("expected exit 4, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), failed.HeadRef) || !strings.Contains(err.Error(), head) || failed.HeadRef == "" {
		t.Errorf("the refusal must show head_ref %s and HEAD %s:\n%v", failed.HeadRef, head, err)
	}
	assertRefusedUnchanged(t, repoDir, invocationsLog, calls, before)
}

func TestScenario18_ResumeRefusesTampering(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j)
	failed := failedAtImplement(t, repoDir, cfg)
	calls := len(invocations(t, invocationsLog))
	artifact := filepath.Join(filepath.Dir(runJSONPath(t, repoDir)), "artifacts", "000-investigate.json")

	cases := []struct {
		name   string
		tamper func() (restore func())
		want   string
	}{
		{"workflow does not match the decision", func() func() {
			original := editRunJSON(t, repoDir, func(run map[string]any) {
				wf := run["workflow"].(map[string]any)
				steps := wf["steps"].([]any)
				wf["steps"] = steps[:len(steps)-1] // drop review
			})
			return func() { os.WriteFile(runJSONPath(t, repoDir), original, 0o644) }
		}, "not the one the decision builds"},
		{"artifact of a completed step deleted", func() func() {
			data, err := os.ReadFile(artifact)
			if err != nil {
				t.Fatal(err)
			}
			os.Remove(artifact)
			return func() { os.WriteFile(artifact, data, 0o644) }
		}, "artifact is missing"},
		{"artifact_path traversal", func() func() {
			original := editRunJSON(t, repoDir, func(run map[string]any) {
				run["executions"].([]any)[0].(map[string]any)["artifact_path"] = "../../../etc/passwd"
			})
			return func() { os.WriteFile(runJSONPath(t, repoDir), original, 0o644) }
		}, "artifact_path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restore := tc.tamper()
			defer restore()

			_, _, err := resume(t, repoDir, cfg, failed.ID)
			var stateErr *apperr.RunStateError
			if !errors.As(err, &stateErr) || stateErr.Kind != apperr.RunCorrupt || exitcode.FromError(err) != exitcode.GenericError {
				t.Fatalf("expected RunStateError{corrupt} (exit 1), got %T: %v", err, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal must say %q:\n%v", tc.want, err)
			}
			if got := len(invocations(t, invocationsLog)); got != calls {
				t.Errorf("a refused resume called a provider")
			}
		})
	}

	// Untampered, the same run resumes.
	stdout, stderr, err := resume(t, repoDir, cfg, failed.ID)
	mustSucceed(t, stdout, stderr, err)
}
