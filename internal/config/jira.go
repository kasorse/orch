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
