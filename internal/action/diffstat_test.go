package action

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

// editingClient stands in for implement: it writes files, then answers.
type editingClient struct {
	edit   func()
	output string
}

func (c *editingClient) Supports(domain.Authority) bool { return true }

func (c *editingClient) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	c.edit()
	return provider.Result{Output: c.output}, nil
}

// An implement step records what it changed from git: against the tree before the call,
// against the restore point, and how that compares with the files its artifact names.
func TestExecuteImplement_RecordsTheDiffFromGit(t *testing.T) {
	repo, dir := guardRepo(t)
	put(t, dir, "lib.go", "package main\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-qm", "lib")
	head := strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))
	// An earlier step of the run changed lib.go: in the run's diff, not in this step's.
	put(t, dir, "lib.go", "package main\n\nfunc lib() {}\n")

	client := &editingClient{
		edit: func() {
			put(t, dir, "main.go", "package main\n\nfunc main() {}\n")
			put(t, dir, "new.go", "package main\n\nvar x = 1\n")
			put(t, dir, "img.bin", "\x00\x01\x02")
		},
		output: `{"summary":"s","changed_files":["main.go","` + filepath.Join(dir, "ghost.go") + `"],"tests_added":["./new.go"]}`,
	}
	d := newDispatcher(client)
	d.Repo = repo
	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "t"}, PreRunRef: head}

	outcome, err := d.Execute(context.Background(), domain.ActionImplement, run)
	if err != nil {
		t.Fatal(err)
	}
	want := &domain.StepDiff{
		Step:              domain.DiffStat{Files: 3, Insertions: 5, Deletions: 0, Binary: 1},
		Run:               &domain.DiffStat{Files: 4, Insertions: 7, Deletions: 0, Binary: 1},
		NotReported:       []string{"img.bin"},
		ReportedUnchanged: []string{"ghost.go"},
	}
	if !reflect.DeepEqual(outcome.Diff, want) {
		t.Errorf("diff = %+v (run %+v)\nwant   %+v (run %+v)", outcome.Diff, outcome.Diff.Run, want, want.Run)
	}
	if outcome.TreeBefore == "" || outcome.TreeAfter == "" || outcome.TreeBefore == outcome.TreeAfter {
		t.Errorf("trees = %q, %q", outcome.TreeBefore, outcome.TreeAfter)
	}
	if outcome.Call == nil || outcome.Call.PromptBytes == 0 {
		t.Errorf("call = %+v, want the prompt's size", outcome.Call)
	}
}

// Without changed_files (the model did not answer in JSON) nothing is compared, and a guard
// violation leaves git alone.
func TestExecuteImplement_DiffEdgeCases(t *testing.T) {
	repo, dir := guardRepo(t)
	client := &editingClient{edit: func() { put(t, dir, "main.go", "package main // x\n") }, output: "prose"}
	d := newDispatcher(client)
	d.Repo = repo
	outcome, err := d.Execute(context.Background(), domain.ActionImplement, &domain.Run{ID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Diff == nil || outcome.Diff.Step.Files != 1 || outcome.Diff.Run != nil || outcome.Diff.NotReported != nil {
		t.Errorf("diff = %+v", outcome.Diff)
	}

	d = newDispatcher(client)
	d.Repo = repo
	d.Guard = &fakeGuard{changed: []string{".git/config"}}
	outcome, err = d.Execute(context.Background(), domain.ActionImplement, &domain.Run{ID: "r"})
	if err == nil || outcome.Diff != nil || outcome.TreeAfter != "" {
		t.Errorf("after a guard violation: err %v, diff %+v, tree %q", err, outcome.Diff, outcome.TreeAfter)
	}
}

func TestReportedPaths(t *testing.T) {
	dir := t.TempDir()
	got := reportedPaths([]string{"a.go", " ./b/c.go ", filepath.Join(dir, "d.go"), "../outside.go", "", "a.go", "e.go\x1b"}, dir, dir)
	if want := []string{"a.go", "b/c.go", "d.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("reportedPaths = %q, want %q", got, want)
	}
	// The model works in a subdirectory: its paths are relative to it.
	sub := filepath.Join(dir, "svc")
	got = reportedPaths([]string{"main.go", filepath.Join(sub, "x.go")}, dir, sub)
	if want := []string{"svc/main.go", "svc/x.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("from a subdirectory: %q, want %q", got, want)
	}
}

func TestCompareReported_Caps(t *testing.T) {
	var changed []string
	for i := range domain.MaxDiffPaths + 5 {
		changed = append(changed, filepath.Join("d", string(rune('a'+i%26)), strings.Repeat("x", i+1)))
	}
	notReported, unchanged := compareReported(changed, []string{"ghost.go"})
	if len(notReported) != domain.MaxDiffPaths || !reflect.DeepEqual(unchanged, []string{"ghost.go"}) {
		t.Errorf("notReported %d, unchanged %q", len(notReported), unchanged)
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
