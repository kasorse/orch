package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func validListener() ListenerConfig {
	return ListenerConfig{
		PollInterval: time.Minute, ServicesDir: ".", MaxAttempts: 3, CloneTimeout: 10 * time.Minute,
		Query: ListenerQuery{
			Projects: []string{"PAY"}, Label: "ai-run", Statuses: []string{"To Do"}, Reporters: []string{"5b10a2844c20165700ede21g"},
		},
		Labels: ListenerLabels{ServicePrefix: "service:", ProjectPrefix: "gitlab:", Restart: "ai-restart", RefPrefix: "ref:"},
		Transitions: ListenerTransitions{
			Start: "In Progress", Blocked: "Blocked", Failed: "Failed", Done: "Ready to Review", Retry: "To Do",
		},
		MaxParallel: 1,
	}
}

func validListenerStory() ListenerStory {
	return ListenerStory{MaxServices: 5, MaxRefs: 3, MaxTasks: 12, MaxCatalog: 500}
}

// withStory sets a valid listener.story changed by mutate.
func withStory(mutate func(*ListenerStory)) func(*ListenerConfig) {
	return func(l *ListenerConfig) {
		s := validListenerStory()
		mutate(&s)
		l.Story = &s
	}
}

// withBlockers sets listener.blockers to the link type and the done statuses.
func withBlockers(linkType string, done ...string) func(*ListenerConfig) {
	return func(l *ListenerConfig) {
		l.Blockers = &ListenerBlockers{LinkType: linkType, DoneStatuses: done}
	}
}

func ptrFloat(v float64) *float64 { return &v }

func validListenerBlockers() ListenerBlockers {
	return ListenerBlockers{LinkType: "Blocks", DoneStatuses: []string{"Done"}}
}

func validGitLab() GitLabConfig {
	return GitLabConfig{
		BaseURL: "https://gitlab.corp", TokenEnv: "GITLAB_TOKEN", Username: "oauth2",
		AllowedGroups: []string{"backend", "platform/payments"},
	}
}

const listenerYAML = `version: 2
jira:
  base_url: https://example.atlassian.net
  auth: basic
  email_env: E
  token_env: T
listener:
  query:
    projects: [PAY]
    statuses: ["To Do"]
    reporters: [abc]
  transitions: {start: In Progress, blocked: Blocked, failed: Failed, done: Ready to Review, retry: To Do}
gitlab:
  base_url: https://gitlab.corp/
  token_env: GITLAB_TOKEN
  allowed_groups: [backend]
`

func TestLoad_ListenerDefaults(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, listenerYAML))
	if err != nil {
		t.Fatal(err)
	}
	l, g := cfg.Listener, cfg.GitLab
	if l == nil || g == nil {
		t.Fatalf("sections = %v, %v", l, g)
	}
	if l.PollInterval != time.Minute || l.ServicesDir != "." || l.MaxAttempts != 3 || l.CloneTimeout != 10*time.Minute ||
		l.Query.Label != "ai-run" || l.Labels.ServicePrefix != "service:" || l.Labels.ProjectPrefix != "gitlab:" || l.Labels.Restart != "ai-restart" ||
		g.Username != "oauth2" || g.BaseURL != "https://gitlab.corp" {
		t.Errorf("defaults: %+v %+v", l, g)
	}
	if l.MaxParallel != 1 || l.Labels.RefPrefix != "ref:" || l.Blockers != nil || l.Story != nil {
		t.Errorf("listener-story defaults: max_parallel %d, ref_prefix %q, blockers %v, story %v",
			l.MaxParallel, l.Labels.RefPrefix, l.Blockers, l.Story)
	}
	if err := Validate(withListenerSections(cfg)); err != nil {
		t.Errorf("today's listener config: %v", err)
	}
	if p := validateListener(*l); len(p) != 0 {
		t.Errorf("listener problems: %v", p)
	}
	if p := validateGitLab(*g); len(p) != 0 {
		t.Errorf("gitlab problems: %v", p)
	}
}

func TestLoad_ListenerAbsent(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, "version: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listener != nil || cfg.GitLab != nil {
		t.Errorf("sections = %v, %v, want nil", cfg.Listener, cfg.GitLab)
	}
}

