package integration

import (
	"errors"
	"os"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/testutil/fakeexec"
)

// specs/claude-auth/SPEC.md §4: --claude-auth api with the key variable unset is a config error
// inside the fail-fast chain, so the fake claude is not called once.
func TestClaudeAuth_APIWithoutKeyFailsBeforeAnyCall(t *testing.T) {
	r := newStageRun(t, fakeexec.InitGitFixture(t))
	argsDir := t.TempDir()
	t.Setenv("FAKE_ARGS_DIR", argsDir)
	t.Setenv("ANTHROPIC_API_KEY", "") // registers the restore; the next line unsets it
	os.Unsetenv("ANTHROPIC_API_KEY")

	_, _, err := runOrchestrator(t, r.repoDir, r.cfg, "--claude-auth", "api", "run", "--stage", "research", "look")
	var cerr *apperr.ConfigError
	if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY is not set") {
		t.Fatalf("want a config error naming the variable, got %T: %v", err, err)
	}
	entries, readErr := os.ReadDir(argsDir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Errorf("claude was called before the failure: %v", entries)
	}
}
