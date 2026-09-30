package integration

import (
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// jiraSectionYAML is a valid basic-auth jira section (specs/jira/SPEC.md §7).
const jiraSectionYAML = `
jira:
  base_url: http://127.0.0.1:1
  auth: basic
  email_env: JIRA_EMAIL
  token_env: JIRA_API_TOKEN
`

// orchestrator validate checks the Jira variables whenever the section is present
// (SPEC.md §7.2): with none set it exits 2, naming each variable and no value.
func TestJiraConfig_ValidateChecksVariables(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")
	jev := startJev(t, scriptBugfixMedium())
	t.Setenv("JIRA_EMAIL", "")
	t.Setenv("JIRA_API_TOKEN", "")

	opts := defaultConfigOptions()
	opts.jevURL = jev.URL()
	opts.extraYAML = jiraSectionYAML
	configPath := writeConfig(t, t.TempDir(), opts)

	_, _, err := runOrchestrator(t, repoDir, configPath, "validate")

	var depErr *apperr.DependencyError
	if !errors.As(err, &depErr) {
		t.Fatalf("expected *apperr.DependencyError, got %T: %v", err, err)
	}
	if got := exitcode.FromError(err); got != exitcode.ConfigOrDependency {
		t.Errorf("exit code = %d, want %d", got, exitcode.ConfigOrDependency)
	}
	for _, want := range []string{
		"jira.email_env names JIRA_EMAIL, which is not set",
		"jira.token_env names JIRA_API_TOKEN, which is not set",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}

	t.Setenv("JIRA_EMAIL", "me@example.com")
	t.Setenv("JIRA_API_TOKEN", "secret-token")
	if _, _, err := runOrchestrator(t, repoDir, configPath, "validate"); err != nil {
		t.Fatalf("validate with the variables set: %v", err)
	}
	if got := jev.Bodies(); len(got) != 0 {
		t.Errorf("expected no Jev requests, got %d", len(got))
	}
}
