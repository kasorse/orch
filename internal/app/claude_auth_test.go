package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/provider/claude"
)

func envLookup(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

func probeEnv(t *testing.T, fake *execution.FakeProcessExecutor, registry *provider.Registry) []string {
	t.Helper()
	client, err := registry.Resolve(string(provider.Claude))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.(*claude.Client).CLIVersion(context.Background()); err != nil {
		t.Fatalf("version probe: %v", err)
	}
	return fake.Calls[len(fake.Calls)-1].Env
}

// The key is read once, trimmed, and reaches claude's environment under ANTHROPIC_API_KEY.
func TestApplyClaudeAuth_APIPassesTheTrimmedKey(t *testing.T) {
	key := "sk-" + "ant-api03-" + strings.Repeat("k", 20)
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "2.1.299"}}}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, claude.New(fake))

	auth := domain.ClaudeAuth{Requested: domain.ClaudeAuthAPI, Mode: domain.ClaudeAuthAPI, KeyEnv: "ORCH_KEY"}
	if _, err := applyClaudeAuth(registry, auth, nil, envLookup(map[string]string{"ORCH_KEY": "  " + key + "\n"})); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(probeEnv(t, fake, registry), "ANTHROPIC_API_KEY="+key) {
		t.Error("the probe did not get the trimmed key as ANTHROPIC_API_KEY")
	}
}

func TestApplyClaudeAuth_APIWithoutAKeyIsAConfigError(t *testing.T) {
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, claude.New(&execution.FakeProcessExecutor{}))
	auth := domain.ClaudeAuth{Requested: domain.ClaudeAuthAPI, Mode: domain.ClaudeAuthAPI, KeyEnv: "ORCH_KEY"}
	_, err := applyClaudeAuth(registry, auth, nil, envLookup(map[string]string{"ORCH_KEY": "  "}))
	var cerr *apperr.ConfigError
	if !errors.As(err, &cerr) {
		t.Fatalf("err = %v, want a config error", err)
	}
}

// Login hands over no key, and the adapter is usable (the probe runs).
func TestApplyClaudeAuth_LoginPassesNoKey(t *testing.T) {
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "2.1.299"}}}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, claude.New(fake))
	auth := domain.ClaudeAuth{Requested: domain.ClaudeAuthLogin, Mode: domain.ClaudeAuthLogin, KeyEnv: "ORCH_KEY"}
	if _, err := applyClaudeAuth(registry, auth, nil, envLookup(map[string]string{"ORCH_KEY": "present"})); err != nil {
		t.Fatal(err)
	}
	for _, kv := range probeEnv(t, fake, registry) {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.Contains(kv, "present") {
			t.Errorf("login mode passed %q", kv)
		}
	}
}

// A registry without the real adapter has nothing to set.
func TestApplyClaudeAuth_NoAdapterIsNotAnError(t *testing.T) {
	if _, err := applyClaudeAuth(provider.NewRegistry(), domain.ClaudeAuth{Mode: domain.ClaudeAuthLogin}, nil, envLookup(nil)); err != nil {
		t.Errorf("err = %v", err)
	}
}

// specs/claude-auth/SPEC.md §17.4: the map reaches the adapter, so the version probe sees it.
func TestApplyClaudeAuth_APIEnvReachesTheProbe(t *testing.T) {
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "2.1.299"}}}
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, claude.New(fake))
	auth := domain.ClaudeAuth{Requested: domain.ClaudeAuthAPI, Mode: domain.ClaudeAuthAPI, KeyEnv: "ORCH_KEY"}
	apiEnv := map[string]string{"ORCH_TEST_GATEWAY_PROFILE": "claude-bedrock"}
	if _, err := applyClaudeAuth(registry, auth, apiEnv, envLookup(map[string]string{"ORCH_KEY": "k"})); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(probeEnv(t, fake, registry), "ORCH_TEST_GATEWAY_PROFILE=claude-bedrock") {
		t.Error("the probe did not get the api_env variable")
	}
}

