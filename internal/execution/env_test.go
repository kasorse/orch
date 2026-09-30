package execution

import (
	"slices"
	"strings"
	"testing"
)

func TestScrubbedEnv(t *testing.T) {
	for name := range parentSessionVars {
		t.Setenv(name, "planted")
	}
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1") // a similar prefix is not a match
	t.Setenv("ORCH_TEST_KEEP", "kept")

	env := ScrubbedEnv()
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if parentSessionVars[name] {
			t.Errorf("%s was not scrubbed", name)
		}
	}
	for _, want := range []string{"ORCH_TEST_KEEP=kept", "CLAUDE_CODE_USE_BEDROCK=1"} {
		if !slices.Contains(env, want) {
			t.Errorf("expected %s to be inherited", want)
		}
	}
	if !slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "PATH=") }) {
		t.Error("expected PATH to be inherited")
	}
}

// TestScrub_EmptyValueAndNoEquals: an entry without "=" or with an empty value is still
// matched by name.
func TestScrub_EmptyValueAndNoEquals(t *testing.T) {
	got := scrub([]string{"CLAUDECODE=", "CLAUDE_PID", "KEEP=1"})
	if !slices.Equal(got, []string{"KEEP=1"}) {
		t.Errorf("scrub = %q", got)
	}
}

func TestScrub_CredentialVars(t *testing.T) {
	t.Cleanup(resetCredentialVarsForTest)
	ScrubCredentials("JIRA_API_TOKEN", "JIRA_EMAIL", "JEV_API_KEY", "")

	environ := []string{
		"PATH=/usr/bin", "HOME=/home/op",
		"JIRA_API_TOKEN=secret", "JIRA_EMAIL=op@example.com", "JEV_API_KEY=sk-1",
		"JIRA_API_TOKEN_OLD=kept", "XJEV_API_KEY=kept", "jira_api_token=kept",
		"CLAUDECODE=1", "CLAUDE_PID=42", "JIRA_EMAIL",
	}
	want := []string{"PATH=/usr/bin", "HOME=/home/op", "JIRA_API_TOKEN_OLD=kept", "XJEV_API_KEY=kept", "jira_api_token=kept"}
	if got := scrub(environ); !slices.Equal(got, want) {
		t.Errorf("scrub =\n%q\nwant\n%q", got, want)
	}
}

// ScrubbedEnv reads the set at call time, so names registered after a caller was built
// still apply - the provider adapters exist before the config is loaded.
func TestScrubbedEnv_CredentialVarsRegisteredLater(t *testing.T) {
	t.Cleanup(resetCredentialVarsForTest)
	t.Setenv("ORCH_TEST_CREDENTIAL", "secret")
	if !slices.Contains(ScrubbedEnv(), "ORCH_TEST_CREDENTIAL=secret") {
		t.Fatal("expected the variable before it is registered")
	}
	ScrubCredentials("ORCH_TEST_CREDENTIAL")
	if slices.Contains(ScrubbedEnv(), "ORCH_TEST_CREDENTIAL=secret") {
		t.Error("a registered credential variable was inherited")
	}
}
