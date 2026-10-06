package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/persistence"
	"orchestrator/internal/report"
)

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
	return stdout.String(), err
}

func TestRunsReportArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{"default dir", []string{"report"}, runsBaseDir, false},
		{"dir separate", []string{"report", "--dir", "bench/runs"}, "bench/runs", false},
		{"dir equals", []string{"report", "--dir=bench/runs"}, "bench/runs", false},
		{"dir twice", []string{"report", "--dir", "a", "--dir", "b"}, "", true},
		{"no subcommand", nil, "", true},
		{"unknown subcommand", []string{"list"}, "", true},
		{"unknown flag", []string{"report", "--json"}, "", true},
		{"dir without value", []string{"report", "--dir"}, "", true},
		{"dir empty", []string{"report", "--dir="}, "", true},
		{"extra argument", []string{"report", "bench/runs"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runsReportArgs(tc.args)
			if tc.wantErr {
				var ce *apperr.ConfigError
				if !errors.As(err, &ce) || !strings.Contains(ce.Msg, "usage: orchestrator runs report [--dir <path>]") {
					t.Fatalf("runsReportArgs(%q) error = %v, want a ConfigError with the usage", tc.args, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("runsReportArgs(%q) = %q, %v; want %q", tc.args, got, err, tc.want)
			}
		})
	}
}

func TestRunsReport_NestedTreeFindsEveryRun(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"taskA/cfg1/rep1", "taskA/cfg1/rep2", "taskA/cfg2/rep1", "taskB/cfg1/rep1"} {
		if _, err := persistence.NewStore(filepath.Join(root, dir)).NewRun(domain.Task{Description: "fix"}); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runCLI(t, "runs", "report", "--dir", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Runs report: " + root + "\n",
		"runs: 4, unreadable: 0\n",
		"  pending ",
		"\nDecisions report: " + root + "\nno decision traces yet\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestRunsReport_UnreadableIsListedNotFatal(t *testing.T) {
	root := t.TempDir()
	if _, err := persistence.NewStore(filepath.Join(root, "taskA", "rep1")).NewRun(domain.Task{Description: "fix"}); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, "taskB", "rep1")
	if _, err := persistence.NewStore(bad).NewRun(domain.Task{Description: "fix"}); err != nil {
		t.Fatal(err)
	}
	runs, _ := filepath.Glob(filepath.Join(bad, "*", "run.json"))
	if len(runs) != 1 {
		t.Fatalf("run.json files: %v", runs)
	}
	if err := os.WriteFile(runs[0], []byte(`{"surprise":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLI(t, "runs", "report", "--dir="+root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "runs: 1, unreadable: 1\n  "+filepath.Join("taskB", "rep1")) {
		t.Errorf("the unreadable run must be listed by its path under the directory:\n%s", out)
	}
}

func TestRunsReport_MissingDirIsNoRuns(t *testing.T) {
	out, err := runCLI(t, "runs", "report", "--dir", filepath.Join(t.TempDir(), "missing"))
	if err != nil || !strings.Contains(out, "no runs yet") {
		t.Errorf("out %q, err %v", out, err)
	}
}

func TestRunsReport_RejectsBadUsage(t *testing.T) {
	for _, args := range [][]string{
		{"runs"},
		{"runs", "report", "--json"},
		{"runs", "report", "extra"},
		{"runs", "report", "--dir"},
	} {
		_, err := runCLI(t, args...)
		var ce *apperr.ConfigError
		if !errors.As(err, &ce) {
			t.Errorf("Run(%q) error = %v, want a ConfigError", args, err)
		}
	}
}

// `decisions report` prints the decisions section and nothing of the runs report.
func TestDecisionsReport_PrintsOnlyTheDecisionsSection(t *testing.T) {
	t.Chdir(t.TempDir())

	out, err := runCLI(t, "decisions", "report")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Decisions report: .orchestrator/runs\nno decision traces yet\n"; out != want {
		t.Errorf("empty report = %q, want %q", out, want)
	}

	store := persistence.NewStore(runsBaseDir)
	run, err := store.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	trace := domain.DecisionTrace{Request: "fix", Source: domain.DecisionSourceManual}
	if err := store.SaveDecision(run.ID, trace); err != nil {
		t.Fatal(err)
	}

	out, err = runCLI(t, "decisions", "report")
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	report.RenderDecisions(&want, runsBaseDir, report.SummarizeDecisions([]report.Entry{{RunID: run.ID, Trace: trace}}, nil))
	if out != want.String() {
		t.Errorf("report = %q, want %q", out, want.String())
	}
	if strings.Contains(out, "Runs report") || strings.Contains(out, "by status") {
		t.Errorf("decisions report must not print the runs section:\n%s", out)
	}
}
