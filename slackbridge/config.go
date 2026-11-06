package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is slackbridge.yaml. No secret lives in it: the Slack tokens and the GitLab
// tokens are read from the environment variables it names.
type Config struct {
	Slack struct {
		BotTokenEnv string `yaml:"bot_token_env"` // xoxb-..., default SLACK_BOT_TOKEN
		AppTokenEnv string `yaml:"app_token_env"` // xapp-... (Socket Mode), default SLACK_APP_TOKEN
	} `yaml:"slack"`
	// AllowedUsers are the Slack user IDs (U...) that may start a run. Required: everyone
	// else is refused.
	AllowedUsers []string `yaml:"allowed_users"`
	// DefaultProject is used when the message names no project and its channel maps to none.
	DefaultProject string `yaml:"default_project"`
	// Channels maps a Slack channel ID to a project.
	Channels map[string]string `yaml:"channels"`
	// Jira lets the bridge read a ticket to choose its project (jira_match), and expands a
	// bare number ("15230") to default_project_key ("PROJ-15230").
	Jira     *JiraConfig         `yaml:"jira"`
	Projects map[string]*Project `yaml:"projects"`
}

// Project is one target repository and how a task runs against it.
type Project struct {
	Runner string `yaml:"runner"` // local | gitlab
	// JiraMatch: a ticket whose component, label or [summary tag] equals one of these
	// (case-insensitive) belongs to this project, e.g. [proj-web, web].
	JiraMatch []string `yaml:"jira_match"`
	// APISpec: refresh this OpenAPI document before each run and add the contract of the
	// endpoints the task or ticket names to the task (local runner only).
	APISpec *APISpec `yaml:"api_spec"`
	// Approve plans first (--mode plan) and asks in Slack before running (T6). local only.
	Approve bool    `yaml:"approve"`
	Local   *Local  `yaml:"local"`
	GitLab  *GitLab `yaml:"gitlab"`
}

// Local runs the orchestrator on this machine, in Dir.
type Local struct {
	Dir          string   `yaml:"dir"`
	Orchestrator string   `yaml:"orchestrator"` // the binary, default "orchestrator"
	Config       string   `yaml:"config"`       // passed as --config when set
	Args         []string `yaml:"args"`         // extra global flags, e.g. [--claude-auth, api]
	// EnvCommand prints `export NAME=value` lines that are evaluated before each run,
	// e.g. an AI gateway key: `my-gateway-login --export`.
	EnvCommand string `yaml:"env_command"`
	Timeout    string `yaml:"timeout"` // Go duration, default 60m
}

// GitLab triggers a pipeline that runs the orchestrator in CI (specs/slack-gitlab T2).
type GitLab struct {
	BaseURL         string `yaml:"base_url"`   // default https://gitlab.com
	ProjectID       string `yaml:"project_id"` // numeric id or url-encoded path
	Ref             string `yaml:"ref"`
	TriggerTokenEnv string `yaml:"trigger_token_env"`
	APITokenEnv     string `yaml:"api_token_env"` // read_api, for polling the pipeline
	Timeout         string `yaml:"timeout"`
}

var userIDRE = regexp.MustCompile(`^[UW][A-Z0-9]+$`)
var projectNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Slack.BotTokenEnv == "" {
		c.Slack.BotTokenEnv = "SLACK_BOT_TOKEN"
	}
	if c.Slack.AppTokenEnv == "" {
		c.Slack.AppTokenEnv = "SLACK_APP_TOKEN"
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// JiraConfig is how the bridge reads tickets. The token is read from the environment.
type JiraConfig struct {
	BaseURL           string `yaml:"base_url"`
	EmailEnv          string `yaml:"email_env"`
	TokenEnv          string `yaml:"token_env"`
	DefaultProjectKey string `yaml:"default_project_key"` // e.g. PROJ: "15230" -> PROJ-15230
	// EnvCommand prints `export NAME=value` lines (the token from Keychain), like local.env_command.
	EnvCommand string `yaml:"env_command"`
}

var projectKeyRE = regexp.MustCompile(`^[A-Z][A-Z0-9]+$`)

func (c *Config) Validate() error {
	var p []string
	if j := c.Jira; j != nil {
		if j.BaseURL == "" || j.EmailEnv == "" || j.TokenEnv == "" {
			p = append(p, "jira needs base_url, email_env and token_env")
		}
		if j.DefaultProjectKey != "" && !projectKeyRE.MatchString(j.DefaultProjectKey) {
			p = append(p, "jira.default_project_key must be a project key such as PROJ")
		}
	}
	if len(c.AllowedUsers) == 0 {
		p = append(p, "allowed_users is required: list the Slack user IDs that may start a run")
	}
	for _, u := range c.AllowedUsers {
		if !userIDRE.MatchString(u) {
			p = append(p, fmt.Sprintf("allowed_users: %q is not a Slack user ID (U...)", u))
		}
	}
	if len(c.Projects) == 0 {
		p = append(p, "projects: at least one project is required")
	}
	for name, pr := range c.Projects {
		if !projectNameRE.MatchString(name) {
			p = append(p, fmt.Sprintf("projects.%s: the name must be lowercase letters, digits, . _ -", name))
		}
		if pr == nil {
			p = append(p, fmt.Sprintf("projects.%s: empty", name))
			continue
		}
		if a := pr.APISpec; a != nil && (a.URL == "" || a.Path == "") {
			p = append(p, fmt.Sprintf("projects.%s.api_spec needs url and path", name))
		}
		switch pr.Runner {
		case "local":
			if pr.Local == nil || pr.Local.Dir == "" {
				p = append(p, fmt.Sprintf("projects.%s.local.dir is required", name))
			}
		case "gitlab":
			if pr.Approve {
				p = append(p, fmt.Sprintf("projects.%s.approve is only supported with runner: local", name))
			}
			g := pr.GitLab
			if g == nil || g.ProjectID == "" || g.Ref == "" || g.TriggerTokenEnv == "" {
				p = append(p, fmt.Sprintf("projects.%s.gitlab needs project_id, ref and trigger_token_env", name))
			}
		default:
			p = append(p, fmt.Sprintf("projects.%s.runner must be local or gitlab, got %q", name, pr.Runner))
		}
	}
	if c.DefaultProject != "" && c.Projects[c.DefaultProject] == nil {
		p = append(p, fmt.Sprintf("default_project %q is not in projects", c.DefaultProject))
	}
	for ch, name := range c.Channels {
		if c.Projects[name] == nil {
			p = append(p, fmt.Sprintf("channels.%s: project %q is not in projects", ch, name))
		}
	}
	if len(p) > 0 {
		sort.Strings(p)
		return fmt.Errorf("invalid configuration:\n  %s", strings.Join(p, "\n  "))
	}
	return nil
}

func (c *Config) Allowed(user string) bool {
	for _, u := range c.AllowedUsers {
		if u == user {
			return true
		}
	}
	return false
}
