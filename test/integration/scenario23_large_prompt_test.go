package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 23 (specs/hardening/SPEC.md §4): a 300 KiB task reaches the claude CLI on stdin.
// Nothing about the prompt is in argv, so Linux's per-argument limit cannot break the call.
func TestScenario23_LargeTaskOnStdin(t *testing.T) {
	r := newStageRun(t, fakeexec.InitGitFixture(t))
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "research", readOnlyResponse)
	argsDir := t.TempDir()
	t.Setenv("FAKE_ARGS_DIR", argsDir)

	task := "summarize this log: " + strings.Repeat("line of a long log\n", 300<<10/19)
	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--stage", "research", task)
	mustSucceed(t, stdout, stderr, err)

	prompt, err := os.ReadFile(filepath.Join(r.promptsDir, "research.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompt), strings.TrimSpace(task)) {
		t.Errorf("the research prompt (%d bytes) lacks the whole task", len(prompt))
	}
	for _, a := range readArgs(t, argsDir, "research") {
		if len(a) > 4096 {
			t.Errorf("argv element of %d bytes: %.60q", len(a), a)
		}
	}
}
