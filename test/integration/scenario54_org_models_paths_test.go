package integration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 54 (specs/governance/SPEC.md §12, GV04): the org file's allowed_models refuses a model
// the config routes to, before any provider call; its protected_paths are added to the
// repository's, and the guard enforces both.

func TestScenario54_ModelOutsideAllowedModelsIsAConfigError(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newStageRun(t, repoDir)
	orgPath := useOrgConfig(t, "allowed_models: [sonnet]\n")

	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Fatalf("expected a ConfigError (exit 2), got %T: %v", err, err)
	}
	assertContains(t, "the error", err.Error(),
		"actions.implement: model fake is not in allowed_models ("+orgPath+")",
		"actions.review: model fake is not in allowed_models ("+orgPath+")")

	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("invocations = %v, want none", got)
	}
}

func TestScenario54_ProtectedPathsAreTheUnionOfOrgAndRepository(t *testing.T) {
	for name, target := range map[string]string{
		"a path only the org protects":        "secrets/key.txt",
		"a path only the repository protects": "docs/note.md",
	} {
		t.Run(name, func(t *testing.T) {
			repoDir := fakeexec.InitGitFixture(t)
			r := newStageRun(t, repoDir, func(o *configOptions) {
				o.extraYAML = "\nsecurity:\n  protected_paths: [\"docs/**\"]\n"
			})
			useOrgConfig(t, "security:\n  protected_paths: [\"secrets/**\"]\n")
			// Empty directories are invisible to git, so the tree stays clean.
			if err := os.MkdirAll(filepath.Join(repoDir, filepath.Dir(target)), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAKE_FIX_TARGET_FILE", filepath.Join(repoDir, target))

			_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
			var wfErr *apperr.WorkflowError
			if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
				t.Fatalf("expected a WorkflowError (exit 4), got %T: %v", err, err)
			}
			assertContains(t, "the error", err.Error(), "implement changed protected paths", target, "Restore point:")
			if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement"}) {
				t.Errorf("invocations = %v, want [implement]", got)
			}
			if run := loadQL(t, repoDir); run.Status != "failed" || !equalStrings(run.actions(), []string{"implement"}) {
				t.Errorf("status %s, executions %v", run.Status, run.actions())
			}
		})
	}
}
