// This file defines and validates the optional listener and gitlab sections
// (specs/jira-listener/SPEC.md §2): which Jira tickets become runs, where their services are
// cloned from, and the variable holding the GitLab token. Validate checks the sections whenever
// they are present; ValidateDependencies checks the variables only with DependencyOptions.Listener.
package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Defaults for keys absent from the listener and gitlab sections (SPEC.md §2).
const (
	defaultListenerPollInterval  = time.Minute
	defaultListenerServicesDir   = "."
	defaultListenerMaxAttempts   = 3
	defaultListenerCloneTimeout  = 10 * time.Minute
	defaultListenerLabel         = "ai-run"
	defaultListenerServicePrefix = "service:"
	defaultListenerProjectPrefix = "gitlab:"
	defaultListenerRestartLabel  = "ai-restart"
	defaultGitLabUsername        = "oauth2"
)

// Bounds on the listener's durations and counts.
const (
	minListenerPollInterval = 30 * time.Second
	maxListenerPollInterval = time.Hour
	minListenerMaxAttempts  = 1
	maxListenerMaxAttempts  = 10
	minListenerCloneTimeout = time.Minute
	maxListenerCloneTimeout = time.Hour
)

// gitlabPathPattern is a project path or a group prefix of one: slash-separated segments of
// SPEC.md §5's charset. The "." and ".." segments are refused separately.
var gitlabPathPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$`)

// ListenerConfig is the listener section.
type ListenerConfig struct {
	PollInterval time.Duration
	// ServicesDir is relative to the working directory.
	ServicesDir string
	// DefaultConfig is the --config of a service without its own orchestrator.yaml; empty means
	// none. Its existence is checked where it is used, not here.
	DefaultConfig string
	MaxAttempts   int
	CloneTimeout  time.Duration
	Query         ListenerQuery
	Labels        ListenerLabels
	Transitions   ListenerTransitions
}

// ListenerQuery is listener.query: what the search and the Go re-check accept.
type ListenerQuery struct {
	Projects  []string `yaml:"projects"`
	Label     string   `yaml:"label"`
	Statuses  []string `yaml:"statuses"`
	Reporters []string `yaml:"reporters"`
}

// ListenerLabels is listener.labels: the prefixes of the labels naming the service and the project,
// and the label that makes a ticket start a new run instead of resuming its earlier one.
type ListenerLabels struct {
	ServicePrefix string `yaml:"service_prefix"`
	ProjectPrefix string `yaml:"project_prefix"`
	Restart       string `yaml:"restart"`
}

// ListenerTransitions is listener.transitions: each a transition's name or its target status, as
// jira.write.transition.
type ListenerTransitions struct {
	Start   string `yaml:"start"`
	Blocked string `yaml:"blocked"`
	Failed  string `yaml:"failed"`
	Done    string `yaml:"done"`
	Retry   string `yaml:"retry"`
}

// UnmarshalYAML applies the defaults for absent keys; an explicit value, even an empty or zero
// one, is kept so Validate can refuse it.
func (l *ListenerConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		PollInterval  string  `yaml:"poll_interval"`
		ServicesDir   *string `yaml:"services_dir"`
		DefaultConfig string  `yaml:"default_config"`
		MaxAttempts   *int    `yaml:"max_attempts"`
		CloneTimeout  string  `yaml:"clone_timeout"`
		Query         struct {
			Projects  []string `yaml:"projects"`
			Label     *string  `yaml:"label"`
			Statuses  []string `yaml:"statuses"`
			Reporters []string `yaml:"reporters"`
		} `yaml:"query"`
		Labels struct {
			ServicePrefix *string `yaml:"service_prefix"`
			ProjectPrefix *string `yaml:"project_prefix"`
			Restart       *string `yaml:"restart"`
		} `yaml:"labels"`
		Transitions ListenerTransitions `yaml:"transitions"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	return l.finish(raw.PollInterval, raw.ServicesDir, raw.DefaultConfig, raw.MaxAttempts, raw.CloneTimeout,
		raw.Query.Projects, raw.Query.Label, raw.Query.Statuses, raw.Query.Reporters,
		raw.Labels.ServicePrefix, raw.Labels.ProjectPrefix, raw.Labels.Restart, raw.Transitions)
}

