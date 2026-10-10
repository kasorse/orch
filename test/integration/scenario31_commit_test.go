package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// commitScenario runs a bugfix workflow (implement, test, review) that changes fixture.go,
// feeding stdin under --mode accept-edits, or with --mode auto when auto is set.
func commitScenario(t *testing.T, stdin string, auto bool) (repoDir, stdout string) {
	t.Helper()
	return commitScenarioWith(t, implementResponse, stdin, auto, "")
}

// commitScenarioWith is commitScenario with implement's answer and extra config given.
func commitScenarioWith(t *testing.T, implement, stdin string, auto bool, extraYAML string) (repoDir, stdout string) {
	t.Helper()
	repoDir = fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	responsesDir := t.TempDir()
	writeResponse(t, responsesDir, "implement", implement)
	writeResponse(t, responsesDir, "review", reviewPassResponse)
	t.Setenv("FAKE_RESPONSES_DIR", responsesDir)
	t.Setenv("FAKE_STATE_DIR", t.TempDir())
	t.Setenv("INVOCATIONS_LOG", filepath.Join(t.TempDir(), "invocations.log"))
	t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", "1")
	t.Setenv("FAKE_FIX_TARGET_FILE", filepath.Join(repoDir, "fixture.go"))
	t.Setenv("FAKE_FIX_CONTENT_FILE", writeFixedFixtureFile(t))

	opts := defaultConfigOptions()
	opts.jevURL = startJev(t, scriptBugfixLow()).URL()
	opts.extraYAML = extraYAML
	configPath := writeConfig(t, t.TempDir(), opts)

	var stderr string
	var err error
	if auto {
		stdout, stderr, err = runOrchestrator(t, repoDir, configPath, "run", "fix Add() to add instead of subtract")
	} else {
		stdout, stderr, err = runOrchestratorInteractive(t, repoDir, configPath, stdin,
			"--mode", "accept-edits", "run", "fix Add() to add instead of subtract")
	}
	if err != nil {
		t.Fatalf("run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	return repoDir, stdout
}

// Scenario 31: at the end of a completed run that changed the tree, the operator is asked
// whether to commit (MVP.md §42). Yes commits the run's changes - and not the run
// directory - with a message Go wrote from the implementation's summary_short; run.json records
// the commit.
func TestScenario31_CommitApproved(t *testing.T) {
	repoDir, stdout := commitScenario(t, "a\ny\n", false)

	branch, _ := gitOutput(repoDir, "symbolic-ref", "--short", "HEAD")
	if !strings.Contains(stdout, "Commit to "+branch+"? [y]es / [N]o") {
		t.Errorf("no commit question in stdout:\n%s", stdout)
	}
	run := readRunJSON(t, repoDir)
	head, _ := gitOutput(repoDir, "rev-parse", "HEAD")
	head = strings.TrimSpace(head)
	if run["commit"] != head {
		t.Errorf("run.json commit = %v, HEAD = %s", run["commit"], head)
	}
	msg, _ := gitOutput(repoDir, "log", "-1", "--format=%B")
	want := "Fix Add to use +\n\nOrchestrator-Run: " + run["id"].(string)
	if strings.TrimSpace(msg) != want {
		t.Errorf("commit message = %q, want %q", msg, want)
	}
	files, _ := gitOutput(repoDir, "show", "--name-only", "--format=", "HEAD")
	if strings.TrimSpace(files) != "fixture.go" {
		t.Errorf("committed files = %q, want only fixture.go", files)
	}
	if count, _ := gitOutput(repoDir, "rev-list", "--count", "HEAD"); strings.TrimSpace(count) != "2" {
		t.Errorf("commit count = %s, want 2", count)
	}
}

// An implementation without a summary_short proposes no message: the operator writes it,
// and it is still committed only on the explicit yes.
func TestScenario31_CommitMessageAsked(t *testing.T) {
	implement := strings.Replace(implementResponse, `  "summary_short": "Fix Add to use +",`+"\n", "", 1)
	if strings.Contains(implement, "summary_short") {
		t.Fatal("the response still has a summary_short")
	}
	repoDir, stdout := commitScenarioWith(t, implement, "a\nAdd instead of subtract\ny\n", false, "")

	if !strings.Contains(stdout, "The implementation proposed no commit message.") {
		t.Errorf("no request for a message in stdout:\n%s", stdout)
	}
	msg, _ := gitOutput(repoDir, "log", "-1", "--format=%B")
	want := "Add instead of subtract\n\nOrchestrator-Run: " + readRunJSON(t, repoDir)["id"].(string)
	if strings.TrimSpace(msg) != want {
		t.Errorf("commit message = %q, want %q", msg, want)
	}
}

// The default answer is no: nothing is committed and the change stays in the tree.
func TestScenario31_CommitDeclined(t *testing.T) {
	repoDir, stdout := commitScenario(t, "a\n\n", false)

	if !strings.Contains(stdout, "Not committed") {
		t.Errorf("stdout lacks the decline:\n%s", stdout)
	}
	if count, _ := gitOutput(repoDir, "rev-list", "--count", "HEAD"); strings.TrimSpace(count) != "1" {
		t.Errorf("commit count = %s, want 1", count)
	}
	if status, _ := gitOutput(repoDir, "status", "--porcelain", "--", "fixture.go"); !strings.Contains(status, "fixture.go") {
		t.Errorf("fixture.go change is gone: %q", status)
	}
	if _, ok := readRunJSON(t, repoDir)["commit"]; ok {
		t.Error("run.json records a commit")
	}
}

// --mode auto asks nothing, so it never commits.
func TestScenario31_AutoModeNeverAsks(t *testing.T) {
	repoDir, stdout := commitScenario(t, "", true)

	if strings.Contains(stdout, "Commit to") {
		t.Errorf("auto mode asked to commit:\n%s", stdout)
	}
	if count, _ := gitOutput(repoDir, "rev-list", "--count", "HEAD"); strings.TrimSpace(count) != "1" {
		t.Errorf("commit count = %s, want 1", count)
	}
}

// auto_commit: --mode auto commits without asking, with the message Go wrote, and
// run.json records it.
func TestScenario31_AutoCommit(t *testing.T) {
	repoDir, stdout := commitScenarioWith(t, implementResponse, "", true, "auto_commit: true\n")

	if strings.Contains(stdout, "Commit to") {
		t.Errorf("auto_commit asked:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Committed ") {
		t.Errorf("stdout lacks the commit:\n%s", stdout)
	}
	run := readRunJSON(t, repoDir)
	head, _ := gitOutput(repoDir, "rev-parse", "HEAD")
	if run["commit"] != strings.TrimSpace(head) {
		t.Errorf("run.json commit = %v, HEAD = %s", run["commit"], head)
	}
	msg, _ := gitOutput(repoDir, "log", "-1", "--format=%B")
	if want := "Fix Add to use +\n\nOrchestrator-Run: " + run["id"].(string); strings.TrimSpace(msg) != want {
		t.Errorf("commit message = %q, want %q", msg, want)
	}
}

// auto_commit without a summary_short has no message to use: nothing is committed.
func TestScenario31_AutoCommitWithoutMessage(t *testing.T) {
	implement := strings.Replace(implementResponse, `  "summary_short": "Fix Add to use +",`+"\n", "", 1)
	repoDir, stdout := commitScenarioWith(t, implement, "", true, "auto_commit: true\n")

	if !strings.Contains(stdout, "Not committing automatically: the implementation proposed no commit message") {
		t.Errorf("stdout lacks the reason:\n%s", stdout)
	}
	if count, _ := gitOutput(repoDir, "rev-list", "--count", "HEAD"); strings.TrimSpace(count) != "1" {
		t.Errorf("commit count = %s, want 1", count)
	}
}
