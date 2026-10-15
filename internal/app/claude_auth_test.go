package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
	"orchestrator/internal/provider/claude"
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
	if _, err := applyClaudeAuth(registry, auth, envLookup(map[string]string{"ORCH_KEY": "  " + key + "\n"})); err != nil {
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
	_, err := applyClaudeAuth(registry, auth, envLookup(map[string]string{"ORCH_KEY": "  "}))
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
	if _, err := applyClaudeAuth(registry, auth, envLookup(map[string]string{"ORCH_KEY": "present"})); err != nil {
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
	if _, err := applyClaudeAuth(provider.NewRegistry(), domain.ClaudeAuth{Mode: domain.ClaudeAuthLogin}, envLookup(nil)); err != nil {
		t.Errorf("err = %v", err)
	}
}
