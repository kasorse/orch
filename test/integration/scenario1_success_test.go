package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 1 (MVP.md §29): classification -> plan -> implement -> tests pass ->
// review pass -> completed.
func TestScenario1_SuccessfulBugfix(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	responsesDir := t.TempDir()
	stateDir := t.TempDir()
	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")
	fixedContent := writeFixedFixtureFile(t)

	writeResponse(t, responsesDir, "plan", readOnlyResponse)
	writeResponse(t, responsesDir, "implement", implementResponse)
	writeResponse(t, responsesDir, "review", reviewPassResponse)

	t.Setenv("FAKE_RESPONSES_DIR", responsesDir)
	t.Setenv("FAKE_STATE_DIR", stateDir)
	t.Setenv("INVOCATIONS_LOG", invocationsLog)
	t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", "1")
	t.Setenv("FAKE_FIX_TARGET_FILE", filepath.Join(repoDir, "fixture.go"))
	t.Setenv("FAKE_FIX_CONTENT_FILE", fixedContent)

	configDir := t.TempDir()
	opts := defaultConfigOptions()
	opts.jevURL = startJev(t, scriptBugfixMedium()).URL()
	configPath := writeConfig(t, configDir, opts)

	stdout, stderr, err := runOrchestrator(t, repoDir, configPath, "run", "fix Add() to add instead of subtract")
	if err != nil {
		t.Fatalf("run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"plan", "implement", "review"}) {
		t.Errorf("invocations = %v, want [plan implement review]", got)
	}

	if !strings.Contains(stdout, "Run completed successfully.") {
		t.Errorf("expected success message in stdout, got:\n%s", stdout)
	}
}
