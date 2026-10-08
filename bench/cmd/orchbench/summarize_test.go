package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchbench/internal/exitcode"
	"orchbench/internal/results"
)

func resultLine(t *testing.T, r results.Record) string {
	t.Helper()
	r.Schema = results.Schema
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// resultsDir is a results directory with four cells: tasks t1 and t2 under A and B.
func resultsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var text string
	for _, task := range []string{"t1", "t2"} {
		text += resultLine(t, results.Record{Task: task, Config: "A", Rep: 1, Language: "go", Category: "bugfix", Result: results.Fail, FailReason: results.FailOracle})
		text += resultLine(t, results.Record{Task: task, Config: "B", Rep: 1, Language: "go", Category: "bugfix", Result: results.Pass})
	}
	if err := os.WriteFile(filepath.Join(dir, "results.jsonl"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func summarizeArgs(args ...string) (stdout string, err error) {
	var out, errOut bytes.Buffer
	err = run(context.Background(), append([]string{"summarize"}, args...), &out, &errOut)
	return out.String(), err
}

func TestSummarizeWritesSummaryMd(t *testing.T) {
	dir := resultsDir(t)

	stdout, err := summarizeArgs(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "summary.md")
	if want := "wrote " + path + " (4 cells)\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Benchmark summary", "Based on 4 cells", "## Paired vs A", "### go (2 tasks, insufficient data)"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("summary.md does not contain %q:\n%s", want, got)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("directory holds %d files, want results.jsonl and summary.md only", len(entries))
	}
}

func TestSummarizeTildeIsExpanded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	text := resultLine(t, results.Record{Task: "t1", Config: "A", Rep: 1, Result: results.Pass})
	if err := os.WriteFile(filepath.Join(home, "out", "results.jsonl"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := summarizeArgs("~/out"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "out", "summary.md")); err != nil {
		t.Error(err)
	}
}

func TestSummarizeStdoutWritesNothingToDisk(t *testing.T) {
	dir := resultsDir(t)

	stdout, err := summarizeArgs(dir, "--stdout")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout, "# Benchmark summary\n") || strings.Contains(stdout, "wrote ") {
		t.Errorf("stdout = %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "summary.md")); !os.IsNotExist(err) {
		t.Errorf("summary.md: stat error = %v, want not exist", err)
	}
}

func TestSummarizeFlagsMayFollowTheDirectory(t *testing.T) {
	dir := resultsDir(t)

	stdout, err := summarizeArgs(dir, "--stdout", "--baseline", "B", "--by", "category", "--seed", "7")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Baseline B · seed 7", "## Paired vs B", "## By category"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output does not contain %q:\n%s", want, stdout)
		}
	}
	if n := strings.Count(stdout, "## By category"); n != 1 {
		t.Errorf("--by category shows the category table %d times, want once:\n%s", n, stdout)
	}
}

func TestSummarizeIsDeterministic(t *testing.T) {
	dir := resultsDir(t)

	first, err := summarizeArgs(dir, "--stdout", "--seed", "3")
	if err != nil {
		t.Fatal(err)
	}
	second, err := summarizeArgs(dir, "--stdout", "--seed", "3")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("two runs differ:\n%s\n---\n%s", first, second)
	}
}

func TestSummarizeUsageErrors(t *testing.T) {
	empty := t.TempDir()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := resultsDir(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no directory argument", nil, "<out> is required"},
		{"a directory that does not exist", []string{filepath.Join(empty, "nope")}, "is not a results directory"},
		{"a file instead of a directory", []string{file}, "is not a results directory"},
		{"a directory without results.jsonl", []string{empty}, "results.jsonl not found"},
		{"a bad --by", []string{dir, "--by", "colour"}, `--by "colour"`},
		{"a negative --seed", []string{dir, "--seed", "-1"}, "--seed -1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, err := summarizeArgs(tt.args...)
			if code := exitcode.FromError(err); code != exitcode.Usage {
				t.Fatalf("exit code %d (%v), want %d", code, err, exitcode.Usage)
			}
			if !strings.Contains(err.Error(), tt.want) || !strings.HasPrefix(err.Error(), "orchbench summarize: ") {
				t.Errorf("error %q does not read %q", err, tt.want)
			}
			if stdout != "" {
				t.Errorf("stdout = %q", stdout)
			}
		})
	}
}

func TestSummarizeMalformedLineFails(t *testing.T) {
	dir := t.TempDir()
	text := resultLine(t, results.Record{Task: "t1", Config: "A", Rep: 1, Result: results.Pass}) + "not json\n" +
		resultLine(t, results.Record{Task: "t2", Config: "A", Rep: 1, Result: results.Pass})
	if err := os.WriteFile(filepath.Join(dir, "results.jsonl"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := summarizeArgs(dir)
	if code := exitcode.FromError(err); code != exitcode.Failed {
		t.Fatalf("exit code %d (%v), want %d", code, err, exitcode.Failed)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q does not name line 2", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "summary.md")); !os.IsNotExist(err) {
		t.Errorf("summary.md was written: %v", err)
	}
}

func TestSummarizeOfAnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "results.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, err := summarizeArgs(dir, "--stdout")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "No results yet") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestSummarizeSkipsAHalfWrittenLastLine(t *testing.T) {
	dir := t.TempDir()
	text := resultLine(t, results.Record{Task: "t1", Config: "A", Rep: 1, Result: results.Pass}) + `{"schema":1,"task":"t2","conf`
	if err := os.WriteFile(filepath.Join(dir, "results.jsonl"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, err := summarizeArgs(dir, "--stdout")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Based on 1 cell ", "half written and was ignored"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
}

func TestSummarizeSaysWhatARedoReplaced(t *testing.T) {
	dir := t.TempDir()
	text := resultLine(t, results.Record{Task: "t1", Config: "A", Rep: 1, Result: results.InfraError}) +
		resultLine(t, results.Record{Task: "t1", Config: "A", Rep: 1, Result: results.Pass})
	if err := os.WriteFile(filepath.Join(dir, "results.jsonl"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, err := summarizeArgs(dir, "--stdout")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Based on 1 cell ", "1 result line replaced by a later line", "0 infra_error"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "half written") {
		t.Errorf("stdout mentions a half-written line:\n%s", stdout)
	}
}

func TestSummarizeCancelledContext(t *testing.T) {
	dir := resultsDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out, errOut bytes.Buffer
	err := run(ctx, []string{"summarize", dir}, &out, &errOut)
	if code := exitcode.FromError(err); code != exitcode.Interrupted {
		t.Fatalf("exit code %d (%v), want %d", code, err, exitcode.Interrupted)
	}
	if _, err := os.Stat(filepath.Join(dir, "summary.md")); !os.IsNotExist(err) {
		t.Errorf("summary.md was written: %v", err)
	}
}
