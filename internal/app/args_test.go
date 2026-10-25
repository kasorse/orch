package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
		{"stage unknown", []string{"run", "--stage", "reveiw", "x"}, cliArgs{}, `unknown stage "reveiw"; valid: research, investigation`},
		{"stage investigate alias", []string{"run", "--stage", "investigate", "x"},
			cliArgs{configPath: defaultConfigPath, stages: []domain.Stage{domain.StageInvestigation}, rest: []string{"run", "x"}}, ""},
		{"stage analysis removed", []string{"run", "--stage", "analysis", "x"}, cliArgs{}, "specs/clarify"},
		{"stage analyze removed", []string{"run", "--stage=analyze", "x"}, cliArgs{}, "specs/clarify"},
		{"stage planning renamed", []string{"run", "--stage", "planning", "x"}, cliArgs{}, "specs/clarify"},
		{"stage plan renamed", []string{"run", "--stage=plan", "x"}, cliArgs{}, "specs/clarify"},
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

func TestParseArgs_StoryTask(t *testing.T) {
	for _, args := range [][]string{{"run", "--story-task", "t.json"}, {"--story-task=t.json", "run"}} {
		got, err := parseArgs(args)
		if err != nil || got.storyTaskFile != "t.json" || !slices.Equal(got.rest, []string{"run"}) {
			t.Errorf("parseArgs(%q) = %+v, %v", args, got, err)
		}
	}
	for _, args := range [][]string{{"run", "--story-task"}, {"run", "--story-task="}, {"run", "--story-task", "a", "--story-task", "b"}} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%q) succeeded", args)
		}
	}
}

// --story-task is hidden and belongs to run alone, without the flags that say what the task is or
// where it runs: a usage error before any config is read.
func TestRun_StoryTaskFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"validate", []string{"validate", "--story-task", "t.json"}, "--story-task is only valid with run"},
		{"stage", []string{"run", "--story-task", "t.json", "--stage", "review"}, "--story-task cannot be combined with --stage"},
		{"jira", []string{"run", "--story-task", "t.json", "--jira", "PMC-1"}, "--story-task cannot be combined with --jira"},
		{"inplace", []string{"run", "--story-task", "t.json", "--inplace"}, "--story-task cannot be combined with --inplace"},
		{"worktree", []string{"run", "--story-task", "t.json", "--worktree"}, "--story-task cannot be combined with --worktree"},
		{"text", []string{"run", "--story-task", "t.json", "fix it"}, "--story-task cannot be combined with task text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml"}, tc.args...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
			if out.Len() != 0 {
				t.Errorf("printed %q", out.String())
			}
		})
	}
	if strings.Contains(usage, "story-task") {
		t.Error("the usage text lists the hidden --story-task")
	}
}

