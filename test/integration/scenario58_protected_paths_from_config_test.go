package integration

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 58 (TODO.md S9): security.protected_paths is the whole list - nothing is built in - so a
// path the config leaves out is implement's to change, a leading "/" protects only the root file,
// and a config without the key is refused before any call.

func TestScenario58_ProtectedPathsComeFromTheConfigOnly(t *testing.T) {
	for name, tc := range map[string]struct {
		protected []string
		touch     string
		guarded   bool
	}{
		"left out of the list":            {[]string{}, ".claude/settings.json", false},
		"a nested file, the root guarded": {[]string{"/.gitlab-ci.yml"}, "examples/ci/.gitlab-ci.yml", false},
		"the root file, the root guarded": {[]string{"/.gitlab-ci.yml"}, ".gitlab-ci.yml", true},
		"a nested file, any depth":        {[]string{".gitlab-ci.yml"}, "examples/ci/.gitlab-ci.yml", true},
	} {
		t.Run(name, func(t *testing.T) {
			repoDir := fakeexec.InitGitFixture(t)
			r := newStageRun(t, repoDir, func(o *configOptions) { o.protectedPaths = tc.protected })
			t.Setenv("FAKE_IMPLEMENT_TOUCH_FILE", filepath.Join(repoDir, tc.touch))

			stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
			// Every run with nothing protected says so, as one without the test sandbox does.
			if none := strings.Contains(stdout, "Protected paths: none"); none != (len(tc.protected) == 0) {
				t.Errorf("the none warning shown = %v with protected_paths %v:\n%s", none, tc.protected, stdout)
			}
			if !tc.guarded {
				mustSucceed(t, stdout, stderr, err)
				return
			}
			var wfErr *apperr.WorkflowError
			if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
				t.Fatalf("expected a WorkflowError (exit 4), got %T: %v", err, err)
			}
			assertContains(t, "the error", err.Error(), "implement changed protected paths", tc.touch)
		})
	}
}

func TestScenario58_AConfigWithoutProtectedPathsIsRefused(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newStageRun(t, repoDir, func(o *configOptions) { o.protectedPaths = nil })

	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Fatalf("expected a ConfigError (exit 2), got %T: %v", err, err)
	}
	assertContains(t, "the error", err.Error(), "security.protected_paths is required", "protected_paths: []")
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("invocations = %v, want none", got)
	}
}
