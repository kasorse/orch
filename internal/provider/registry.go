package provider

import "fmt"

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
