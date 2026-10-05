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

func TestTestEnv_Allowlist(t *testing.T) {
	t.Cleanup(resetCredentialVarsForTest)
	t.Cleanup(resetTestEnvPassForTest)
	ScrubCredentials("JIRA_API_TOKEN")

	environ := []string{
		"PATH=/usr/bin", "HOME=/home/op", "GOFLAGS=-mod=mod", "LC_ALL=C.UTF-8", "CI=true",
		"ANTHROPIC_API_KEY=sk-ant-x", "AWS_SECRET_ACCESS_KEY=x", "GITHUB_TOKEN=ghp_x", "SSH_AUTH_SOCK=/run/agent",
		"GOPROXY=https://user:pw@proxy", "HTTPS_PROXY=http://p", "JIRA_API_TOKEN=secret", "CLAUDECODE=1",
		"FOO_BAR=1", "LC_FAKE=1", "PATHX=/x",
	}
	want := []string{"PATH=/usr/bin", "HOME=/home/op", "GOFLAGS=-mod=mod", "LC_ALL=C.UTF-8", "CI=true"}
	if got := testEnv(environ); !slices.Equal(got, want) {
		t.Errorf("testEnv =\n%q\nwant\n%q", got, want)
	}

	// test.env_pass adds exact names; a credential or session variable stays out.
	AllowTestEnv("GOPROXY", "JIRA_API_TOKEN", "CLAUDECODE", "")
	want = []string{"PATH=/usr/bin", "HOME=/home/op", "GOFLAGS=-mod=mod", "LC_ALL=C.UTF-8", "CI=true", "GOPROXY=https://user:pw@proxy"}
	if got := testEnv(environ); !slices.Equal(got, want) {
		t.Errorf("with env_pass:\n%q\nwant\n%q", got, want)
	}
}

// A provider CLI keeps ScrubbedEnv's denylist (DECISIONS.md D7): only the test command
// gets the allowlist.
func TestTestEnv_ProvidersKeepTheirEnvironment(t *testing.T) {
	t.Setenv("ORCH_UNRELATED_VAR", "1")
	if !slices.Contains(ScrubbedEnv(), "ORCH_UNRELATED_VAR=1") {
		t.Error("ScrubbedEnv lost an unrelated variable")
	}
	if slices.Contains(TestEnv(), "ORCH_UNRELATED_VAR=1") {
		t.Error("TestEnv passed a variable outside the allowlist")
	}
}

func TestIsParentSessionVar(t *testing.T) {
	if !IsParentSessionVar("CLAUDECODE") || IsParentSessionVar("PATH") {
		t.Error("IsParentSessionVar disagrees with parentSessionVars")
	}
}