func TestLoad_ListenerUnknownKeys(t *testing.T) {
	for name, src := range map[string]string{
		"listener":       strings.Replace(listenerYAML, "listener:\n", "listener:\n  poll: 1m\n", 1),
		"listener.query": strings.Replace(listenerYAML, "  query:\n", "  query:\n    jql: x\n", 1),
		"gitlab":         strings.Replace(listenerYAML, "gitlab:\n", "gitlab:\n  group: x\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeTempConfig(t, src)); err == nil {
				t.Fatal("expected a schema error")
			}
		})
	}
}

func TestLoad_GitLabLiteralTokenRejected(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, listenerYAML+"  token: glpat-secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	p := validateGitLab(*cfg.GitLab)
	if len(p) != 1 || !strings.Contains(p[0], "gitlab.token must not be set in the configuration file") {
		t.Fatalf("problems = %q", p)
	}
	if strings.Contains(p[0], "glpat-secret") {
		t.Error("the token's value appears in the message")
	}
}

func TestLoad_GitLabAPITokenEnv(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, listenerYAML+"  api_token_env: GITLAB_READ_TOKEN\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitLab.APITokenEnv != "GITLAB_READ_TOKEN" {
		t.Fatalf("APITokenEnv = %q", cfg.GitLab.APITokenEnv)
	}
}

