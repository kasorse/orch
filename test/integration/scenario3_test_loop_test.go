package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 3 (MVP.md §29): tests fail -> implement -> tests pass -> review -> completed.
// The implement fake only fixes the fixture on its second call, so the first test run
// fails and loops back to implement before ever reaching review.
func TestScenario3_TestLoop(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	responsesDir := t.TempDir()
	stateDir := t.TempDir()
	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")
	fixedContent := writeFixedFixtureFile(t)

	writeResponse(t, responsesDir, "classify", classifyBugfixMedium)
	writeResponse(t, responsesDir, "analyze", analyzeResponse)
	writeResponse(t, responsesDir, "implement", implementResponse)
	writeResponse(t, responsesDir, "review", reviewPassResponse)

	t.Setenv("FAKE_RESPONSES_DIR", responsesDir)
	t.Setenv("FAKE_STATE_DIR", stateDir)
	t.Setenv("INVOCATIONS_LOG", invocationsLog)
	t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", "2")
	t.Setenv("FAKE_FIX_TARGET_FILE", filepath.Join(repoDir, "fixture.go"))
	t.Setenv("FAKE_FIX_CONTENT_FILE", fixedContent)

	configDir := t.TempDir()
	configPath := writeConfig(t, configDir, defaultConfigOptions())

	stdout, stderr, err := runOrchestrator(t, repoDir, configPath, "run", "fix Add() to add instead of subtract")
	if err != nil {
		t.Fatalf("run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	want := []string{"classify", "analyze", "implement", "implement", "review"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v", got, want)
	}

	if !strings.Contains(stdout, "test failed") {
		t.Errorf("expected a test failure to be reported, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Run completed successfully.") {
		t.Errorf("expected eventual success message in stdout, got:\n%s", stdout)
	}
}
