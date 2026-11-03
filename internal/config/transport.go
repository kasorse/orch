// This file defines and validates how claude and codex calls are made: their CLI, or their
// HTTP API (specs/api-transport/SPEC.md §2).
package config

import (
	"fmt"
	"maps"
	"math"
	"net"
	"net/url"
	"slices"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

// providers.*.transport values. The empty string means cli.
const (
	TransportCLI = "cli"
	TransportAPI = "api"
)

// TransportConfig is the part of providers.claude and providers.codex that chooses the
// transport. BaseURL, WebTools and Prices only exist for transport: api.
type TransportConfig struct {
	Transport string `yaml:"transport"`
	// BaseURL is the API endpoint, e.g. a LiteLLM gateway; empty is the provider's own.
	BaseURL string `yaml:"base_url"`
	// WebTools offers the endpoint's server web tools to web steps; nil is the default (§6).
	WebTools *bool `yaml:"web_tools"`
	// Prices are USD per million tokens by full model ID, over the built-in table.
	Prices map[string]ModelPrice `yaml:"prices"`
}

// ModelPrice is one model's price in USD per million tokens. Input and Output are required;
// the cache rates default to 0.1× and 1.25× Input (pointers, so 0 can be written).
type ModelPrice struct {
	Input      *float64 `yaml:"input"`
	Output     *float64 `yaml:"output"`
	CacheRead  *float64 `yaml:"cache_read"`
	CacheWrite *float64 `yaml:"cache_write"`
}

// EffectiveTransport is transport, with the default cli for an absent value.
func (t TransportConfig) EffectiveTransport() string {
	if t.Transport == "" {
		return TransportCLI
	}
	return t.Transport
}

// UsesAPI reports whether calls go to the HTTP API instead of the CLI.
func (t TransportConfig) UsesAPI() bool { return t.Transport == TransportAPI }

// WebToolsOn is web_tools, defaulting to true for the provider's own endpoint and false for
// a base_url: a gateway to Bedrock has no server web tools.
func (t TransportConfig) WebToolsOn() bool {
	if t.WebTools != nil {
		return *t.WebTools
	}
	return t.BaseURL == ""
}

// UsesAPI reports whether the provider id is on transport: api. Only claude and codex have one.
func (p ProvidersConfig) UsesAPI(id string) bool {
	switch id {
	case string(provider.Claude):
		return p.Claude.UsesAPI()
	case string(provider.Codex):
		return p.Codex.UsesAPI()
	}
	return false
}

// UserSettingsOverAPI reports whether an action with user_settings runs on a provider over the API
// transport: then the orchestrator reads the operator's skills itself (SPEC §5.4). Otherwise nothing
// under ~/.claude is read.
func UserSettingsOverAPI(cfg Config) bool {
	for _, pm := range cfg.actionProviderModels() {
		if slices.Contains(extensionActions, pm.Action) && cfg.Extensions.UserSettingsFor(domain.ActionType(pm.Action)) && cfg.Providers.UsesAPI(pm.Provider) {
			return true
		}
	}
	return false
}

// RoutedModels lists the distinct models the config routes to provider id, in route order:
// what the API transport's model check asks the endpoint for (SPEC §9).
func RoutedModels(cfg Config, id string) []string {
	var models []string
	for _, r := range modelRoutes(cfg) {
		if m := strings.TrimSpace(r.Model); r.Provider == id && m != "" && !slices.Contains(models, m) {
			models = append(models, m)
		}
	}
	return models
}

// transportProblems validates the transport keys of providers.<name> (SPEC §2 items 1, 3, 4, 6).
func transportProblems(cfg Config, name string, t TransportConfig) []string {
	key := "providers." + name
	switch t.Transport {
	case "", TransportCLI, TransportAPI:
	default:
		return []string{fmt.Sprintf("%s.transport must be cli (the default) or api, got %q", key, t.Transport)}
	}
	if !t.UsesAPI() {
		var problems []string
		for _, set := range []struct {
			key string
			on  bool
		}{{"base_url", t.BaseURL != ""}, {"web_tools", t.WebTools != nil}, {"prices", t.Prices != nil}} {
			if set.on {
				problems = append(problems, fmt.Sprintf("%s.%s needs %s.transport: api; the CLI does not use it", key, set.key, key))
			}
		}
		return problems
	}
	var problems []string
	if t.BaseURL != "" {
		if problem := baseURLProblem(t.BaseURL); problem != "" {
			problems = append(problems, key+".base_url: "+problem)
		}
	}
	for _, model := range slices.Sorted(maps.Keys(t.Prices)) {
		problems = append(problems, priceProblems(fmt.Sprintf("%s.prices[%q]", key, model), model, t.Prices[model])...)
	}
	if name == string(provider.Claude) {
		for _, r := range modelRoutes(cfg) {
			if r.Provider == name && domain.ClaudeModelAlias(r.Model) {
				problems = append(problems, fmt.Sprintf("%s: %s is a claude CLI alias; with %s.transport: api name the full model ID (e.g. claude-sonnet-5-5, or the gateway's name)",
					r.Key, strings.TrimSpace(r.Model), key))
			}
		}
	}
	return problems
}

// baseURLProblem reports why a base_url is refused, without quoting it.
func baseURLProblem(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "must be an absolute URL such as https://ai-gateway.example.internal"
	}
	if u.User != nil {
		return "must not hold a user or password; the key comes only from the environment"
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "must not have a query or a fragment"
	}
	switch u.Scheme {
	case "https":
	case "http":
		if host := u.Hostname(); host != "localhost" && !isLoopback(host) {
			return "must use https (plain http only for localhost)"
		}
	default:
		return "must use https"
	}
	return apiEnvValueProblem(raw)
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func priceProblems(key, model string, p ModelPrice) []string {
	var problems []string
	if strings.TrimSpace(model) == "" || strings.TrimSpace(model) != model {
		problems = append(problems, key+": the model ID must not be empty or padded with spaces")
	}
	for _, f := range []struct {
		name     string
		v        *float64
		required bool
	}{{"input", p.Input, true}, {"output", p.Output, true}, {"cache_read", p.CacheRead, false}, {"cache_write", p.CacheWrite, false}} {
		switch {
		case f.v == nil && f.required:
			problems = append(problems, fmt.Sprintf("%s.%s is required (USD per million tokens)", key, f.name))
		case f.v != nil && (*f.v < 0 || math.IsNaN(*f.v) || math.IsInf(*f.v, 0)):
			problems = append(problems, fmt.Sprintf("%s.%s must be a finite number >= 0, got %v", key, f.name, *f.v))
		}
	}
	return problems
}

// transportAuthError is the refusal of SPEC §2 item 2: transport: api resolved to the login
// mode, which only the CLI has.
func transportAuthError(name, decidedBy, keyEnv string) error {
	return &apperr.ConfigError{Msg: fmt.Sprintf(
		"providers.%s.transport is api, which needs the api auth mode, but the mode resolved to login (decided by %s); "+
			"export the key in %s and use --%s-auth api or auto, or set providers.%s.transport: cli",
		name, decidedBy, keyEnv, name, name)}
}