func TestValidateListener(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ListenerConfig)
		want   string // substring of the one expected problem; "" means valid
	}{
		{"valid", func(*ListenerConfig) {}, ""},
		{"poll too short", func(l *ListenerConfig) { l.PollInterval = 29 * time.Second }, "listener.poll_interval must be between 30s and 1h0m0s, got 29s"},
		{"poll too long", func(l *ListenerConfig) { l.PollInterval = 2 * time.Hour }, "listener.poll_interval must be between"},
		{"poll bounds", func(l *ListenerConfig) { l.PollInterval = 30 * time.Second }, ""},
		{"attempts zero", func(l *ListenerConfig) { l.MaxAttempts = 0 }, "listener.max_attempts must be between 1 and 10, got 0"},
		{"attempts eleven", func(l *ListenerConfig) { l.MaxAttempts = 11 }, "listener.max_attempts must be between 1 and 10, got 11"},
		{"clone too short", func(l *ListenerConfig) { l.CloneTimeout = 59 * time.Second }, "listener.clone_timeout must be between 1m0s and 1h0m0s, got 59s"},
		{"clone too long", func(l *ListenerConfig) { l.CloneTimeout = 61 * time.Minute }, "listener.clone_timeout must be between"},
		{"services_dir empty", func(l *ListenerConfig) { l.ServicesDir = " " }, "listener.services_dir must be a path"},
		{"default_config relative", func(l *ListenerConfig) { l.DefaultConfig = "service.yaml" }, ""},
		{"default_config absolute", func(l *ListenerConfig) { l.DefaultConfig = "/etc/orch/service.yaml" }, ""},
		{"default_config multiline", func(l *ListenerConfig) { l.DefaultConfig = "a\nb" }, "listener.default_config must be a relative or absolute path"},
		{"projects missing", func(l *ListenerConfig) { l.Query.Projects = nil }, "listener.query.projects is required"},
		{"statuses missing", func(l *ListenerConfig) { l.Query.Statuses = nil }, "listener.query.statuses is required"},
		{"reporters missing", func(l *ListenerConfig) { l.Query.Reporters = nil }, "listener.query.reporters is required"},
		{"project quote", func(l *ListenerConfig) { l.Query.Projects = []string{`P"AY`} }, `listener.query.projects[0] "P\"AY" must be one printable line`},
		{"status backslash", func(l *ListenerConfig) { l.Query.Statuses = []string{`To\Do`} }, "listener.query.statuses[0]"},
		{"reporter newline", func(l *ListenerConfig) { l.Query.Reporters = []string{"a\nb"} }, "listener.query.reporters[0]"},
		{"reporter empty", func(l *ListenerConfig) { l.Query.Reporters = []string{""} }, "listener.query.reporters[0]"},
		{"label quote", func(l *ListenerConfig) { l.Query.Label = `a"b` }, "listener.query.label"},
		{"label space", func(l *ListenerConfig) { l.Query.Label = "ai run" }, "without whitespace"},
		{"service prefix empty", func(l *ListenerConfig) { l.Labels.ServicePrefix = "" }, "listener.labels.service_prefix"},
		{"project prefix space", func(l *ListenerConfig) { l.Labels.ProjectPrefix = "git lab:" }, "listener.labels.project_prefix"},
		{"prefixes equal", func(l *ListenerConfig) { l.Labels.ProjectPrefix = "service:" }, "must differ"},
		{"restart label empty", func(l *ListenerConfig) { l.Labels.Restart = "" }, "listener.labels.restart"},
		{"start missing", func(l *ListenerConfig) { l.Transitions.Start = "" }, "listener.transitions.start is required"},
		{"blocked missing", func(l *ListenerConfig) { l.Transitions.Blocked = "" }, "listener.transitions.blocked is required"},
		{"failed missing", func(l *ListenerConfig) { l.Transitions.Failed = "" }, "listener.transitions.failed is required"},
		{"done missing", func(l *ListenerConfig) { l.Transitions.Done = "" }, "listener.transitions.done is required"},
		{"retry missing", func(l *ListenerConfig) { l.Transitions.Retry = "" }, "listener.transitions.retry is required"},
		{"transition padded", func(l *ListenerConfig) { l.Transitions.Done = " Done" }, "listener.transitions.done"},
		{"transition control", func(l *ListenerConfig) { l.Transitions.Retry = "To\nDo" }, "listener.transitions.retry must be a single line"},
		{"max_parallel zero", func(l *ListenerConfig) { l.MaxParallel = 0 }, "listener.max_parallel must be between 1 and 8, got 0"},
		{"max_parallel nine", func(l *ListenerConfig) { l.MaxParallel = 9 }, "listener.max_parallel must be between 1 and 8, got 9"},
		{"max_parallel eight", func(l *ListenerConfig) { l.MaxParallel = 8 }, ""},
		{"ref prefix empty", func(l *ListenerConfig) { l.Labels.RefPrefix = "" }, "listener.labels.ref_prefix"},
		{"ref prefix space", func(l *ListenerConfig) { l.Labels.RefPrefix = "r ef:" }, "listener.labels.ref_prefix"},
		{"ref prefix is service", func(l *ListenerConfig) { l.Labels.RefPrefix = "service:" }, "listener.labels.ref_prefix must differ"},
		{"ref prefix is project", func(l *ListenerConfig) { l.Labels.RefPrefix = "gitlab:" }, "listener.labels.ref_prefix must differ"},
		{"blockers valid", withBlockers("Blocks", "Done"), ""},
		{"blockers link type missing", withBlockers("", "Done"), "listener.blockers.link_type is required"},
		{"blockers link type padded", withBlockers(" Blocks", "Done"), "listener.blockers.link_type"},
		{"blockers done missing", withBlockers("Blocks"), "listener.blockers.done_statuses is required"},
		{"blockers done empty item", withBlockers("Blocks", " "), "listener.blockers.done_statuses[0]"},
		{"story valid", withStory(func(*ListenerStory) {}), ""},
		{"story bounds low", withStory(func(s *ListenerStory) { *s = ListenerStory{MaxServices: 1, MaxTasks: 1, MaxCatalog: 1} }), ""},
		{"story bounds high", withStory(func(s *ListenerStory) { *s = ListenerStory{20, 10, 30, 2000, nil} }), ""},
		{"story services zero", withStory(func(s *ListenerStory) { s.MaxServices = 0 }), "listener.story.max_services must be between 1 and 20, got 0"},
		{"story services 21", withStory(func(s *ListenerStory) { s.MaxServices = 21 }), "listener.story.max_services must be between 1 and 20, got 21"},
		{"story refs negative", withStory(func(s *ListenerStory) { s.MaxRefs = -1 }), "listener.story.max_refs must be between 0 and 10, got -1"},
		{"story refs 11", withStory(func(s *ListenerStory) { s.MaxRefs = 11 }), "listener.story.max_refs must be between 0 and 10, got 11"},
		{"story tasks zero", withStory(func(s *ListenerStory) { s.MaxTasks = 0 }), "listener.story.max_tasks must be between 1 and 30, got 0"},
		{"story tasks 31", withStory(func(s *ListenerStory) { s.MaxTasks = 31 }), "listener.story.max_tasks must be between 1 and 30, got 31"},
		{"story catalog zero", withStory(func(s *ListenerStory) { s.MaxCatalog = 0 }), "listener.story.max_catalog must be between 1 and 2000, got 0"},
		{"story catalog 2001", withStory(func(s *ListenerStory) { s.MaxCatalog = 2001 }), "listener.story.max_catalog must be between 1 and 2000, got 2001"},
		{"story cost zero", withStory(func(s *ListenerStory) { s.MaxCostUSD = new(float64) }), "listener.story.max_cost_usd must be positive"},
		{"story cost set", withStory(func(s *ListenerStory) { s.MaxCostUSD = ptrFloat(5) }), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := validListener()
			tt.mutate(&l)
			problems := validateListener(l)
			if tt.want == "" {
				if len(problems) != 0 {
					t.Fatalf("expected valid, got %q", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tt.want) {
				t.Fatalf("problems = %q, want exactly one containing %q", problems, tt.want)
			}
		})
	}
}

