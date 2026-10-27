// This file defines and validates the optional jira section (specs/jira/SPEC.md §7): where
// run --jira fetches a ticket from, and which environment variables hold the credentials.
// Validate checks the section whenever it is present; ValidateDependencies checks the
// variables only when DependencyOptions.Jira asks for it.
package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// jira.auth values: basic is Jira Cloud (account email + API token), bearer is a Data
// Center personal access token.
const (
	JiraAuthBasic  = "basic"
	JiraAuthBearer = "bearer"
)

// Defaults for keys absent from the jira section (SPEC.md §7).
const (
	defaultJiraTimeout    = 10 * time.Second
	defaultJiraMaxRetries = 1
	defaultJiraMaxBytes   = 32768
)

// Bounds on jira.max_retries and jira.max_bytes.
const (
	maxJiraRetries  = 3
	minJiraMaxBytes = 1024
	maxJiraMaxBytes = 262144
)

var (
	envNamePattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	jiraFieldIDPattern = regexp.MustCompile(`^customfield_[0-9]+$`)

	// jiraSecretKeys are keys that would put a credential into the file. They are
	// rejected whatever jira.auth is, in this order.
	jiraSecretKeys = []string{"token", "api_token", "password", "pat", "email"}

	// jiraReservedLabels are the section labels Render uses itself (SPEC.md §9), compared
	// case-insensitively.
	jiraReservedLabels = map[string]bool{
		"summary": true, "description": true, "parent": true, "type": true,
		"source": true, "operator notes": true,
	}

	// jiraLoopbackHosts may be reached over plain http, so tests can use httptest.
	jiraLoopbackHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
)

// JiraField is an extra text custom field rendered into the task under Label.
type JiraField struct {
	ID    string `yaml:"id"`
	Label string `yaml:"label"`
}

// jira.create.link values: the child points at its parent through the standard parent field,
// or through a custom field (Epic Link on Server and Data Center).
const (
	JiraLinkParent = "parent"
	JiraLinkField  = "field"
)

// JiraCreateConfig is jira.create (specs/story/SPEC.md §5).
type JiraCreateConfig struct {
	// IssueType is the children's type, e.g. Story.
	IssueType string `yaml:"issue_type"`
	// Link is parent (the default) or field.
	Link string `yaml:"link"`
	// Field is the custom field holding the parent with link: field.
	Field  string   `yaml:"field"`
	Labels []string `yaml:"labels"`
	// Components maps a service name to the Jira component its children get.
	Components map[string]string `yaml:"components"`
}

// UnmarshalYAML defaults link to parent.
func (c *JiraCreateConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type plain JiraCreateConfig
	var raw plain
	if err := unmarshal(&raw); err != nil {
		return err
	}
	*c = JiraCreateConfig(raw)
	if c.Link == "" {
		c.Link = JiraLinkParent
	}
	return nil
}

// maxJiraTransition bounds jira.write.transition, the length of a Jira status or transition name.
const maxJiraTransition = 255

// JiraWriteConfig is jira.write (specs/delivery/SPEC.md §10): what a run writes to the task's
// Jira issue once its push opened a merge request.
type JiraWriteConfig struct {
	// Comment posts the merge request as a comment on the issue.
	Comment bool `yaml:"comment"`
	// Transition is the name of a transition, or of the status it leads to, to move the issue
	// by; empty moves nothing.
	Transition string `yaml:"transition"`
}

// Enabled reports whether anything is to be written.
func (w *JiraWriteConfig) Enabled() bool {
	return w != nil && (w.Comment || strings.TrimSpace(w.Transition) != "")
}

// JiraWrites reports whether the configuration writes to Jira after a merge request.
func (c Config) JiraWrites() bool {
	return c.Jira != nil && c.Jira.Write.Enabled()
}

