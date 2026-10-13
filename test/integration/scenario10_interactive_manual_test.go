package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/app"
	"orchestrator/internal/testutil/fakeexec"
)

// runOrchestratorInteractive is like runOrchestrator but feeds canned stdin and never
// forces --mode auto, so the default interactive approve/edit/mode-select flow runs.
func runOrchestratorInteractive(t *testing.T, repoDir, configPath, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	origWD, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("getwd: %v", wdErr)
	}
	if chdirErr := os.Chdir(repoDir); chdirErr != nil {
		t.Fatalf("chdir %s: %v", repoDir, chdirErr)
	}
	defer func() {
		if chdirErr := os.Chdir(origWD); chdirErr != nil {
			t.Fatalf("restore chdir: %v", chdirErr)
		}
	}()

	fullArgs := append([]string{"--config", configPath}, args...)

	var outBuf, errBuf bytes.Buffer
	err = app.Run(context.Background(), fullArgs, strings.NewReader(stdin), &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), err
}

// readRunJSON finds the single run.json under repoDir/.orchestrator/runs/*/ and
// unmarshals the fields this test cares about.
func readRunJSON(t *testing.T, repoDir string) map[string]any {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(repoDir, ".orchestrator", "runs", "*", "run.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one run.json, got %v (err=%v)", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read run.json: %v", err)
	}
	var run map[string]any
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatalf("run.json is not valid JSON: %v", err)
	}
	return run
}

// Scenario 10: default interactive flow (no --mode flag) - approve the workflow as-is,
// choose manual mode, then confirm every step. Asserts the persisted run.json records
// mode "manual" and status "completed".
func TestScenario10_InteractiveManualRun(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	responsesDir := t.TempDir()
	stateDir := t.TempDir()
	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")
	fixedContent := writeFixedFixtureFile(t)

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
	opts.jevURL = startJev(t, scriptBugfixLow()).URL()
	configPath := writeConfig(t, configDir, opts)

	// approve the workflow, pick manual mode, then confirm implement and test in turn.
	stdin := "a\n2\ny\ny\n"

	stdout, stderr, err := runOrchestratorInteractive(t, repoDir, configPath, stdin, "run", "fix Add() to add instead of subtract")
	if err != nil {
		t.Fatalf("run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations = %v, want [implement]", got)
	}

	if !strings.Contains(stdout, "Run completed successfully.") {
		t.Errorf("expected success message in stdout, got:\n%s", stdout)
	}

	run := readRunJSON(t, repoDir)
	if run["mode"] != "manual" {
		t.Errorf("run.json mode = %v, want manual", run["mode"])
	}
	if run["status"] != "completed" {
		t.Errorf("run.json status = %v, want completed", run["status"])
	}
	decision, ok := run["decision"].(map[string]any)
	if !ok || decision["source"] != "jev" {
		t.Errorf("run.json decision = %v, want the Jev decision recorded", run["decision"])
	}
}
