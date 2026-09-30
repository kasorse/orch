package config

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// validJira returns a valid Cloud-style jira section, as UnmarshalYAML would build it.
func validJira() JiraConfig {
	return JiraConfig{
		BaseURL:    "https://example.atlassian.net",
		Auth:       JiraAuthBasic,
		EmailEnv:   "JIRA_EMAIL",
		TokenEnv:   "JIRA_API_TOKEN",
		Timeout:    10 * time.Second,
		MaxRetries: 1,
		MaxBytes:   32768,
		Fields:     []JiraField{{ID: "customfield_10035", Label: "Acceptance criteria"}},
	}
}

func unmarshalJira(t *testing.T, src string) JiraConfig {
	t.Helper()
	var j JiraConfig
	if err := yaml.Unmarshal([]byte(src), &j); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return j
}

func TestJiraConfig_UnmarshalYAML(t *testing.T) {
	j := unmarshalJira(t, `
base_url: https://jira.corp/jira/
auth: bearer
token_env: JIRA_PAT
timeout: 3s
max_retries: 0
max_bytes: 2048
fields:
  - id: customfield_10035
    label: Acceptance criteria
`)
	want := JiraConfig{
		BaseURL: "https://jira.corp/jira", Auth: JiraAuthBearer, TokenEnv: "JIRA_PAT",
		Timeout: 3 * time.Second, MaxRetries: 0, MaxBytes: 2048,
		Fields: []JiraField{{ID: "customfield_10035", Label: "Acceptance criteria"}},
	}
	if j.BaseURL != want.BaseURL || j.Auth != want.Auth || j.TokenEnv != want.TokenEnv || j.EmailEnv != "" ||
		j.Timeout != want.Timeout || j.MaxRetries != want.MaxRetries || j.MaxBytes != want.MaxBytes ||
		len(j.Fields) != 1 || j.Fields[0] != want.Fields[0] {
		t.Errorf("got %+v, want %+v", j, want)
	}
	if len(j.literalSecrets) != 0 {
		t.Errorf("literalSecrets = %v, want none", j.literalSecrets)
	}
	if problems := validateJira(j); len(problems) != 0 {
		t.Errorf("expected valid, got %v", problems)
	}
}

func TestJiraConfig_UnmarshalYAML_Defaults(t *testing.T) {
	j := unmarshalJira(t, "base_url: https://example.atlassian.net\nauth: basic\nemail_env: E\ntoken_env: T\n")
	if j.Timeout != 10*time.Second || j.MaxRetries != 1 || j.MaxBytes != 32768 {
		t.Errorf("defaults = timeout %s, max_retries %d, max_bytes %d; want 10s, 1, 32768", j.Timeout, j.MaxRetries, j.MaxBytes)
	}
}

func TestJiraConfig_UnmarshalYAML_InvalidTimeout(t *testing.T) {
	var j JiraConfig
	err := yaml.Unmarshal([]byte("timeout: soon\n"), &j)
	if err == nil || !strings.Contains(err.Error(), "jira.timeout") {
		t.Fatalf("expected a jira.timeout parse error, got %v", err)
	}
}

// Every secret key counts, even with an empty or null value, and its value is never kept.
func TestJiraConfig_UnmarshalYAML_LiteralSecrets(t *testing.T) {
	j := unmarshalJira(t, "token: abc\napi_token: sk-1\npassword: ''\npat:\nemail: me@example.com\n")
	if got := strings.Join(j.literalSecrets, ","); got != "token,api_token,password,pat,email" {
		t.Errorf("literalSecrets = %q", got)
	}
	for _, secret := range []string{"abc", "sk-1", "me@example.com"} {
		if strings.Contains(strings.Join(validateJira(j), "\n"), secret) {
			t.Errorf("secret %q appears in a validation message", secret)
		}
	}
}