// JiraConfig describes the Jira instance run --jira reads one issue from. The credentials
// themselves are never part of it: EmailEnv and TokenEnv only name environment variables.
type JiraConfig struct {
	// BaseURL is stored without a trailing "/"; on Data Center it includes any context
	// path.
	BaseURL  string
	Auth     string
	EmailEnv string
	TokenEnv string
	// Timeout bounds one HTTP attempt.
	Timeout    time.Duration
	MaxRetries int
	// MaxBytes caps the rendered task.
	MaxBytes int
	Fields   []JiraField
	// Create is how story run creates the child issues of a story (specs/story/SPEC.md §12);
	// nil means it creates nothing in Jira.
	Create *JiraCreateConfig
	// Write is what a run writes to its task's issue after a merge request (specs/delivery/SPEC.md
	// §10); nil means it writes nothing.
	Write *JiraWriteConfig

	// literalSecrets lists the jiraSecretKeys the YAML set, without their values, which
	// validateJira rejects (as JevConfig.apiKeyLiteralSet does for decision.jev.api_key).
	literalSecrets []string
}

// UnmarshalYAML parses the timeout as a duration string, applies the defaults for absent
// keys - an explicit max_retries: 0 stays 0 - and records which literal secret keys were
// present without keeping their values.
func (j *JiraConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		BaseURL    string      `yaml:"base_url"`
		Auth       string      `yaml:"auth"`
		EmailEnv   string      `yaml:"email_env"`
		TokenEnv   string      `yaml:"token_env"`
		Timeout    string      `yaml:"timeout"`
		MaxRetries *int        `yaml:"max_retries"`
		MaxBytes   *int        `yaml:"max_bytes"`
		Fields     []JiraField `yaml:"fields"`

		Create *JiraCreateConfig `yaml:"create"`
		Write  *JiraWriteConfig  `yaml:"write"`

		// The jiraSecretKeys, declared only so a strict decode (Load) accepts them and
		// validateJira can reject them with its own message; their values are discarded.
		Token    any `yaml:"token"`
		APIToken any `yaml:"api_token"`
		Password any `yaml:"password"`
		PAT      any `yaml:"pat"`
		Email    any `yaml:"email"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	// A second pass over the keys alone: a secret key counts even when its value is empty
	// or null, and its value is never stored anywhere.
	var keys map[string]interface{}
	if err := unmarshal(&keys); err != nil {
		return err
	}

	timeout, err := parseDurationField("jira.timeout", raw.Timeout)
	if err != nil {
		return err
	}
	if raw.Timeout == "" {
		timeout = defaultJiraTimeout
	}
	maxRetries := defaultJiraMaxRetries
	if raw.MaxRetries != nil {
		maxRetries = *raw.MaxRetries
	}
	maxBytes := defaultJiraMaxBytes
	if raw.MaxBytes != nil {
		maxBytes = *raw.MaxBytes
	}

	var literal []string
	for _, k := range jiraSecretKeys {
		if _, ok := keys[k]; ok {
			literal = append(literal, k)
		}
	}

	*j = JiraConfig{
		BaseURL:  strings.TrimRight(raw.BaseURL, "/"),
		Auth:     raw.Auth,
		EmailEnv: raw.EmailEnv,
		TokenEnv: raw.TokenEnv,
		Timeout:  timeout, MaxRetries: maxRetries, MaxBytes: maxBytes,
		Fields:         raw.Fields,
		Create:         raw.Create,
		Write:          raw.Write,
		literalSecrets: literal,
	}
	return nil
}

// validateJira checks the jira section's shape (SPEC.md §7.1). It is part of Validate
// whenever the section is present, whether or not this run uses --jira.
func validateJira(j JiraConfig) []string {
	var problems []string

	problems = append(problems, jiraBaseURLProblems(j.BaseURL)...)

	switch j.Auth {
	case JiraAuthBasic, JiraAuthBearer:
	case "":
		problems = append(problems, fmt.Sprintf("jira.auth is required (%s or %s)", JiraAuthBasic, JiraAuthBearer))
	default:
		problems = append(problems, fmt.Sprintf("jira.auth must be %s or %s, got %q", JiraAuthBasic, JiraAuthBearer, j.Auth))
	}

	if j.TokenEnv == "" {
		problems = append(problems, "jira.token_env is required")
	} else if !envNamePattern.MatchString(j.TokenEnv) {
		problems = append(problems, fmt.Sprintf("jira.token_env %q is not a valid environment variable name", j.TokenEnv))
	}
	switch {
	case j.Auth == JiraAuthBearer && j.EmailEnv != "":
		problems = append(problems, "jira.email_env applies only to auth: basic; remove it for auth: bearer")
	case j.Auth == JiraAuthBasic && j.EmailEnv == "":
		problems = append(problems, "jira.email_env is required for auth: basic")
	case j.EmailEnv != "" && !envNamePattern.MatchString(j.EmailEnv):
		problems = append(problems, fmt.Sprintf("jira.email_env %q is not a valid environment variable name", j.EmailEnv))
	}

	for _, k := range j.literalSecrets {
		problems = append(problems, fmt.Sprintf("jira.%s must not be set in the configuration file; "+
			"name an environment variable via jira.token_env / jira.email_env instead", k))
	}

	if j.Timeout <= 0 {
		problems = append(problems, "jira.timeout must be a positive duration")
	}
	if j.MaxRetries < 0 || j.MaxRetries > maxJiraRetries {
		problems = append(problems, fmt.Sprintf("jira.max_retries must be between 0 and %d, got %d", maxJiraRetries, j.MaxRetries))
	}
	if j.MaxBytes < minJiraMaxBytes || j.MaxBytes > maxJiraMaxBytes {
		problems = append(problems, fmt.Sprintf("jira.max_bytes must be between %d and %d, got %d",
			minJiraMaxBytes, maxJiraMaxBytes, j.MaxBytes))
	}

	problems = append(problems, jiraFieldProblems(j.Fields)...)
	if j.Create != nil {
		problems = append(problems, jiraCreateProblems(*j.Create)...)
	}
	if j.Write != nil {
		problems = append(problems, jiraWriteProblems(*j.Write)...)
	}
	return problems
}

// jiraWriteProblems checks jira.write (specs/delivery/SPEC.md §10): a transition is a name on one
// line, as Jira writes it.
func jiraWriteProblems(w JiraWriteConfig) []string {
	t := w.Transition
	switch {
	case t != strings.TrimSpace(t):
		return []string{fmt.Sprintf("jira.write.transition %q must not start or end with whitespace", t)}
	case len(t) > maxJiraTransition:
		return []string{fmt.Sprintf("jira.write.transition is longer than %d bytes", maxJiraTransition)}
	case strings.ContainsFunc(t, func(r rune) bool { return r < ' ' || r == 0x7f }):
		return []string{"jira.write.transition must be a single line without control characters"}
	}
	return nil
}

// jiraCreateProblems checks jira.create (specs/story/SPEC.md §5). The block lives inside jira,
// so a jira.create without the rest of the section is also reported by validateJira's own
// required keys (base_url, auth, token_env).
func jiraCreateProblems(c JiraCreateConfig) []string {
	var problems []string
	if strings.TrimSpace(c.IssueType) == "" {
		problems = append(problems, "jira.create.issue_type is required")
	}
	switch c.Link {
	case JiraLinkParent:
		if c.Field != "" {
			problems = append(problems, "jira.create.field applies only to link: field; remove it for link: parent")
		}
	case JiraLinkField:
		switch {
		case c.Field == "":
			problems = append(problems, "jira.create.field is required with link: field")
		case !jiraFieldIDPattern.MatchString(c.Field):
			problems = append(problems, fmt.Sprintf("jira.create.field %q must be a custom field id (customfield_<N>)", c.Field))
		}
	default:
		problems = append(problems, fmt.Sprintf("jira.create.link must be %s or %s, got %q", JiraLinkParent, JiraLinkField, c.Link))
	}
	for i, l := range c.Labels {
		if strings.TrimSpace(l) == "" || strings.ContainsAny(l, " \t\r\n") {
			problems = append(problems, fmt.Sprintf("jira.create.labels[%d] %q must be a non-empty label without whitespace", i, l))
		}
	}
	for name, comp := range c.Components {
		if strings.TrimSpace(comp) == "" {
			problems = append(problems, fmt.Sprintf("jira.create.components.%s must name a Jira component", name))
		}
	}
	return problems
}

// jiraBaseURLProblems accepts an absolute https URL with no query, fragment or userinfo;
// plain http only for a loopback host, so credentials never cross a network in clear text.
func jiraBaseURLProblems(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{"jira.base_url is required"}
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return []string{fmt.Sprintf("jira.base_url %q must be an absolute https URL", raw)}
	}
	var problems []string
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && jiraLoopbackHosts[strings.ToLower(u.Hostname())]:
	default:
		problems = append(problems, fmt.Sprintf(
			"jira.base_url %q must use https (http is allowed only for localhost, 127.0.0.1 and ::1)", raw))
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		problems = append(problems, fmt.Sprintf("jira.base_url %q must not have a query or fragment", raw))
	}
	if u.User != nil {
		problems = append(problems, "jira.base_url must not contain credentials; "+
			"name environment variables via jira.token_env / jira.email_env instead")
	}
	return problems
}

func jiraFieldProblems(fields []JiraField) []string {
	var problems []string
	ids := make(map[string]bool, len(fields))
	labels := make(map[string]bool, len(fields))
	for i, f := range fields {
		if !jiraFieldIDPattern.MatchString(f.ID) {
			problems = append(problems, fmt.Sprintf("jira.fields[%d].id %q must be a custom field id (customfield_<N>)", i, f.ID))
		} else if ids[f.ID] {
			problems = append(problems, fmt.Sprintf("jira.fields[%d].id %q is listed more than once", i, f.ID))
		}
		ids[f.ID] = true

		label := strings.ToLower(strings.TrimSpace(f.Label))
		switch {
		case label == "":
			problems = append(problems, fmt.Sprintf("jira.fields[%d].label is required", i))
		case strings.ContainsAny(f.Label, "\r\n"):
			problems = append(problems, fmt.Sprintf("jira.fields[%d].label must be a single line", i))
		case jiraReservedLabels[label]:
			problems = append(problems, fmt.Sprintf("jira.fields[%d].label %q is reserved", i, f.Label))
		case labels[label]:
			problems = append(problems, fmt.Sprintf("jira.fields[%d].label %q is listed more than once", i, f.Label))
		}
		labels[label] = true
	}
	return problems
}

// CredentialProblems reports which of the variables the credentials come from are unset or
// blank, naming the variables and never their values. A resume of a run with options.jira_write
// asks it before the write-back, which it does not check in the fail-fast chain.
func (j *JiraConfig) CredentialProblems() []string {
	return jiraCredentialProblems(j, nil)
}

// jiraCredentialProblems reports what would stop run --jira from authenticating: a missing
// section, or a named variable that is unset or blank. It names the variables, never their
// values.
func jiraCredentialProblems(j *JiraConfig, getenv func(string) string) []string {
	if j == nil {
		return []string{"--jira needs a jira section in the configuration (see orchestrator.example.yaml)"}
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	var problems []string
	for _, v := range []struct{ key, name string }{
		{"jira.email_env", j.EmailEnv},
		{"jira.token_env", j.TokenEnv},
	} {
		if v.name == "" {
			continue
		}
		if strings.TrimSpace(getenv(v.name)) == "" {
			problems = append(problems, fmt.Sprintf("%s names %s, which is not set", v.key, v.name))
		}
	}
	return problems
}