func TestValidateGitLab(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GitLabConfig)
		want   string
	}{
		{"valid", func(*GitLabConfig) {}, ""},
		{"context path", func(g *GitLabConfig) { g.BaseURL = "https://corp.example/gitlab" }, ""},
		{"http loopback", func(g *GitLabConfig) { g.BaseURL = "http://127.0.0.1:8080" }, ""},
		{"base_url missing", func(g *GitLabConfig) { g.BaseURL = "" }, "gitlab.base_url is required"},
		{"http real host", func(g *GitLabConfig) { g.BaseURL = "http://gitlab.corp" }, "must use https"},
		{"relative", func(g *GitLabConfig) { g.BaseURL = "gitlab.corp" }, "must be an absolute https URL"},
		{"userinfo", func(g *GitLabConfig) { g.BaseURL = "https://me:pw@gitlab.corp" }, "must not contain credentials"},
		{"query", func(g *GitLabConfig) { g.BaseURL = "https://gitlab.corp?x=1" }, "must not have a query or fragment"},
		{"fragment", func(g *GitLabConfig) { g.BaseURL = "https://gitlab.corp#x" }, "must not have a query or fragment"},
		{"token_env missing", func(g *GitLabConfig) { g.TokenEnv = "" }, "gitlab.token_env is required"},
		{"token_env invalid", func(g *GitLabConfig) { g.TokenEnv = "GL-TOKEN" }, `gitlab.token_env "GL-TOKEN" is not a valid`},
		{"api token only", func(g *GitLabConfig) { g.TokenEnv, g.APITokenEnv = "", "GITLAB_READ_TOKEN" }, ""},
		{"api_token_env invalid", func(g *GitLabConfig) { g.APITokenEnv = "GL-READ" }, `gitlab.api_token_env "GL-READ" is not a valid`},
		{"api_token_env same as token_env", func(g *GitLabConfig) { g.APITokenEnv = g.TokenEnv }, "must not be gitlab.token_env"},
		{"literal token", func(g *GitLabConfig) { g.literalSecrets = []string{"token"} }, "gitlab.token must not be set in the configuration file"},
		{"literal password", func(g *GitLabConfig) { g.literalSecrets = []string{"password"} }, "gitlab.password must not be set"},
		{"username empty", func(g *GitLabConfig) { g.Username = "" }, "gitlab.username"},
		{"groups missing", func(g *GitLabConfig) { g.AllowedGroups = nil }, "gitlab.allowed_groups is required"},
		{"group trailing slash", func(g *GitLabConfig) { g.AllowedGroups = []string{"backend/"} }, "gitlab.allowed_groups[0]"},
		{"group dot segment", func(g *GitLabConfig) { g.AllowedGroups = []string{"backend/.."} }, "gitlab.allowed_groups[0]"},
		{"group single dot", func(g *GitLabConfig) { g.AllowedGroups = []string{"."} }, "gitlab.allowed_groups[0]"},
		{"group charset", func(g *GitLabConfig) { g.AllowedGroups = []string{"back end"} }, "gitlab.allowed_groups[0]"},
		{"group empty", func(g *GitLabConfig) { g.AllowedGroups = []string{""} }, "gitlab.allowed_groups[0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := validGitLab()
			tt.mutate(&g)
			problems := validateGitLab(g)
			if tt.want == "" {
				if len(problems) != 0 {
					t.Fatalf("expected valid, got %q", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tt.want) {
				t.Fatalf("problems = %q, want exactly one containing %q", problems, tt.want)
			}
		})
	}
}

func TestValidate_ListenerNeedsJiraAndGitLab(t *testing.T) {
	cfg := validConfig()
	l := validListener()
	cfg.Listener = &l
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "listener needs a jira section") ||
		!strings.Contains(err.Error(), "listener needs a gitlab section") {
		t.Fatalf("err = %v", err)
	}

	j, g := validJira(), validGitLab()
	cfg.Jira, cfg.GitLab = &j, &g
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