// finish builds the section from the decoded keys, defaulting the absent ones.
func (l *ListenerConfig) finish(poll string, dir *string, defaultConfig string, attempts *int, clone string,
	projects []string, label *string, statuses, reporters []string,
	servicePrefix, projectPrefix, restart *string, transitions ListenerTransitions) error {
	pollInterval, err := parseDurationField("listener.poll_interval", poll)
	if err != nil {
		return err
	}
	if poll == "" {
		pollInterval = defaultListenerPollInterval
	}
	cloneTimeout, err := parseDurationField("listener.clone_timeout", clone)
	if err != nil {
		return err
	}
	if clone == "" {
		cloneTimeout = defaultListenerCloneTimeout
	}
	or := func(p *string, def string) string {
		if p == nil {
			return def
		}
		return *p
	}
	maxAttempts := defaultListenerMaxAttempts
	if attempts != nil {
		maxAttempts = *attempts
	}
	*l = ListenerConfig{
		PollInterval:  pollInterval,
		ServicesDir:   or(dir, defaultListenerServicesDir),
		DefaultConfig: defaultConfig,
		MaxAttempts:   maxAttempts,
		CloneTimeout:  cloneTimeout,
		Query: ListenerQuery{
			Projects: projects, Label: or(label, defaultListenerLabel),
			Statuses: statuses, Reporters: reporters,
		},
		Labels: ListenerLabels{
			ServicePrefix: or(servicePrefix, defaultListenerServicePrefix),
			ProjectPrefix: or(projectPrefix, defaultListenerProjectPrefix),
			Restart:       or(restart, defaultListenerRestartLabel),
		},
		Transitions: transitions,
	}
	return nil
}

