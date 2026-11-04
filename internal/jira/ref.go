package jira

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

// ParseRef turns the operator's --jira value into an issue key, without any network
// access (SPEC.md §6.1). It accepts a key in any case, or the issue's /browse/<KEY> URL on
// the configured instance. The URL form only names the key: the request always goes to
// baseURL, never to the URL pasted (SPEC.md F3). Errors are *apperr.ConfigError, and show
// hosts only, never a whole URL, which could carry credentials.
func ParseRef(ref, baseURL string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.Contains(ref, "://") {
		key := strings.ToUpper(ref)
		if !domain.ValidTicketKey(key) {
			return "", apperr.NewConfigError(fmt.Sprintf(
				"--jira %q is not an issue key: pass a key such as PMC-2789, or its /browse/<KEY> URL", ref), nil)
		}
		return key, nil
	}

	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return "", apperr.NewConfigError("--jira value is not a valid URL: pass the issue key or its /browse/<KEY> URL", nil)
	}
	if u.User != nil {
		return "", apperr.NewConfigError(fmt.Sprintf(
			"--jira URL for host %q must not contain credentials: pass the issue key or its /browse/<KEY> URL", u.Hostname()), nil)
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", apperr.NewConfigError("jira.base_url is not a valid URL", nil)
	}

	// The host is checked first: it is what a URL from another instance gets wrong, and
	// the most useful thing to name.
	hostMismatch := apperr.NewConfigError(fmt.Sprintf(
		"--jira URL host %q does not match jira.base_url %q", u.Host, base.Host), nil)
	if !strings.EqualFold(u.Hostname(), base.Hostname()) {
		return "", hostMismatch
	}
	if !strings.EqualFold(u.Scheme, base.Scheme) {
		return "", apperr.NewConfigError(fmt.Sprintf(
			"--jira URL scheme %q does not match jira.base_url's %q", u.Scheme, base.Scheme), nil)
	}
	if port(u) != port(base) {
		return "", hostMismatch
	}

	// On Data Center the base carries a context path (https://jira.corp/jira); issue URLs
	// live under it.
	prefix := strings.TrimRight(base.Path, "/") + "/browse/"
	rest, ok := strings.CutPrefix(u.Path, prefix)
	rest = strings.TrimSuffix(rest, "/")
	key := strings.ToUpper(rest)
	if !ok || !domain.ValidTicketKey(key) {
		return "", apperr.NewConfigError("--jira URL is not an issue link: pass the issue key or its /browse/<KEY> URL", nil)
	}
	return key, nil
}

// port is u's port with the scheme default made explicit, so https://h and https://h:443
// compare equal.
func port(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}