// withListenerSections puts the loaded jira, gitlab and listener sections into validConfig, so
// Validate sees them without the rest of a loaded file's requirements.
func withListenerSections(loaded Config) Config {
	cfg := validConfig()
	cfg.Jira, cfg.GitLab, cfg.Listener, cfg.Story = loaded.Jira, loaded.GitLab, loaded.Listener, loaded.Story
	return cfg
}

const listenerStoryYAML = `  max_parallel: 2
  labels:
    ref_prefix: "src:"
  blockers:
    link_type: Blocks
    done_statuses: [Done]
  story:
    max_refs: 0
    max_cost_usd: 5
`

func TestLoad_ListenerStoryKeys(t *testing.T) {
	src := strings.Replace(listenerYAML, "listener:\n", "listener:\n"+listenerStoryYAML, 1)
	cfg, err := Load(writeTempConfig(t, src))
	if err != nil {
		t.Fatal(err)
	}
	l := cfg.Listener
	if l.MaxParallel != 2 || l.Labels.RefPrefix != "src:" || l.Labels.ServicePrefix != "service:" {
		t.Errorf("listener = %+v", l)
	}
	if l.Blockers == nil || l.Blockers.LinkType != "Blocks" || !slices.Equal(l.Blockers.DoneStatuses, []string{"Done"}) {
		t.Errorf("blockers = %+v", l.Blockers)
	}
	s := l.Story
	if s == nil || s.MaxServices != 5 || s.MaxRefs != 0 || s.MaxTasks != 12 || s.MaxCatalog != 500 ||
		s.MaxCostUSD == nil || *s.MaxCostUSD != 5 {
		t.Errorf("story = %+v", s)
	}
	if p := validateListener(*l); len(p) != 0 {
		t.Errorf("listener problems: %v", p)
	}
}

func TestLoad_ListenerStoryUnknownKeys(t *testing.T) {
	for name, extra := range map[string]string{
		"listener.blockers": "  blockers:\n    link_type: Blocks\n    done_statuses: [Done]\n    inward: x\n",
		"listener.story":    "  story:\n    max_children: 3\n",
	} {
		t.Run(name, func(t *testing.T) {
			src := strings.Replace(listenerYAML, "listener:\n", "listener:\n"+extra, 1)
			if _, err := Load(writeTempConfig(t, src)); err == nil {
				t.Fatal("expected a schema error")
			}
		})
	}
}

func TestLoad_StoryIssueTypesAlone(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, listenerYAML+"story:\n  jira_issue_types: [Epic, Initiative]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Story == nil || !slices.Equal(cfg.Story.JiraIssueTypes, []string{"Epic", "Initiative"}) {
		t.Fatalf("story = %+v", cfg.Story)
	}
	if err := Validate(withListenerSections(cfg)); err != nil {
		t.Fatalf("a story section with only jira_issue_types: %v", err)
	}
}

func TestValidate_ListenerStoryCrossChecks(t *testing.T) {
	full := func() Config {
		cfg := validConfig()
		j, g, l := validJira(), validGitLab(), validListener()
		j.Create = &JiraCreateConfig{IssueType: "Task", Link: "parent"}
		g.APITokenEnv = "GITLAB_READ_TOKEN"
		s, b := validListenerStory(), validListenerBlockers()
		l.Story, l.Blockers = &s, &b
		cfg.Jira, cfg.GitLab, cfg.Listener = &j, &g, &l
		return cfg
	}
	if err := Validate(full()); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   []string
	}{
		{"no jira.create", func(c *Config) { c.Jira.Create = nil }, []string{"listener.story needs jira.create"}},
		{"no api_token_env", func(c *Config) { c.GitLab.APITokenEnv = "" }, []string{"listener.story needs gitlab.api_token_env"}},
		{"no blockers", func(c *Config) { c.Listener.Blockers = nil }, []string{"listener.story needs listener.blockers"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := full()
			tt.mutate(&cfg)
			err := Validate(cfg)
			if err == nil {
				t.Fatal("expected a config error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, lacks %q", err, w)
				}
			}
		})
	}

	// Without listener.story none of the three is required.
	cfg := full()
	cfg.Listener.Story, cfg.Listener.Blockers, cfg.Jira.Create, cfg.GitLab.APITokenEnv = nil, nil, nil, ""
	if err := Validate(cfg); err != nil {
		t.Fatalf("no listener.story: %v", err)
	}
}