// validateListener checks the listener section's shape (SPEC.md §2). It is part of Validate
// whenever the section is present; jira and gitlab are the caller's to require.
func validateListener(l ListenerConfig) []string {
	var problems []string

	if l.PollInterval < minListenerPollInterval || l.PollInterval > maxListenerPollInterval {
		problems = append(problems, fmt.Sprintf("listener.poll_interval must be between %s and %s, got %s",
			minListenerPollInterval, maxListenerPollInterval, l.PollInterval))
	}
	if l.CloneTimeout < minListenerCloneTimeout || l.CloneTimeout > maxListenerCloneTimeout {
		problems = append(problems, fmt.Sprintf("listener.clone_timeout must be between %s and %s, got %s",
			minListenerCloneTimeout, maxListenerCloneTimeout, l.CloneTimeout))
	}
	if l.MaxAttempts < minListenerMaxAttempts || l.MaxAttempts > maxListenerMaxAttempts {
		problems = append(problems, fmt.Sprintf("listener.max_attempts must be between %d and %d, got %d",
			minListenerMaxAttempts, maxListenerMaxAttempts, l.MaxAttempts))
	}
	if strings.TrimSpace(l.ServicesDir) == "" || hasControl(l.ServicesDir) {
		problems = append(problems, "listener.services_dir must be a path on one line")
	}
	if l.DefaultConfig != "" && (strings.TrimSpace(l.DefaultConfig) != l.DefaultConfig || hasControl(l.DefaultConfig)) {
		problems = append(problems, "listener.default_config must be a relative or absolute path on one line")
	}

	problems = append(problems, jqlListProblems("listener.query.projects", l.Query.Projects)...)
	problems = append(problems, jqlListProblems("listener.query.statuses", l.Query.Statuses)...)
	problems = append(problems, jqlListProblems("listener.query.reporters", l.Query.Reporters)...)
	if p := jqlValueProblem(l.Query.Label); p != "" {
		problems = append(problems, fmt.Sprintf("listener.query.label %q %s", l.Query.Label, p))
	} else if strings.ContainsAny(l.Query.Label, " \t") {
		problems = append(problems, fmt.Sprintf("listener.query.label %q must be a label without whitespace", l.Query.Label))
	}

	for _, p := range []struct{ key, value string }{
		{"listener.labels.service_prefix", l.Labels.ServicePrefix},
		{"listener.labels.project_prefix", l.Labels.ProjectPrefix},
		{"listener.labels.restart", l.Labels.Restart},
	} {
		if p.value == "" || strings.ContainsFunc(p.value, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) {
			problems = append(problems, fmt.Sprintf("%s %q must be a non-empty label or label prefix without whitespace", p.key, p.value))
		}
	}
	if l.Labels.ServicePrefix == l.Labels.ProjectPrefix && l.Labels.ServicePrefix != "" {
		problems = append(problems, "listener.labels.service_prefix and listener.labels.project_prefix must differ")
	}

	for _, t := range []struct{ key, value string }{
		{"start", l.Transitions.Start}, {"blocked", l.Transitions.Blocked}, {"failed", l.Transitions.Failed},
		{"done", l.Transitions.Done}, {"retry", l.Transitions.Retry},
	} {
		key := "listener.transitions." + t.key
		switch {
		case t.value == "":
			problems = append(problems, key+" is required")
		case t.value != strings.TrimSpace(t.value):
			problems = append(problems, fmt.Sprintf("%s %q must not start or end with whitespace", key, t.value))
		case len(t.value) > maxJiraTransition:
			problems = append(problems, fmt.Sprintf("%s is longer than %d bytes", key, maxJiraTransition))
		case hasControl(t.value):
			problems = append(problems, key+" must be a single line without control characters")
		}
	}
	return problems
}

// hasControl reports whether s holds a control character, a newline included.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < ' ' || r == 0x7f })
}

// jqlListProblems requires a non-empty list of values the listener can put in a JQL string.
func jqlListProblems(key string, values []string) []string {
	if len(values) == 0 {
		return []string{key + " is required (at least one)"}
	}
	var problems []string
	for i, v := range values {
		if p := jqlValueProblem(v); p != "" {
			problems = append(problems, fmt.Sprintf("%s[%d] %q %s", key, i, v, p))
		}
	}
	return problems
}

