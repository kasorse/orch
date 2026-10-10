package action

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/execution"
	"orchestrator/internal/repository"
)

// The guard itself is tested in package guard; these tests cover what stays here: the
// aliases, the resume baseline wrappers and the run directory patterns.

func guardRepo(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	put(t, dir, "main.go", "package main\n")
	put(t, dir, "orchestrator.yaml", "version: 2\n")
	// Like this repository's own: the config is ignored, and so is a whole agent
	// settings directory and Claude's local settings file.
	put(t, dir, ".gitignore", "orchestrator.yaml\nsettings.local.json\n.vscode/\n")
	run("add", "-A")
	run("commit", "-qm", "init")
	return &repository.Repository{Dir: dir, Executor: execution.NewOSProcessExecutor()}, dir
}

func put(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRepoGuard_ThroughTheAliases(t *testing.T) {
	repo, dir := guardRepo(t)
	var g Guard = NewRepoGuard(repo, []string{"go.mod"})
	ctx := context.Background()
	before, err := g.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	put(t, dir, "go.mod", "module x\n")
	got, err := g.Changed(ctx, before)
	if err != nil || !slices.Equal(got, []string{"go.mod"}) {
		t.Errorf("Changed = %v, %v; want [go.mod]", got, err)
	}

	// The baseline a run stores is the State's JSON; CheckBaseline reads it back.
	var state GuardState = before
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := CheckBaseline(ctx, repo, data)
	if err != nil || !slices.Equal(changed, []string{"go.mod"}) {
		t.Errorf("CheckBaseline = %v, %v; want [go.mod]", changed, err)
	}
	if _, err := CheckBaseline(ctx, repo, []byte(`{}`)); err == nil {
		t.Error("accepted an empty baseline")
	}
}

func TestBaselineViolation_Message(t *testing.T) {
	msg := BaselineViolation([]string{gitViolation}, "abc").Error()
	if !strings.Contains(msg, "since the run started") ||
		!strings.Contains(msg, "Do not run git") || !strings.Contains(msg, "Restore point: abc") ||
		!strings.Contains(msg, "Moving or renaming the repository") {
		t.Errorf("violation message:\n%s", msg)
	}
	if strings.Contains(BaselineViolation([]string{"go.mod"}, "").Error(), "Moving or renaming") {
		t.Error("a protected-path change is not a moved repository")
	}
}

func TestRunDirPatterns(t *testing.T) {
	if RunDirPatterns("") != nil {
		t.Error("a runs directory outside the worktree must add no patterns")
	}
	for _, ignored := range []bool{false, true} {
		t.Run(fmt.Sprintf("ignored=%v", ignored), func(t *testing.T) {
			repo, dir := guardRepo(t)
			if ignored {
				put(t, dir, ".gitignore", "orchestrator.yaml\nsettings.local.json\n.vscode/\n.orchestrator/\n")
				cmd := exec.Command("git", "commit", "-qam", "ignore runs")
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
			}
			const other = ".orchestrator/runs/3f2a9c1e-0000-4000-8000-000000000001/"
			put(t, dir, other+"run.json", `{"id":"x"}`)
			put(t, dir, other+"artifacts/000-plan.json", `{}`)
			put(t, dir, other+"logs/000-plan.stdout.log", "log")

			g := NewRepoGuard(repo, RunDirPatterns(".orchestrator/runs"))
			before, err := g.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			put(t, dir, other+"logs/000-plan.stdout.log", "more log")
			if changed, _ := g.Changed(context.Background(), before); len(changed) != 0 {
				t.Errorf("a log write is not a resume input: %v", changed)
			}
			put(t, dir, other+"run.json", `{"id":"tampered"}`)
			put(t, dir, other+"artifacts/001-implement.json", `{}`)
			put(t, dir, other+"decision.json", `{}`)
			changed, err := g.Changed(context.Background(), before)
			want := []string{other + "artifacts/001-implement.json", other + "decision.json", other + "run.json"}
			if err != nil || !slices.Equal(changed, want) {
				t.Errorf("changed = %v, %v; want %v", changed, err, want)
			}
		})
	}
}
