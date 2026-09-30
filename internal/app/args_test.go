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
		{"bad mode", []string{"--mode", "yolo"}, cliArgs{}, "yolo"},
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
				got.jiraRef != tc.want.jiraRef || !slices.Equal(got.rest, tc.want.rest) {
				t.Errorf("parseArgs(%q) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

// --jira belongs to run alone; with any other command it is a usage error before
// anything else happens - no config is read.
func TestRun_JiraOnlyWithRun(t *testing.T) {
	for _, command := range [][]string{{"version"}, {"validate"}, {"resume", "3f2a9c1e"}} {
		t.Run(command[0], func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--config", "/nonexistent.yaml", "--jira", "PMC-1"}, command...)
			err := Run(context.Background(), args, strings.NewReader(""), &out, &out)
			var cerr *apperr.ConfigError
			if !errors.As(err, &cerr) || err.Error() != "--jira is only valid with run" {
				t.Fatalf("got %v, want the --jira usage error", err)
			}
			if out.Len() != 0 {
				t.Errorf("printed %q", out.String())
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
