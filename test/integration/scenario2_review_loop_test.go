package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 2 (MVP.md §29): review fail -> implement -> tests pass -> review pass.
// The implement fake fixes the fixture on its very first call, so both test runs pass;
// only the review outcome differs between the two passes through the workflow.
func TestScenario2_ReviewLoop(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	responsesDir := t.TempDir()
	stateDir := t.TempDir()
	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")
	fixedContent := writeFixedFixtureFile(t)

	writeResponse(t, responsesDir, "classify", classifyBugfixMedium)
	writeResponse(t, responsesDir, "analyze", analyzeResponse)
	writeResponse(t, responsesDir, "implement", implementResponse)
	writeResponse(t, responsesDir, "review_1", reviewFailResponse)
	writeResponse(t, responsesDir, "review_2", reviewPassResponse)

	t.Setenv("FAKE_RESPONSES_DIR", responsesDir)
	t.Setenv("FAKE_STATE_DIR", stateDir)
	t.Setenv("INVOCATIONS_LOG", invocationsLog)
	t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", "1")
	t.Setenv("FAKE_FIX_TARGET_FILE", filepath.Join(repoDir, "fixture.go"))
	t.Setenv("FAKE_FIX_CONTENT_FILE", fixedContent)

	configDir := t.TempDir()
	configPath := writeConfig(t, configDir, defaultConfigOptions())

	stdout, stderr, err := runOrchestrator(t, repoDir, configPath, "run", "fix Add() to add instead of subtract")
	if err != nil {
		t.Fatalf("run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	want := []string{"classify", "analyze", "implement", "review", "implement", "review"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v", got, want)
	}

	if !strings.Contains(stdout, "review failed") {
		t.Errorf("expected a review failure to be reported, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Run completed successfully.") {
		t.Errorf("expected eventual success message in stdout, got:\n%s", stdout)
	}
}