// §17.5, §13 #12: the names are recorded, the console shows the base URL's host and no other
// value, and the warning says where the base URL came from.
func TestClaudeEnvReport(t *testing.T) {
	apiEnv := map[string]string{
		"ANTHROPIC_BASE_URL":           "https://ai-gateway.example.internal/v1",
		"AWS_PROFILE":                  "claude-bedrock",
		"ANTHROPIC_DEFAULT_OPUS_MODEL": "bedrock/eu.anthropic.claude-opus-5-5",
	}
	api := func() domain.ClaudeAuth {
		return domain.ClaudeAuth{Requested: "api", Mode: domain.ClaudeAuthAPI, KeyEnv: "K"}
	}

	t.Run("api with api_env", func(t *testing.T) {
		auth := api()
		line, notices := claudeEnvReport(&auth, apiEnv, []string{"ANTHROPIC_AUTH_TOKEN"}, envLookup(map[string]string{"ANTHROPIC_BASE_URL": "https://shell.example"}))
		want := "claude env (api): ANTHROPIC_BASE_URL (ai-gateway.example.internal), ANTHROPIC_DEFAULT_OPUS_MODEL, AWS_PROFILE"
		if line != want {
			t.Errorf("line = %q, want %q", line, want)
		}
		if !slices.Equal(auth.APIEnv, []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "AWS_PROFILE"}) {
			t.Errorf("APIEnv = %v", auth.APIEnv)
		}
		wantNotices := []string{
			"ignored in api mode: ANTHROPIC_AUTH_TOKEN",
			"ANTHROPIC_BASE_URL is set (providers.claude.api_env): the API key goes to ai-gateway.example.internal",
		}
		if !slices.Equal(notices, wantNotices) {
			t.Errorf("notices = %q, want %q", notices, wantNotices)
		}
		all := line + strings.Join(notices, "\n")
		for _, v := range []string{"claude-bedrock", "bedrock/eu", "/v1", "shell.example"} {
			if strings.Contains(all, v) {
				t.Errorf("the output shows %q:\n%s", v, all)
			}
		}
	})

	t.Run("api with an inherited base URL", func(t *testing.T) {
		auth := api()
		line, notices := claudeEnvReport(&auth, nil, nil, envLookup(map[string]string{"ANTHROPIC_BASE_URL": "https://shell.example:8443/x"}))
		if line != "" || auth.APIEnv != nil {
			t.Errorf("line = %q, APIEnv = %v; want none without api_env", line, auth.APIEnv)
		}
		want := []string{"ANTHROPIC_BASE_URL is set (inherited from the environment): the API key goes to shell.example:8443"}
		if !slices.Equal(notices, want) {
			t.Errorf("notices = %q, want %q", notices, want)
		}
	})

	t.Run("api without either", func(t *testing.T) {
		auth := api()
		if line, notices := claudeEnvReport(&auth, nil, nil, envLookup(nil)); line != "" || notices != nil || auth.APIEnv != nil {
			t.Errorf("got %q, %q, %v; want nothing", line, notices, auth.APIEnv)
		}
	})

	t.Run("login records and names the removed variables", func(t *testing.T) {
		auth := domain.ClaudeAuth{Requested: "login", Mode: domain.ClaudeAuthLogin, KeyEnv: "K"}
		line, notices := claudeEnvReport(&auth, apiEnv, []string{"ANTHROPIC_BASE_URL"}, envLookup(map[string]string{"ANTHROPIC_BASE_URL": "https://shell.example"}))
		if line != "" {
			t.Errorf("line = %q, want none in login mode", line)
		}
		if !slices.Equal(notices, []string{"ignored in login mode: ANTHROPIC_BASE_URL"}) {
			t.Errorf("notices = %q", notices)
		}
		if !slices.Equal(auth.APIEnv, []string{"ANTHROPIC_BASE_URL"}) {
			t.Errorf("APIEnv = %v, want the removed name", auth.APIEnv)
		}
	})
}
