package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const credentialTestToken = "glpat-test-value-0123"

// credentialConfig writes a listener config whose gitlab section names baseURL and the variable
// GITLAB_TEST_TOKEN, which holds credentialTestToken.
func credentialConfig(t *testing.T, baseURL string) string {
	t.Helper()
	t.Setenv("GITLAB_TEST_TOKEN", credentialTestToken)
	path := filepath.Join(t.TempDir(), "listener.yaml")
	yaml := "version: 2\ngitlab:\n  base_url: " + baseURL + "\n  token_env: GITLAB_TEST_TOKEN\n  username: oauth2\n  allowed_groups: [backend]\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// helper feeds git's protocol to `orchestrator --config <cfg> git-credential <action>`.
func helper(t *testing.T, cfg, action, request string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = Run(context.Background(), []string{"--config", cfg, "git-credential", action}, strings.NewReader(request), &out, &errOut)
	return out.String(), errOut.String(), err
}

func TestGitCredential_GetAnswersForTheConfiguredHost(t *testing.T) {
	cfg := credentialConfig(t, "https://gitlab.corp")
	for _, request := range []string{
		"protocol=https\nhost=gitlab.corp\n\n",
		"protocol=https\nhost=gitlab.corp\npath=backend/x.git\n\n",
		"protocol=https\nhost=GitLab.Corp\n\n",
		"protocol=https\nhost=gitlab.corp:443\n\n",
		"protocol=https\nhost=gitlab.corp\nusername=someone\n", // no blank line: EOF ends it
	} {
		stdout, stderr, err := helper(t, cfg, "get", request)
		if err != nil {
			t.Fatalf("%q: %v", request, err)
		}
		if want := "username=oauth2\npassword=" + credentialTestToken + "\n"; stdout != want {
			t.Errorf("%q: stdout = %q, want %q", request, stdout, want)
		}
		if strings.Contains(stderr, credentialTestToken) {
			t.Errorf("%q: the token is on stderr", request)
		}
	}
}

func TestGitCredential_GetIsSilentOtherwise(t *testing.T) {
	cfg := credentialConfig(t, "https://gitlab.corp")
	for _, request := range []string{
		"protocol=https\nhost=other.corp\n\n",
		"protocol=https\nhost=gitlab.corp.evil.example\n\n",
		"protocol=https\nhost=evil.example\npath=gitlab.corp\n\n",
		"protocol=https\nhost=gitlab.corp:8443\n\n",
		"protocol=http\nhost=gitlab.corp\n\n",
		"protocol=ssh\nhost=gitlab.corp\n\n",
		"protocol=http\nhost=gitlab.corp:443\n\n",
		"protocol=https\n\n",
		"host=gitlab.corp\n\n",
		"\n",
		"",
	} {
		stdout, stderr, err := helper(t, cfg, "get", request)
		if err != nil || stdout != "" || stderr != "" {
			t.Errorf("%q: stdout = %q, stderr = %q, err = %v; want silence", request, stdout, stderr, err)
		}
	}
}

func TestGitCredential_ExplicitPortAndContextPath(t *testing.T) {
	cfg := credentialConfig(t, "https://gitlab.corp:8443/git")
	if stdout, _, err := helper(t, cfg, "get", "protocol=https\nhost=gitlab.corp:8443\n\n"); err != nil || !strings.Contains(stdout, "password="+credentialTestToken) {
		t.Errorf("the configured port: %q, %v", stdout, err)
	}
	for _, host := range []string{"gitlab.corp", "gitlab.corp:443", "gitlab.corp:9443"} {
		if stdout, _, err := helper(t, cfg, "get", "protocol=https\nhost="+host+"\n\n"); err != nil || stdout != "" {
			t.Errorf("host %s: %q, %v; want silence", host, stdout, err)
		}
	}
}

func TestGitCredential_StoreAndEraseDoNothing(t *testing.T) {
	cfg := credentialConfig(t, "https://gitlab.corp")
	for _, action := range []string{"store", "erase"} {
		stdout, _, err := helper(t, cfg, action, "protocol=https\nhost=gitlab.corp\nusername=oauth2\npassword=x\n\n")
		if err != nil || stdout != "" {
			t.Errorf("%s: %q, %v", action, stdout, err)
		}
	}
}

func TestGitCredential_NoTokenNoAnswer(t *testing.T) {
	cfg := credentialConfig(t, "https://gitlab.corp")
	t.Setenv("GITLAB_TEST_TOKEN", "  ")
	if stdout, _, err := helper(t, cfg, "get", "protocol=https\nhost=gitlab.corp\n\n"); err != nil || stdout != "" {
		t.Errorf("a blank token: %q, %v", stdout, err)
	}
}

func TestGitCredential_RefusesWhatTheProtocolCannotCarry(t *testing.T) {
	cfg := credentialConfig(t, "https://gitlab.corp")
	t.Setenv("GITLAB_TEST_TOKEN", "abc\npassword=injected")
	stdout, stderr, err := helper(t, cfg, "get", "protocol=https\nhost=gitlab.corp\n\n")
	if err == nil || stdout != "" {
		t.Errorf("a token with a line break: %q, %v", stdout, err)
	}
	if strings.Contains(stderr, "injected") || (err != nil && strings.Contains(err.Error(), "injected")) {
		t.Error("the token is in the message")
	}
}

func TestGitCredential_BadInput(t *testing.T) {
	cfg := credentialConfig(t, "https://gitlab.corp")
	for _, args := range [][]string{
		{"git-credential"},
		{"git-credential", "fill"},
		{"git-credential", "get", "extra"},
	} {
		var out, errOut bytes.Buffer
		if err := Run(context.Background(), append([]string{"--config", cfg}, args...), strings.NewReader(""), &out, &errOut); err == nil {
			t.Errorf("%v succeeded", args)
		}
	}
	if _, _, err := helper(t, cfg, "get", "no equals sign\n\n"); err == nil {
		t.Error("a malformed request was accepted")
	}
	if _, _, err := helper(t, filepath.Join(t.TempDir(), "missing.yaml"), "get", "protocol=https\nhost=gitlab.corp\n\n"); err == nil {
		t.Error("a missing config was accepted")
	}
	noGitLab := filepath.Join(t.TempDir(), "l.yaml")
	if err := os.WriteFile(noGitLab, []byte("version: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := helper(t, noGitLab, "get", "protocol=https\nhost=gitlab.corp\n\n"); err == nil {
		t.Error("a config without a gitlab section was accepted")
	}
}

func TestListenerGitEnv(t *testing.T) {
	got := listenerGitEnv("/usr/local/bin/orchestrator", "https://gitlab.corp")
	want := []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=credential.https://gitlab.corp.helper",
		"GIT_CONFIG_VALUE_0=!/usr/local/bin/orchestrator git-credential",
	}
	if !slices.Equal(got, want) {
		t.Errorf("listenerGitEnv = %q, want %q", got, want)
	}

	got = listenerGitEnv("/opt/my tools/orchestrator", "https://gitlab.corp/git", "--config", "/etc/l y.yaml")
	if v := got[2]; v != "GIT_CONFIG_VALUE_0=!'/opt/my tools/orchestrator' --config '/etc/l y.yaml' git-credential" {
		t.Errorf("the helper command = %q", v)
	}
	for _, e := range got {
		if strings.Contains(e, credentialTestToken) {
			t.Errorf("the token is in %q", e)
		}
	}
}

func TestWithListenerGit(t *testing.T) {
	base := []string{"PATH=/bin", "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=a.b", "GIT_CONFIG_VALUE_0=1", "GIT_CONFIG_KEY_1=c.d", "GIT_CONFIG_VALUE_1=2"}
	got := withListenerGit(base, "/x/orchestrator", "https://h")
	for _, want := range []string{
		"PATH=/bin", "GIT_CONFIG_KEY_0=a.b", "GIT_CONFIG_VALUE_1=2",
		"GIT_CONFIG_COUNT=3", "GIT_CONFIG_KEY_2=credential.https://h.helper", "GIT_CONFIG_VALUE_2=!/x/orchestrator git-credential",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if n := countPrefix(got, "GIT_CONFIG_COUNT="); n != 1 {
		t.Errorf("%d GIT_CONFIG_COUNT entries", n)
	}
	if got := withListenerGit([]string{"PATH=/bin"}, "/x/o", "https://h"); !slices.Contains(got, "GIT_CONFIG_COUNT=1") || !slices.Contains(got, "GIT_CONFIG_KEY_0=credential.https://h.helper") {
		t.Errorf("without entries: %q", got)
	}
	if len(base) != 6 || base[1] != "GIT_CONFIG_COUNT=2" {
		t.Error("the base environment was modified")
	}
}

func countPrefix(env []string, prefix string) int {
	n := 0
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}