// A task input that breaks a rule is refused before the configuration is even read.
func TestRun_StoryTaskInputIsCheckedFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.json")
	if err := os.WriteFile(path, []byte(`{"story":"refunds"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := Run(context.Background(), []string{"--config", "/nonexistent.yaml", "run", "--story-task", path}, strings.NewReader(""), &out, &out)
	var cerr *apperr.ConfigError
	if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "the task input") {
		t.Fatalf("got %v, want a refusal of the task input", err)
	}
	err = Run(context.Background(), []string{"--config", "/nonexistent.yaml", "run", "--story-task", path + ".missing"}, strings.NewReader(""), &out, &out)
	if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "cannot read the story task input") {
		t.Fatalf("got %v, want a refusal of the missing file", err)
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

// --claude-auth belongs to run, resume, validate and story run: a usage error elsewhere (corner case 13).
func TestRun_ClaudeAuthOnlyWithRunResumeValidate(t *testing.T) {
	for _, command := range [][]string{{"version"}, {"decisions", "report"}, {"runs", "report"}, {"story", "plan", "x"}, {"story", "status", "d"}} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml", "--claude-auth", "api"}, command...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || err.Error() != "--claude-auth is only valid with run, resume, validate and story run" {
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
	cases := []struct {
		command []string
		want    string
	}{
		{[]string{"story"}, storyUsage},
		{[]string{"story", "frobnicate"}, storyUsage},
		{[]string{"story", "plan"}, storyPlanUsage},
		{[]string{"story", "plan", "  "}, storyPlanUsage},
		{[]string{"story", "run"}, storyRunUsage},
		{[]string{"story", "run", "specs/a", "specs/b"}, storyRunUsage},
		{[]string{"story", "status"}, storyStatusUsage},
		{[]string{"story", "status", "specs/a", "specs/b"}, storyStatusUsage},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.command, " "), func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml"}, tc.command...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || err.Error() != tc.want {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if out.Len() != 0 {
				t.Errorf("printed %q", out.String())
			}
		})
	}
	err := Run(context.Background(), nil, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	for _, want := range []string{`story plan [--jira <KEY|URL>] [--repo <name>]... "<task>"`, `story run <spec-dir> [--retry <ID>]`, `story status <spec-dir>`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("the general usage lacks %q: %v", want, err)
		}
	}
}

// story run and story status take one directory; --retry, a task ID, belongs to story run alone
// and --mode and --claude-auth pass through it.
func TestParseArgs_StoryRun(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want cliArgs
	}{
		{"plain", []string{"story", "run", "specs/refunds"},
			cliArgs{configPath: defaultConfigPath, rest: []string{"story", "run", "specs/refunds"}}},
		{"retry separate, any position", []string{"--retry", "RF02", "story", "run", "specs/refunds"},
			cliArgs{configPath: defaultConfigPath, retry: "RF02", rest: []string{"story", "run", "specs/refunds"}}},
		{"retry equals, mode, claude-auth, config", []string{"story", "run", "specs/refunds", "--retry=RF02", "--mode", "auto", "--claude-auth=login", "--config", "c.yaml"},
			cliArgs{configPath: "c.yaml", configGiven: true, mode: domain.ModeAuto, claudeAuth: "login", retry: "RF02", rest: []string{"story", "run", "specs/refunds"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArgs(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if got.configPath != tc.want.configPath || got.configGiven != tc.want.configGiven || got.mode != tc.want.mode ||
				got.claudeAuth != tc.want.claudeAuth || got.retry != tc.want.retry || !slices.Equal(got.rest, tc.want.rest) {
				t.Errorf("parseArgs(%q) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}

	bad := []struct {
		name string
		args []string
		want string
	}{
		{"retry twice", []string{"story", "run", "d", "--retry", "RF01", "--retry=RF02"}, "--retry given twice"},
		{"retry missing value", []string{"story", "run", "d", "--retry"}, "--retry requires a value"},
		{"retry bad ID", []string{"story", "run", "d", "--retry", "rf2"}, "--retry requires a task ID"},
		{"retry a branch", []string{"story", "run", "d", "--retry=../RF02"}, "--retry requires a task ID"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseArgs(tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("parseArgs(%q) error = %v, want one containing %q", tc.args, err, tc.want)
			}
		})
	}
}

// --parallel is a number from 1 to 8, and belongs to story run alone.
func TestParseArgs_Parallel(t *testing.T) {
	for args, want := range map[string]int{"--parallel 1": 1, "--parallel=8": 8, "--parallel 2": 2} {
		got, err := parseArgs(append(strings.Fields(args), "story", "run", "d"))
		if err != nil || got.parallel != want {
			t.Errorf("%s: parallel = %d, err = %v; want %d", args, got.parallel, err, want)
		}
	}
	for _, args := range [][]string{
		{"--parallel", "0"}, {"--parallel", "9"}, {"--parallel=-1"}, {"--parallel", "two"}, {"--parallel"},
		{"--parallel", "2", "--parallel", "3"},
	} {
		if _, err := parseArgs(append(args, "story", "run", "d")); err == nil || !strings.Contains(err.Error(), "--parallel") {
			t.Errorf("parseArgs(%q) error = %v, want a --parallel usage error", args, err)
		}
	}

	for _, command := range [][]string{{"run", "x"}, {"validate"}, {"story", "plan", "x"}, {"story", "status", "specs/x"}} {
		var out bytes.Buffer
		args := append([]string{"--config", "/nonexistent.yaml", "--parallel", "2"}, command...)
		err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
		var cerr *apperr.ConfigError
		if !errors.As(err, &cerr) || err.Error() != "--parallel is only valid with story run" {
			t.Errorf("%v: got %v, want the --parallel usage error", command, err)
		}
	}
}

// --retry is a usage error with any command but story run, before any config is read.
func TestRun_RetryOnlyWithStoryRun(t *testing.T) {
	for _, command := range [][]string{{"run", "x"}, {"validate"}, {"story", "plan", "x"}, {"story", "status", "specs/x"}} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml", "--retry", "RF02"}, command...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || err.Error() != "--retry is only valid with story run" {
				t.Fatalf("got %v, want the --retry usage error", err)
			}
			if out.Len() != 0 {
				t.Errorf("printed %q", out.String())
			}
		})
	}
}

// --claude-auth passes through story run (to the children it spawns) and --jira and --repo do not.
func TestRun_StoryRunFlags(t *testing.T) {
	flags := []struct {
		flag, value, want string
	}{
		{"--jira", "PMC-1", "--jira is only valid with run and story plan"},
		{"--repo", "billing", "--repo is only valid with story plan"},
	}
	for _, tc := range flags {
		var out bytes.Buffer
		err := Run(context.Background(), []string{"--config", "/nonexistent.yaml", tc.flag, tc.value, "story", "run", "specs/x"}, strings.NewReader(""), &out, &out)
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s with story run: %v, want %q", tc.flag, err, tc.want)
		}
	}
	// With a valid shape the command starts: the missing config is what stops it, not the flag.
	var out bytes.Buffer
	err := Run(context.Background(), []string{"--config", "/nonexistent.yaml", "--claude-auth", "login", "--mode", "auto", "story", "run", t.TempDir()}, strings.NewReader(""), &out, &out)
	if err == nil || strings.Contains(err.Error(), "only valid") {
		t.Errorf("story run with --claude-auth and --mode: %v", err)
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

// run story <dir> is not a story run: it is refused before any config is read, with the command
// that runs the spec's tasks.
func TestRun_RunStoryDirIsRefused(t *testing.T) {
	spec := t.TempDir()
	var out bytes.Buffer
	err := Run(context.Background(), []string{"--config", "/nonexistent.yaml", "run", "story", spec}, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(err.Error(), "story plan") {
		t.Fatalf("got %v, want a hint to story plan", err)
	}
	if err := os.Mkdir(filepath.Join(spec, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	err = Run(context.Background(), []string{"--config", "/nonexistent.yaml", "run", "story", spec}, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(err.Error(), "orchestrator story run "+spec) {
		t.Fatalf("got %v, want a hint to story run", err)
	}
}