func TestValidateJira(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*JiraConfig)
		want   string // substring of the one expected problem; "" means valid
	}{
		{"valid", func(*JiraConfig) {}, ""},
		{"https with context path", func(j *JiraConfig) { j.BaseURL = "https://jira.corp/jira" }, ""},
		{"http loopback ip", func(j *JiraConfig) { j.BaseURL = "http://127.0.0.1:8080" }, ""},
		{"http localhost", func(j *JiraConfig) { j.BaseURL = "http://localhost:8080" }, ""},
		{"http ipv6 loopback", func(j *JiraConfig) { j.BaseURL = "http://[::1]:8080" }, ""},
		{"base_url missing", func(j *JiraConfig) { j.BaseURL = "" }, "jira.base_url is required"},
		{"http real host", func(j *JiraConfig) { j.BaseURL = "http://jira.corp" }, "must use https"},
		{"ftp", func(j *JiraConfig) { j.BaseURL = "ftp://jira.corp" }, "must use https"},
		{"relative", func(j *JiraConfig) { j.BaseURL = "jira.corp/jira" }, "must be an absolute https URL"},
		{"no host", func(j *JiraConfig) { j.BaseURL = "https:///jira" }, "must be an absolute https URL"},
		{"query", func(j *JiraConfig) { j.BaseURL = "https://jira.corp?x=1" }, "must not have a query or fragment"},
		{"fragment", func(j *JiraConfig) { j.BaseURL = "https://jira.corp#top" }, "must not have a query or fragment"},
		{"userinfo", func(j *JiraConfig) { j.BaseURL = "https://me:secret@jira.corp" }, "must not contain credentials"},
		{"auth missing", func(j *JiraConfig) { j.Auth = "" }, "jira.auth is required"},
		{"auth unknown", func(j *JiraConfig) { j.Auth = "oauth" }, `jira.auth must be basic or bearer, got "oauth"`},
		{"token_env missing", func(j *JiraConfig) { j.TokenEnv = "" }, "jira.token_env is required"},
		{"token_env invalid", func(j *JiraConfig) { j.TokenEnv = "JIRA-TOKEN" }, `jira.token_env "JIRA-TOKEN" is not a valid`},
		{"token_env leading digit", func(j *JiraConfig) { j.TokenEnv = "1TOKEN" }, "jira.token_env"},
		{"email_env missing for basic", func(j *JiraConfig) { j.EmailEnv = "" }, "jira.email_env is required for auth: basic"},
		{"email_env invalid", func(j *JiraConfig) { j.EmailEnv = "JIRA EMAIL" }, `jira.email_env "JIRA EMAIL" is not a valid`},
		{"bearer without email_env", func(j *JiraConfig) { j.Auth = JiraAuthBearer; j.EmailEnv = "" }, ""},
		{"bearer with email_env", func(j *JiraConfig) { j.Auth = JiraAuthBearer }, "jira.email_env applies only to auth: basic"},
		{"literal token", func(j *JiraConfig) { j.literalSecrets = []string{"token"} },
			"jira.token must not be set in the configuration file; name an environment variable via jira.token_env / jira.email_env instead"},
		{"literal pat", func(j *JiraConfig) { j.literalSecrets = []string{"pat"} }, "jira.pat must not be set"},
		{"timeout zero", func(j *JiraConfig) { j.Timeout = 0 }, "jira.timeout must be a positive duration"},
		{"max_retries zero", func(j *JiraConfig) { j.MaxRetries = 0 }, ""},
		{"max_retries three", func(j *JiraConfig) { j.MaxRetries = 3 }, ""},
		{"max_retries negative", func(j *JiraConfig) { j.MaxRetries = -1 }, "jira.max_retries must be between 0 and 3, got -1"},
		{"max_retries four", func(j *JiraConfig) { j.MaxRetries = 4 }, "jira.max_retries must be between 0 and 3, got 4"},
		{"max_bytes min", func(j *JiraConfig) { j.MaxBytes = 1024 }, ""},
		{"max_bytes max", func(j *JiraConfig) { j.MaxBytes = 262144 }, ""},
		{"max_bytes too small", func(j *JiraConfig) { j.MaxBytes = 1023 }, "jira.max_bytes must be between 1024 and 262144, got 1023"},
		{"max_bytes too large", func(j *JiraConfig) { j.MaxBytes = 262145 }, "jira.max_bytes must be between 1024 and 262144, got 262145"},
		{"field system id", func(j *JiraConfig) { j.Fields[0].ID = "description" }, `jira.fields[0].id "description" must be a custom field id`},
		{"field id suffix", func(j *JiraConfig) { j.Fields[0].ID = "customfield_1x" }, "jira.fields[0].id"},
		{"field label empty", func(j *JiraConfig) { j.Fields[0].Label = "  " }, "jira.fields[0].label is required"},
		{"field label multiline", func(j *JiraConfig) { j.Fields[0].Label = "Acceptance\ncriteria" }, "jira.fields[0].label must be a single line"},
		{"field label reserved", func(j *JiraConfig) { j.Fields[0].Label = "Description" }, `jira.fields[0].label "Description" is reserved`},
		{"field label reserved case", func(j *JiraConfig) { j.Fields[0].Label = "OPERATOR NOTES" }, "is reserved"},
		{"field label source", func(j *JiraConfig) { j.Fields[0].Label = "source" }, "is reserved"},
		{"field id duplicate", func(j *JiraConfig) {
			j.Fields = append(j.Fields, JiraField{ID: "customfield_10035", Label: "Other"})
		}, `jira.fields[1].id "customfield_10035" is listed more than once`},
		{"field label duplicate", func(j *JiraConfig) {
			j.Fields = append(j.Fields, JiraField{ID: "customfield_1", Label: "acceptance criteria"})
		}, `jira.fields[1].label "acceptance criteria" is listed more than once`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := validJira()
			j.Fields = append([]JiraField(nil), j.Fields...)
			tt.mutate(&j)
			problems := validateJira(j)
			if tt.want == "" {
				if len(problems) != 0 {
					t.Fatalf("expected valid, got %v", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tt.want) {
				t.Fatalf("problems = %q, want exactly one containing %q", problems, tt.want)
			}
		})
	}
}

