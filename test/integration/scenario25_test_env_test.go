package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 25 (specs/hardening/SPEC.md §6.1, §6.2): the test command inherits only the
// allowlist and test.env_pass - no API key, nothing unrelated - while a provider CLI keeps
// its scrubbed-but-full environment.
func TestScenario25_TestEnvAllowlist(t *testing.T) {
	envOut := filepath.Join(t.TempDir(), "test.env")
	envDir := t.TempDir()
	t.Setenv("FAKE_ENV_DIR", envDir)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-scenario25")
	t.Setenv("TZ", "Europe/Moscow")
	t.Setenv("ORCH_SCENARIO25_EXTRA", "passed")
	t.Setenv("ORCH_SCENARIO25_OTHER", "dropped")

	r := newStageRun(t, fakeexec.InitGitFixture(t), func(o *configOptions) {
		o.testCommand = "env > '" + envOut + "'"
		o.testEnvPass = []string{"ORCH_SCENARIO25_EXTRA"}
	})
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "investigate", readOnlyResponse)
	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--stage", "investigation", "--stage", "testing", "check the tests")
	mustSucceed(t, stdout, stderr, err)

	testEnv := readEnvDump(t, envOut)
	for _, want := range []string{"PATH", "HOME", "TZ", "ORCH_SCENARIO25_EXTRA"} {
		if _, ok := testEnv[want]; !ok {
			t.Errorf("the test command did not inherit %s", want)
		}
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "ORCH_SCENARIO25_OTHER", "FAKE_ENV_DIR"} {
		if _, ok := testEnv[name]; ok {
			t.Errorf("the test command inherited %s", name)
		}
	}

	providerEnv := readEnvDump(t, filepath.Join(envDir, "investigate.env"))
	// ANTHROPIC_API_KEY is a credential of the config now (specs/claude-auth/SPEC.md §5), so
	// no child inherits it. The claude adapter passes it on only in api mode, from the key it
	// was given (specs/claude-auth/SPEC.md §7); this run is in api mode (auto with the key set).
	if _, ok := providerEnv["ORCH_SCENARIO25_OTHER"]; !ok {
		t.Errorf("the provider CLI lost ORCH_SCENARIO25_OTHER; only the test command gets the allowlist")
	}
	if got := providerEnv["ANTHROPIC_API_KEY"]; got != "sk-ant-scenario25" {
		t.Errorf("the claude CLI got ANTHROPIC_API_KEY %q in api mode, want the configured key", got)
	}
}

// 25b: a credential-like name in test.env_pass is refused before anything runs.
func TestScenario25_EnvPassRejectsCredentials(t *testing.T) {
	r := newStageRun(t, fakeexec.InitGitFixture(t), func(o *configOptions) {
		o.testEnvPass = []string{"GITLAB_TOKEN"}
	})
	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--stage", "testing", "run the tests")
	if err == nil {
		t.Fatalf("a credential in test.env_pass was accepted\nstdout: %s", stdout)
	}
	if !strings.Contains(err.Error()+stdout+stderr, `test.env_pass[0] "GITLAB_TOKEN": looks like a credential`) {
		t.Errorf("the refusal does not name the key: %v\n%s%s", err, stdout, stderr)
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("calls before the refusal: %v", got)
	}
}

// readEnvDump parses `env` output into name -> value (first line of each value).
func readEnvDump(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("environment dump: %v", err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if name, value, ok := strings.Cut(line, "="); ok && name != "" && !strings.ContainsAny(name, " \t") {
			env[name] = value
		}
	}
	return env
}
