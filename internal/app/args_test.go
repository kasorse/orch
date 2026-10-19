package app

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    cliArgs
		wantErr string
	}{
		{"defaults", []string{"run", "fix it"},
			cliArgs{configPath: defaultConfigPath, rest: []string{"run", "fix it"}}, ""},
		{"all flags", []string{"--config", "c.yaml", "--mode=auto", "-v", "run", "x"},
			cliArgs{configPath: "c.yaml", mode: domain.ModeAuto, verbose: true, rest: []string{"run", "x"}}, ""},
		{"jira separate value", []string{"run", "--jira", "PMC-1"},
			cliArgs{configPath: defaultConfigPath, jiraRef: "PMC-1", rest: []string{"run"}}, ""},
		{"jira equals", []string{"--jira=pmc-1", "run"},
			cliArgs{configPath: defaultConfigPath, jiraRef: "pmc-1", rest: []string{"run"}}, ""},
		{"jira after notes", []string{"run", "only", "the", "FE", "--jira", "https://h/browse/PMC-1", "--mode", "plan"},
			cliArgs{configPath: defaultConfigPath, mode: domain.ModePlan, jiraRef: "https://h/browse/PMC-1",
				rest: []string{"run", "only", "the", "FE"}}, ""},
		{"jira value trimmed", []string{"run", "--jira", " PMC-1 "},
			cliArgs{configPath: defaultConfigPath, jiraRef: "PMC-1", rest: []string{"run"}}, ""},
		{"jira twice", []string{"run", "--jira", "PMC-1", "--jira=PMC-2"}, cliArgs{}, "--jira given twice"},
		{"jira empty", []string{"run", "--jira="}, cliArgs{}, "--jira requires an issue key or URL"},
		{"jira blank", []string{"run", "--jira", "  "}, cliArgs{}, "--jira requires an issue key or URL"},
		{"jira missing value", []string{"run", "--jira"}, cliArgs{}, "--jira requires a value"},
		{"config missing value", []string{"run", "--config"}, cliArgs{}, "--config requires a value"},
		{"mode missing value", []string{"--mode"}, cliArgs{}, "--mode requires a value"},
		{"stage separate", []string{"run", "--stage", "review", "x"},
			cliArgs{configPath: defaultConfigPath, stages: []domain.Stage{domain.StageReview}, rest: []string{"run", "x"}}, ""},
		{"stage equals, repeated, alias, any position", []string{"--stage=test", "run", "x", "--stage", "Review"},
			cliArgs{configPath: defaultConfigPath, stages: []domain.Stage{domain.StageTesting, domain.StageReview}, rest: []string{"run", "x"}}, ""},
		{"stage duplicate collapsed", []string{"run", "--stage", "test", "--stage=testing", "x"},
			cliArgs{configPath: defaultConfigPath, stages: []domain.Stage{domain.StageTesting}, rest: []string{"run", "x"}}, ""},
		{"stage with jira", []string{"run", "--stage", "review", "--jira", "PMC-1"},
			cliArgs{configPath: defaultConfigPath, jiraRef: "PMC-1", stages: []domain.Stage{domain.StageReview}, rest: []string{"run"}}, ""},
		{"stage unknown", []string{"run", "--stage", "reveiw", "x"}, cliArgs{}, `unknown stage "reveiw"; valid: research, analysis`},
		{"stage missing value", []string{"run", "--stage"}, cliArgs{}, "--stage requires a value"},
		{"stage comma", []string{"run", "--stage", "test,review", "x"}, cliArgs{}, "one stage per flag"},
		{"bad mode", []string{"--mode", "yolo"}, cliArgs{}, "yolo"},
		{"claude-auth separate, any position", []string{"run", "x", "--claude-auth", "api"},
			cliArgs{configPath: defaultConfigPath, claudeAuth: "api", rest: []string{"run", "x"}}, ""},
		{"claude-auth equals", []string{"--claude-auth=login", "validate"},
			cliArgs{configPath: defaultConfigPath, claudeAuth: "login", rest: []string{"validate"}}, ""},
		{"claude-auth twice", []string{"run", "--claude-auth", "api", "--claude-auth=login"}, cliArgs{}, "--claude-auth given twice"},
		{"claude-auth bad value", []string{"--claude-auth", "sub", "run"}, cliArgs{}, "--claude-auth must be auto, api or login"},
		{"claude-auth missing value", []string{"run", "--claude-auth"}, cliArgs{}, "--claude-auth requires a value"},
		{"repo separate, equals, repeated, any position", []string{"--repo", "gateway", "story", "plan", "x", "--repo=billing"},
			cliArgs{configPath: defaultConfigPath, repos: []string{"gateway", "billing"}, rest: []string{"story", "plan", "x"}}, ""},
		{"repo duplicate collapsed", []string{"story", "plan", "--repo", "billing", "--repo= billing ", "x"},
			cliArgs{configPath: defaultConfigPath, repos: []string{"billing"}, rest: []string{"story", "plan", "x"}}, ""},
		{"repo with jira", []string{"story", "plan", "--jira", "PAY-1", "--repo", "billing"},
			cliArgs{configPath: defaultConfigPath, jiraRef: "PAY-1", repos: []string{"billing"}, rest: []string{"story", "plan"}}, ""},
		{"repo empty", []string{"story", "plan", "--repo=", "x"}, cliArgs{}, "--repo requires a service name"},
		{"repo missing value", []string{"story", "plan", "x", "--repo"}, cliArgs{}, "--repo requires a value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArgs(tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parseArgs(%q) error = %v, want one containing %q", tc.args, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.configPath != tc.want.configPath || got.mode != tc.want.mode || got.verbose != tc.want.verbose ||
				got.claudeAuth != tc.want.claudeAuth || !slices.Equal(got.repos, tc.want.repos) ||
				got.jiraRef != tc.want.jiraRef || !slices.Equal(got.stages, tc.want.stages) || !slices.Equal(got.rest, tc.want.rest) {
				t.Errorf("parseArgs(%q) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

// --jira belongs to run and story plan; with any other command it is a usage error before
// anything else happens - no config is read.
func TestRun_JiraOnlyWithRun(t *testing.T) {
	for _, command := range [][]string{{"version"}, {"validate"}, {"resume", "3f2a9c1e"}, {"story"}, {"story", "run", "specs/x"}} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml", "--jira", "PMC-1"}, command...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || err.Error() != "--jira is only valid with run and story plan" {
				t.Fatalf("got %v, want the --jira usage error", err)
			}
			if out.Len() != 0 {
				t.Errorf("printed %q", out.String())
			}
		})
	}
}