func TestConfig_CredentialEnvNames_GitLab(t *testing.T) {
	cfg := validConfig()
	g := validGitLab()
	g.APITokenEnv = "GITLAB_READ_TOKEN"
	cfg.GitLab = &g
	if got := cfg.CredentialEnvNames(); !slices.Contains(got, "GITLAB_TOKEN") || !slices.Contains(got, "GITLAB_READ_TOKEN") {
		t.Errorf("CredentialEnvNames() = %q, lacks GITLAB_TOKEN or GITLAB_READ_TOKEN", got)
	}
}

func TestValidate_ListenerNeedsTheGitToken(t *testing.T) {
	cfg := validConfig()
	j, g, l := validJira(), validGitLab(), validListener()
	g.TokenEnv, g.APITokenEnv = "", "GITLAB_READ_TOKEN"
	cfg.Jira, cfg.GitLab, cfg.Listener = &j, &g, &l
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "listener needs gitlab.token_env") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateDependencies_GitLabAPI(t *testing.T) {
	cfg := validConfig()
	t.Setenv("JEV_API_KEY", "k")
	env := map[string]string{}
	opts := DependencyOptions{GitLab: true, getenv: func(n string) string { return env[n] }}
	if err := ValidateDependencies(cfg, FakeDependencyChecker{}, opts); err == nil || !strings.Contains(err.Error(), "needs a gitlab section") {
		t.Fatalf("no section: err = %v", err)
	}
	g := validGitLab()
	cfg.GitLab = &g
	if err := ValidateDependencies(cfg, FakeDependencyChecker{}, opts); err == nil || !strings.Contains(err.Error(), "needs gitlab.api_token_env") {
		t.Fatalf("no key: err = %v", err)
	}
	g.APITokenEnv = "GITLAB_READ_TOKEN"
	if err := ValidateDependencies(cfg, FakeDependencyChecker{}, opts); err == nil || !strings.Contains(err.Error(), "GITLAB_READ_TOKEN, which is not set") {
		t.Fatalf("unset: err = %v", err)
	}
	env["GITLAB_READ_TOKEN"] = "v"
	if err := ValidateDependencies(cfg, FakeDependencyChecker{}, opts); err != nil {
		t.Fatalf("set: err = %v", err)
	}
	if err := ValidateDependencies(cfg, FakeDependencyChecker{}, DependencyOptions{getenv: opts.getenv}); err != nil {
		t.Fatalf("not asked: err = %v", err)
	}
}

func TestValidateDependencies_Listener(t *testing.T) {
	cfg := validConfig()
	j, g, l := validJira(), validGitLab(), validListener()
	cfg.Jira, cfg.GitLab, cfg.Listener = &j, &g, &l
	checker := FakeDependencyChecker{}
	t.Setenv("JEV_API_KEY", "k")

	env := map[string]string{"JIRA_EMAIL": "me@example.com", "JIRA_API_TOKEN": "jt", "GITLAB_TOKEN": "glpat-secret"}
	opts := DependencyOptions{Listener: true, getenv: func(n string) string { return env[n] }}
	if err := ValidateDependencies(cfg, checker, opts); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	delete(env, "GITLAB_TOKEN")
	err := ValidateDependencies(cfg, checker, opts)
	if err == nil || !strings.Contains(err.Error(), "gitlab.token_env names GITLAB_TOKEN, which is not set") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "glpat-secret") || strings.Contains(err.Error(), "jt") && strings.Contains(err.Error(), "me@example.com") {
		t.Errorf("a value appears in the error: %v", err)
	}

	env["GITLAB_TOKEN"] = "glpat-secret"
	delete(env, "JIRA_API_TOKEN")
	if err := ValidateDependencies(cfg, checker, opts); err == nil || !strings.Contains(err.Error(), "jira.token_env names JIRA_API_TOKEN") {
		t.Fatalf("jira variable: err = %v", err)
	}

	env["JIRA_API_TOKEN"] = "jt"
	err = ValidateDependencies(cfg, FakeDependencyChecker{Missing: map[string]bool{"git": true}}, opts)
	if err == nil || !strings.Contains(err.Error(), `"git"`) {
		t.Fatalf("git: err = %v", err)
	}

	// Without the option nothing of the listener is checked.
	if err := ValidateDependencies(cfg, FakeDependencyChecker{Missing: map[string]bool{"git": true}}, DependencyOptions{}); err != nil {
		t.Fatalf("no listener option: %v", err)
	}
}