// Validate aggregates every jira problem with the rest of the configuration's.
func TestValidate_JiraAggregatesProblems(t *testing.T) {
	cfg := validConfig()
	cfg.Limits.MaxSteps = 0
	cfg.Jira = &JiraConfig{
		BaseURL: "http://jira.corp", Auth: "oauth", Timeout: 0, MaxRetries: 9, MaxBytes: 10,
		literalSecrets: []string{"token"},
	}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		"limits.max_steps must be positive",
		"jira.base_url",
		"jira.auth must be basic or bearer",
		"jira.token_env is required",
		"jira.token must not be set",
		"jira.timeout must be a positive duration",
		"jira.max_retries must be between 0 and 3",
		"jira.max_bytes must be between 1024 and 262144",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestValidate_JiraSectionValid(t *testing.T) {
	cfg := validConfig()
	j := validJira()
	cfg.Jira = &j
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

// A literal token in the file is rejected through the real Load -> Validate path, and its
// value never reaches the message.
func TestLoad_JiraLiteralTokenRejected(t *testing.T) {
	path := writeTempConfig(t, "version: 2\njira:\n  base_url: https://example.atlassian.net\n"+
		"  auth: basic\n  email_env: E\n  token_env: T\n  token: abc\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	err = Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "jira.token must not be set in the configuration file") {
		t.Fatalf("expected literal-token error, got %v", err)
	}
	if strings.Contains(err.Error(), "abc") {
		t.Errorf("the token's value appears in the error:\n%v", err)
	}
}

// An absent section is nil, not a zero value that would fail validation.
func TestLoad_JiraAbsent(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, "version: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Jira != nil {
		t.Errorf("Jira = %+v, want nil", cfg.Jira)
	}
}
