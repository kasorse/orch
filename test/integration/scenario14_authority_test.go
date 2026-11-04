package integration

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 14 (specs/permissions/SPEC.md §6.5): an action routed to a provider that cannot
// enforce its authority fails the fail-fast chain - exit 2, before any Jev request or CLI
// call - even though the provider's executable is installed. gemini supports no authority
// until its translation is verified (SPEC.md §6.4).
func TestScenario14_UnenforceableAuthority(t *testing.T) {
	for _, command := range []string{"run", "validate"} {
		t.Run(command, func(t *testing.T) {
			repoDir := fakeexec.InitGitFixture(t)
			installFakeProviders(t, "claude", "codex", "gemini")

			invocationsLog := filepath.Join(t.TempDir(), "invocations.log")
			t.Setenv("INVOCATIONS_LOG", invocationsLog)

			jev := startJev(t, scriptBugfixMedium())
			opts := defaultConfigOptions()
			opts.jevURL = jev.URL()
			opts.providers.Review = "gemini"
			configPath := writeConfig(t, t.TempDir(), opts)

			args := []string{command}
			if command == "run" {
				args = append(args, "fix Add()")
			}
			_, _, err := runOrchestrator(t, repoDir, configPath, args...)

			var authErr *apperr.AuthorityError
			if !errors.As(err, &authErr) {
				t.Fatalf("expected *apperr.AuthorityError, got %T: %v", err, err)
			}
			if got := exitcode.FromError(err); got != exitcode.ConfigOrDependency {
				t.Errorf("exit code = %d, want %d", got, exitcode.ConfigOrDependency)
			}
			if !strings.Contains(err.Error(), `provider "gemini" cannot enforce the "read" authority required by "review"`) {
				t.Errorf("unexpected message:\n%v", err)
			}
			if got := invocations(t, invocationsLog); len(got) != 0 {
				t.Errorf("expected no CLI invocations, got %v", got)
			}
			if got := jev.Bodies(); len(got) != 0 {
				t.Errorf("expected no Jev requests, got %d", len(got))
			}
		})
	}
}
