package provider

import (
	"fmt"

	"orchestrator/internal/domain"
)

// Registry resolves a provider ID to its Client implementation. Constructed once at
// startup with concrete adapters (see provider/claude, provider/codex, provider/gemini).
type Registry struct {
	clients map[ID]Client
}

func NewRegistry() *Registry {
	return &Registry{clients: make(map[ID]Client)}
}

func (r *Registry) Register(id ID, client Client) {
	r.clients[id] = client
}

func (r *Registry) Resolve(id string) (Client, error) {
	client, ok := r.clients[ID(id)]
	if !ok {
		return nil, fmt.Errorf("no client registered for provider %q", id)
	}
	return client, nil
}

// Supports reports whether the client registered for providerID can enforce a. An
// unregistered provider supports nothing. It makes *Registry a config.AuthorityChecker.
func (r *Registry) Supports(providerID string, a domain.Authority) bool {
	client, ok := r.clients[ID(providerID)]
	return ok && client.Supports(a)
}

// ReportsCost reports whether the client registered for providerID reports a call's cost. An
// unregistered provider, or one that is not a CostReporter, reports none. It makes *Registry a
// config.CostChecker.
func (r *Registry) ReportsCost(providerID string) bool {
	client, ok := r.clients[ID(providerID)]
	if !ok {
		return false
	}
	reporter, ok := client.(CostReporter)
	return ok && reporter.ReportsCost()
}