// --claude-auth belongs to run, resume and validate: a usage error elsewhere (corner case 13).
func TestRun_ClaudeAuthOnlyWithRunResumeValidate(t *testing.T) {
	for _, command := range [][]string{{"version"}, {"decisions", "report"}, {"runs", "report"}} {
		t.Run(command[0], func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml", "--claude-auth", "api"}, command...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || err.Error() != "--claude-auth is only valid with run, resume and validate" {
				t.Fatalf("got %v, want the --claude-auth usage error", err)
			}
		})
	}
}

func TestRun_RunUsage(t *testing.T) {
	var out bytes.Buffer
	err := Run(context.Background(), []string{"run"}, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(err.Error(), `run --jira <KEY|URL> ["notes"]`) {
		t.Fatalf("got %v, want the run usage", err)
	}
	err = Run(context.Background(), nil, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(err.Error(), `run --jira <KEY|URL> ["notes"]`) {
		t.Fatalf("got %v, want the general usage", err)
	}
}

// --repo belongs to story plan alone: a usage error before any config is read.
func TestRun_RepoOnlyWithStoryPlan(t *testing.T) {
	for _, command := range [][]string{{"run", "x"}, {"validate"}, {"story", "run", "specs/x"}} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml", "--repo", "billing"}, command...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || err.Error() != "--repo is only valid with story plan" {
				t.Fatalf("got %v, want the --repo usage error", err)
			}
			if out.Len() != 0 {
				t.Errorf("printed %q", out.String())
			}
		})
	}
}

// story takes the plan subcommand and, without --jira, a task: anything else is its usage,
// before any config is read.
func TestRun_StoryUsage(t *testing.T) {
	for _, command := range [][]string{{"story"}, {"story", "frobnicate"}, {"story", "plan"}, {"story", "plan", "  "}} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml"}, command...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || err.Error() != storyPlanUsage {
				t.Fatalf("got %v, want the story plan usage", err)
			}
			if out.Len() != 0 {
				t.Errorf("printed %q", out.String())
			}
		})
	}
	err := Run(context.Background(), nil, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `story plan [--jira <KEY|URL>] [--repo <name>]... "<task>"`) {
		t.Errorf("the general usage lacks story plan: %v", err)
	}
}

// --stage belongs to run alone, like --jira: a usage error before any config is read.
func TestRun_StageOnlyWithRun(t *testing.T) {
	for _, command := range [][]string{{"version"}, {"validate"}, {"resume", "3f2a9c1e"}} {
		t.Run(command[0], func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml", "--stage", "review"}, command...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || err.Error() != "--stage is only valid with run" {
				t.Fatalf("got %v, want the --stage usage error", err)
			}
			if out.Len() != 0 {
				t.Errorf("printed %q", out.String())
			}
		})
	}
}
