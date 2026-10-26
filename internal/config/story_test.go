package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/story"
)

func loadStoryConfig(t *testing.T, extra string) (Config, error) {
	t.Helper()
	return Load(writeTempConfig(t, "version: 2\n"+extra))
}

func TestStoryConfig_Defaults(t *testing.T) {
	cfg, err := loadStoryConfig(t, "story:\n  roots: [..]\n")
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Story
	if s == nil {
		t.Fatal("Story is nil")
	}
	if s.MaxServices != 5 || s.MaxTasks != 12 || s.SpecDir != "specs" || s.MaxCostUSD != nil || len(s.JiraIssueTypes) != 0 {
		t.Errorf("defaults = %+v", s)
	}
}

// story.max_parallel is 1 to 8 and defaults to 1.
func TestStoryConfig_MaxParallel(t *testing.T) {
	cfg, err := loadStoryConfig(t, "story:\n  roots: [..]\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Story.MaxParallel != 1 {
		t.Errorf("default max_parallel = %d, want 1", cfg.Story.MaxParallel)
	}
	for _, n := range []string{"1", "8"} {
		cfg, err := loadStoryConfig(t, "story:\n  roots: [..]\n  max_parallel: "+n+"\n")
		if err != nil {
			t.Fatal(err)
		}
		valid := validConfig()
		valid.Story = cfg.Story
		if err := Validate(valid); err != nil {
			t.Errorf("max_parallel %s: %v", n, err)
		}
	}
	for _, n := range []string{"0", "9", "-1"} {
		cfg, err := loadStoryConfig(t, "story:\n  roots: [..]\n  max_parallel: "+n+"\n")
		if err != nil {
			t.Fatal(err)
		}
		invalid := validConfig()
		invalid.Story = cfg.Story
		if err := Validate(invalid); err == nil || !strings.Contains(err.Error(), "story.max_parallel must be between 1 and 8") {
			t.Errorf("max_parallel %s: err = %v, want a config error", n, err)
		}
	}
}

// story.max_acceptance_chars is 50 to 2000 and defaults to 300, the story package's own limits.
func TestStoryConfig_MaxAcceptanceChars(t *testing.T) {
	if DefaultStoryMaxAcceptanceChars != story.MaxAcceptanceRunes || MaxStoryMaxAcceptanceChars != story.MaxAcceptanceRunesCeiling {
		t.Fatalf("the config's limits drifted from the story package's")
	}
	cfg, err := loadStoryConfig(t, "story:\n  roots: [..]\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Story.MaxAcceptanceChars != 300 {
		t.Errorf("default max_acceptance_chars = %d, want 300", cfg.Story.MaxAcceptanceChars)
	}
	for _, c := range []struct {
		n  string
		ok bool
	}{{"50", true}, {"2000", true}, {"49", false}, {"2001", false}, {"0", false}} {
		cfg, err := loadStoryConfig(t, "story:\n  roots: [..]\n  max_acceptance_chars: "+c.n+"\n")
		if err != nil {
			t.Fatal(err)
		}
		valid := validConfig()
		valid.Story = cfg.Story
		err = Validate(valid)
		if c.ok != (err == nil) || (!c.ok && !strings.Contains(err.Error(), "story.max_acceptance_chars must be between 50 and 2000")) {
			t.Errorf("max_acceptance_chars %s: err = %v", c.n, err)
		}
	}
}