// jqlValueProblem is why s cannot be a JQL value, or "": one printable, non-empty line without
// a double quote or a backslash, so quoting it is total.
func jqlValueProblem(s string) string {
	if strings.TrimSpace(s) == "" {
		return "must not be empty"
	}
	if strings.ContainsFunc(s, func(r rune) bool { return r == '"' || r == '\\' || !unicode.IsPrint(r) }) {
		return `must be one printable line without " or \`
	}
	return ""
}

// GitLabConfig is the gitlab section: where services are cloned from and the token variable.
// The token itself is never part of it.
type GitLabConfig struct {
	// BaseURL is stored without a trailing "/"; it may carry a context path.
	BaseURL  string
	TokenEnv string
	Username string
	// AllowedGroups are project-path prefixes: a clone is allowed only from under one.
	AllowedGroups []string

	// literalSecrets lists the gitlabSecretKeys the YAML set, without their values, which
	// validateGitLab rejects.
	literalSecrets []string
}

// gitlabSecretKeys are keys that would put a credential into the file.
var gitlabSecretKeys = []string{"token", "api_token", "password", "pat"}

// UnmarshalYAML defaults the username and records which literal secret keys were present
// without keeping their values.
func (g *GitLabConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		BaseURL       string   `yaml:"base_url"`
		TokenEnv      string   `yaml:"token_env"`
		Username      *string  `yaml:"username"`
		AllowedGroups []string `yaml:"allowed_groups"`

		// The gitlabSecretKeys, declared only so a strict decode accepts them and validateGitLab
		// can reject them with its own message; their values are discarded.
		Token    any `yaml:"token"`
		APIToken any `yaml:"api_token"`
		Password any `yaml:"password"`
		PAT      any `yaml:"pat"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	var keys map[string]interface{}
	if err := unmarshal(&keys); err != nil {
		return err
	}
	var literal []string
	for _, k := range gitlabSecretKeys {
		if _, ok := keys[k]; ok {
			literal = append(literal, k)
		}
	}
	username := defaultGitLabUsername
	if raw.Username != nil {
		username = *raw.Username
	}
	*g = GitLabConfig{
		BaseURL:        strings.TrimRight(raw.BaseURL, "/"),
		TokenEnv:       raw.TokenEnv,
		Username:       username,
		AllowedGroups:  raw.AllowedGroups,
		literalSecrets: literal,
	}
	return nil
}

// validateGitLab checks the gitlab section's shape (SPEC.md §2).
func validateGitLab(g GitLabConfig) []string {
	problems := gitlabBaseURLProblems(g.BaseURL)

	if g.TokenEnv == "" {
		problems = append(problems, "gitlab.token_env is required")
	} else if !envNamePattern.MatchString(g.TokenEnv) {
		problems = append(problems, fmt.Sprintf("gitlab.token_env %q is not a valid environment variable name", g.TokenEnv))
	}
	for _, k := range g.literalSecrets {
		problems = append(problems, fmt.Sprintf("gitlab.%s must not be set in the configuration file; "+
			"name an environment variable via gitlab.token_env instead", k))
	}
	if g.Username == "" || strings.ContainsFunc(g.Username, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) {
		problems = append(problems, fmt.Sprintf("gitlab.username %q must be a non-empty name without whitespace", g.Username))
	}

	if len(g.AllowedGroups) == 0 {
		problems = append(problems, "gitlab.allowed_groups is required (at least one group)")
	}
	for i, grp := range g.AllowedGroups {
		if !gitlabPathPattern.MatchString(grp) || hasDotSegment(grp) {
			problems = append(problems, fmt.Sprintf(
				"gitlab.allowed_groups[%d] %q must be a project-path prefix of letters, digits, _ . - separated by / (no . or .. segment)",
				i, grp))
		}
	}
	return problems
}

// hasDotSegment reports whether a slash-separated path has a "." or ".." segment.
func hasDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// gitlabBaseURLProblems accepts an absolute https URL with no userinfo, query or fragment; plain
// http only for a loopback host.
func gitlabBaseURLProblems(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{"gitlab.base_url is required"}
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return []string{fmt.Sprintf("gitlab.base_url %q must be an absolute https URL", raw)}
	}
	var problems []string
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && jiraLoopbackHosts[strings.ToLower(u.Hostname())]:
	default:
		problems = append(problems, fmt.Sprintf(
			"gitlab.base_url %q must use https (http is allowed only for localhost, 127.0.0.1 and ::1)", raw))
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		problems = append(problems, fmt.Sprintf("gitlab.base_url %q must not have a query or fragment", raw))
	}
	if u.User != nil {
		problems = append(problems, "gitlab.base_url must not contain credentials; name an environment variable via gitlab.token_env instead")
	}
	return problems
}

// listenerCredentialProblems reports what would stop the listener from authenticating to GitLab:
// a missing section, or a token variable that is unset or blank. It names the variable, never its
// value.
func listenerCredentialProblems(g *GitLabConfig, getenv func(string) string) []string {
	if g == nil {
		return []string{"the listener needs a gitlab section in the configuration"}
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if g.TokenEnv != "" && strings.TrimSpace(getenv(g.TokenEnv)) == "" {
		return []string{fmt.Sprintf("gitlab.token_env names %s, which is not set", g.TokenEnv)}
	}
	return nil
}
