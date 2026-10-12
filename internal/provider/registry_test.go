package provider_test

import (
	"testing"

	"orchestrator/internal/provider"
	"orchestrator/internal/provider/claude"
	"orchestrator/internal/provider/codex"
	"orchestrator/internal/provider/gemini"
)

func TestRegistry_ReportsCost(t *testing.T) {
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, claude.New(nil))
	registry.Register(provider.Codex, codex.New(nil))
	registry.Register(provider.Gemini, gemini.New(nil))

	for id, want := range map[string]bool{"claude": true, "codex": false, "gemini": false, "unregistered": false} {
		if got := registry.ReportsCost(id); got != want {
			t.Errorf("ReportsCost(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestCostReporter_OnlyClaudeImplementsIt(t *testing.T) {
	if _, ok := provider.Client(claude.New(nil)).(provider.CostReporter); !ok {
		t.Error("claude.Client is not a provider.CostReporter")
	}
	if _, ok := provider.Client(codex.New(nil)).(provider.CostReporter); ok {
		t.Error("codex.Client must not be a provider.CostReporter")
	}
	if _, ok := provider.Client(gemini.New(nil)).(provider.CostReporter); ok {
		t.Error("gemini.Client must not be a provider.CostReporter")
	}
}
