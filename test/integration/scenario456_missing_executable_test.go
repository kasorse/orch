package integration

import (
	"errors"
	"path/filepath"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenarios 4-6 (MVP.md §29): missing Claude / Codex / Gemini. In every case config
// validation (including dependency checking) must fail before workflow execution -
// i.e. before any AI call happens.
func TestScenario456_MissingExecutable(t *testing.T) {
	cases := []struct {
		name          string
		installed     []string
		missingAction func(providerSet) providerSet
	}{
		{
			name:      "missing claude",
			installed: []string{"codex", "gemini"},
			missingAction: func(p providerSet) providerSet {
				p.Classifier = "claude"
				return p
			},
		},
		{
			name:      "missing codex",
			installed: []string{"claude", "gemini"},
			missingAction: func(p providerSet) providerSet {
				p.Review = "codex"
				return p
			},
		},
		{
			name:      "missing gemini",
			installed: []string{"claude", "codex"},
			missingAction: func(p providerSet) providerSet {
				p.Answer = "gemini"
				return p
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := fakeexec.InitGitFixture(t)
			installFakeProviders(t, tc.installed...)

			invocationsLog := filepath.Join(t.TempDir(), "invocations.log")
			t.Setenv("INVOCATIONS_LOG", invocationsLog)

			opts := defaultConfigOptions()
			opts.providers = tc.missingAction(opts.providers)

			configDir := t.TempDir()
			configPath := writeConfig(t, configDir, opts)

			_, _, err := runOrchestrator(t, repoDir, configPath, "run", "any task")
			if err == nil {
				t.Fatal("expected dependency validation to fail, got nil error")
			}

			var depErr *apperr.DependencyError
			if !errors.As(err, &depErr) {
				t.Errorf("expected *apperr.DependencyError, got %T: %v", err, err)
			}

			if got := invocations(t, invocationsLog); len(got) != 0 {
				t.Errorf("expected no AI invocations after a dependency validation failure, got %v", got)
			}
		})
	}
}
