package integration

import (
	"errors"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 53 (specs/governance/SPEC.md §12, GV04): the org file requires test.sandbox: bwrap and
// the repository config says none. That is a configuration error naming both files, before any
// provider call.

func TestScenario53_RepositoryWeakerThanTheOrgIsAConfigError(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newStageRun(t, repoDir)
	orgPath := useOrgConfig(t, "test:\n  sandbox: bwrap\n")

	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Fatalf("expected a ConfigError (exit 2), got %T: %v", err, err)
	}
	assertContains(t, "the error", err.Error(),
		r.cfg+" conflicts with the org config "+orgPath, r.cfg+": test.sandbox: none is weaker than "+orgPath)

	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("invocations = %v, want none", got)
	}
	if n := len(r.jev.Bodies()); n != 0 {
		t.Errorf("Jev got %d requests, want 0", n)
	}
}