func TestStoryConfig_AbsentIsNil(t *testing.T) {
	cfg, err := loadStoryConfig(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Story != nil {
		t.Errorf("Story = %+v, want nil", cfg.Story)
	}
}

func TestStoryConfig_UnknownKeyRefused(t *testing.T) {
	for _, src := range []string{
		"story:\n  rots: [..]\n",
		"jira:\n  base_url: https://x.example.com\n  create:\n    issue_type: Story\n    lable: [a]\n",
	} {
		if _, err := loadStoryConfig(t, src); err == nil || !strings.Contains(err.Error(), "unknown configuration keys") {
			t.Errorf("%q: err = %v, want unknown key", src, err)
		}
	}
}

func TestValidate_StoryProblemsNameTheKey(t *testing.T) {
	neg := -1.0
	for _, tc := range []struct {
		name string
		s    StoryConfig
		want string
	}{
		{"services low", StoryConfig{MaxServices: 0, MaxTasks: 12, SpecDir: "specs"}, "story.max_services"},
		{"services high", StoryConfig{MaxServices: 21, MaxTasks: 12, SpecDir: "specs"}, "story.max_services"},
		{"tasks low", StoryConfig{MaxServices: 5, MaxTasks: 1, SpecDir: "specs"}, "story.max_tasks"},
		{"tasks high", StoryConfig{MaxServices: 5, MaxTasks: 31, SpecDir: "specs"}, "story.max_tasks"},
		{"cost", StoryConfig{MaxServices: 5, MaxTasks: 12, SpecDir: "specs", MaxCostUSD: &neg}, "story.max_cost_usd"},
		{"spec dir", StoryConfig{MaxServices: 5, MaxTasks: 12, SpecDir: "../x"}, "story.spec_dir"},
		{"root", StoryConfig{MaxServices: 5, MaxTasks: 12, SpecDir: "specs", Roots: []string{" "}}, "story.roots[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Story = &tc.s
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidate_StoryValid(t *testing.T) {
	cfg, err := loadStoryConfig(t, "story:\n  roots: [.., ~/src]\n  max_cost_usd: 20\n  jira_issue_types: [Epic]\n")
	if err != nil {
		t.Fatal(err)
	}
	base := validConfig()
	base.Story = cfg.Story
	if err := Validate(base); err != nil {
		t.Fatal(err)
	}
}

type costStub map[string]bool

func (c costStub) ReportsCost(p string) bool { return c[p] }

func TestStoryBudgetProblems(t *testing.T) {
	cfg := validConfig()
	cost := 5.0
	cfg.Story = &StoryConfig{MaxServices: 5, MaxTasks: 12, SpecDir: "specs", MaxCostUSD: &cost}
	cfg.Actions.Investigate.Provider = "codex"

	got := storyBudgetProblems(cfg, costStub{"claude": true})
	if len(got) != 1 || !strings.Contains(got[0], "story.max_cost_usd") || !strings.Contains(got[0], "actions.investigate") {
		t.Errorf("problems = %v", got)
	}
	if got := storyBudgetProblems(cfg, costStub{"claude": true, "codex": true}); len(got) != 0 {
		t.Errorf("problems = %v, want none", got)
	}
	cfg.Story.MaxCostUSD = nil
	if got := storyBudgetProblems(cfg, nil); len(got) != 0 {
		t.Errorf("no budget: problems = %v", got)
	}
}

func TestResolveRoots(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storyHome := filepath.Join(tmp, "work", "home")
	userHome := filepath.Join(tmp, "user")
	abs := filepath.Join(tmp, "abs")
	sibling := filepath.Join(tmp, "work", "sibling")
	under := filepath.Join(userHome, "src")
	real := filepath.Join(tmp, "real")
	for _, d := range []string{storyHome, abs, sibling, under, real} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	s := StoryConfig{Roots: []string{abs, "~/src", "../sibling", link}}
	got, err := s.resolveRoots(storyHome, userHome)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{abs, under, sibling, real}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("root %d = %s, want %s", i, got[i], want[i])
		}
	}

	file := filepath.Join(tmp, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{filepath.Join(tmp, "missing"), file} {
		_, err := StoryConfig{Roots: []string{bad}}.resolveRoots(storyHome, userHome)
		if err == nil || !strings.Contains(err.Error(), "story.roots[0]") {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
}

func TestJiraCreate(t *testing.T) {
	base := "jira:\n  base_url: https://example.atlassian.net\n  auth: bearer\n  token_env: T\n  create:\n"
	cfg, err := loadStoryConfig(t, base+"    issue_type: Story\n    labels: [orchestrator]\n    components: {billing: Billing}\n")
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Jira.Create
	if c == nil || c.IssueType != "Story" || c.Link != "parent" || c.Components["billing"] != "Billing" {
		t.Fatalf("create = %+v", c)
	}
	valid := validConfig()
	valid.Jira = cfg.Jira
	if err := Validate(valid); err != nil {
		t.Fatal(err)
	}

	if cfg, err = loadStoryConfig(t, "jira:\n  base_url: https://example.atlassian.net\n  auth: bearer\n  token_env: T\n"); err != nil || cfg.Jira.Create != nil {
		t.Errorf("no create: err = %v, create = %+v", err, cfg.Jira)
	}

	for _, tc := range []struct{ yaml, want string }{
		{"    link: field\n    issue_type: Story\n", "jira.create.field is required"},
		{"    link: field\n    field: epic\n    issue_type: Story\n", "jira.create.field"},
		{"    link: sideways\n    issue_type: Story\n", "jira.create.link"},
		{"    issue_type: Story\n    field: customfield_1\n", "jira.create.field applies only"},
		{"    link: parent\n", "jira.create.issue_type is required"},
	} {
		cfg, err := loadStoryConfig(t, base+tc.yaml)
		if err != nil {
			t.Fatal(err)
		}
		c := validConfig()
		c.Jira = cfg.Jira
		if err := Validate(c); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want %q", tc.yaml, err, tc.want)
		}
	}

	cfg, err = loadStoryConfig(t, base+"    issue_type: Story\n    link: field\n    field: customfield_10014\n")
	if err != nil {
		t.Fatal(err)
	}
	c2 := validConfig()
	c2.Jira = cfg.Jira
	if err := Validate(c2); err != nil {
		t.Errorf("link field: %v", err)
	}
}
