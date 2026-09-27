package config

import (
	"fmt"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

// AuthorityChecker reports whether a provider can enforce an authority.
// *provider.Registry implements it; tests pass a fake.
type AuthorityChecker interface {
	Supports(providerID string, a domain.Authority) bool
}

// ValidateAuthorities verifies that every configured route - each action, the translator,
// and an LLM decision provider - is routed to a provider that can enforce the authority
// that route runs with (specs/permissions/SPEC.md §6.5). It runs right after
// ValidateDependencies, so a route that would run with authority it cannot enforce stops
// the run before any AI call. Authority does not depend on the run mode, so this one check
// is complete.
func ValidateAuthorities(cfg Config, checker AuthorityChecker) error {
	var errs []error
	for _, r := range authorityRoutes(cfg) {
		if r.Provider == "" || checker.Supports(r.Provider, r.Authority) {
			continue
		}
		errs = append(errs, &apperr.AuthorityError{
			Provider:  r.Provider,
			Route:     r.Name,
			Authority: string(r.Authority),
			Reason:    "The orchestrator does not run a provider with authority it cannot enforce, rather than let it run with unknown authority (specs/permissions/SPEC.md §6).",
			Remedy:    authorityRemedy(r, checker),
		})
	}
	if len(errs) == 0 {
		return nil
	}
	return &authorityErrors{errs: errs}
}

type authorityRoute struct {
	Name      string
	Provider  string
	Authority domain.Authority
	// Key is the config key that selects the provider, for the remedy text.
	Key string
}

// authorityRoutes lists every provider call the config can make, with the authority it
// runs under: workflow actions from domain.AuthorityFor, and the translator and an LLM
// decision provider with AuthorityNone, since they only read the request text.
func authorityRoutes(cfg Config) []authorityRoute {
	var routes []authorityRoute
	for _, pm := range cfg.actionProviderModels() {
		routes = append(routes, authorityRoute{
			Name:      pm.Action,
			Provider:  pm.Provider,
			Authority: domain.AuthorityFor(domain.ActionType(pm.Action)),
			Key:       "actions." + pm.Action + ".provider",
		})
	}
	for _, pm := range decisionProviderModels(cfg) {
		routes = append(routes, authorityRoute{
			Name:      pm.Action,
			Provider:  pm.Provider,
			Authority: domain.AuthorityNone,
			Key:       pm.Action + ".provider",
		})
	}
	return routes
}

func authorityRemedy(r authorityRoute, checker AuthorityChecker) string {
	var able []string
	for _, id := range provider.SupportedProviders() {
		if checker.Supports(string(id), r.Authority) {
			able = append(able, string(id))
		}
	}
	if len(able) == 0 {
		return fmt.Sprintf("no supported provider can enforce %q yet; see specs/permissions/SPEC.md §6", r.Authority)
	}
	return fmt.Sprintf("route %s to a provider that supports it: %s (%s: %s)",
		r.Name, strings.Join(able, ", "), r.Key, able[0])
}

// authorityErrors reports every unenforceable route at once, like ValidateDependencies
// reports every missing executable. Unwrap exposes each *apperr.AuthorityError to
// errors.As, so the exit code mapping sees them.
type authorityErrors struct {
	errs []error
}

func (e *authorityErrors) Error() string {
	parts := make([]string, len(e.errs))
	for i, err := range e.errs {
		parts[i] = err.Error()
	}
	return "configuration validation failed:\n\n" + strings.Join(parts, "\n\n")
}

func (e *authorityErrors) Unwrap() []error { return e.errs }
