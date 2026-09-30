package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 19g (specs/jira/SPEC.md F1, §12): neither the test command, which runs code a model
// wrote, nor any provider CLI inherits a credential variable the config names. The
// orchestrator itself still read them: the fetch and the decision both authenticated.
func TestScenario19g_CredentialsScrubbed(t *testing.T) {
	envOut := filepath.Join(t.TempDir(), "test.env")
	envDir := t.TempDir()
	t.Setenv("ENV_OUT", envOut)
	t.Setenv("FAKE_ENV_DIR", envDir)

	r := newJiraRun(t)
	cfg := jevConfig(t, r.jev, func(o *configOptions) {
		o.testCommand = `env > "$ENV_OUT"`
		o.jira = &jiraOptions{baseURL: r.jira.URL()}
	})

	stdout, stderr, err := runOrchestrator(t, r.repoDir, cfg, "run", "--jira", "PMC-1")
	mustSucceed(t, stdout, stderr, err)
	if len(r.jira.Requests()) != 1 || len(r.jev.Bodies()) == 0 {
		t.Fatalf("expected the fetch and the decision to happen (jira %d, jev %d)", len(r.jira.Requests()), len(r.jev.Bodies()))
	}

	dumps := map[string]string{"test command": envOut}
	for _, action := range []string{"analyze", "implement", "review"} {
		dumps[action] = filepath.Join(envDir, action+".env")
	}
	for who, path := range dumps {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s environment: %v", who, err)
		}
		env := "\n" + string(data)
		for _, name := range []string{"JIRA_API_TOKEN", "JIRA_EMAIL", "JEV_API_KEY"} {
			if strings.Contains(env, "\n"+name+"=") {
				t.Errorf("%s inherited %s", who, name)
			}
		}
		for _, secret := range []string{jiraToken, jiraEmail, jevKey} {
			if strings.Contains(env, secret) {
				t.Errorf("%s environment contains the value %q", who, secret)
			}
		}
		if !strings.Contains(env, "\nPATH=") {
			t.Errorf("%s did not inherit PATH", who)
		}
	}
}
