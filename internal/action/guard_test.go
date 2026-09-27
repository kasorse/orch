package action

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"orchestrator/internal/execution"
	"orchestrator/internal/repository"
)

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

func TestRepoGuard(t *testing.T) {
	cases := []struct {
		name  string
		write func(t *testing.T, dir string)
		want  []string
	}{
		{"source edit", func(t *testing.T, dir string) { put(t, dir, "main.go", "package main // fixed\n") }, nil},
		{"agent settings", func(t *testing.T, dir string) { put(t, dir, ".claude/settings.json", "{}") }, []string{".claude/settings.json"}},
		{"ci workflow", func(t *testing.T, dir string) { put(t, dir, ".github/workflows/ci.yml", "on: push") }, []string{".github/workflows/ci.yml"}},
		{"ignored local agent settings", func(t *testing.T, dir string) { put(t, dir, ".claude/settings.local.json", "{}") }, []string{".claude/settings.local.json"}},
		{"file in an ignored protected directory", func(t *testing.T, dir string) { put(t, dir, ".vscode/tasks.json", "{}") }, []string{".vscode/tasks.json"}},
		{"orchestrator config", func(t *testing.T, dir string) {
			put(t, dir, "orchestrator.yaml", "version: 2\ntest: {command: curl x}\n")
		}, []string{"orchestrator.yaml"}},
		{"extra pattern", func(t *testing.T, dir string) { put(t, dir, "go.mod", "module x\n") }, []string{"go.mod"}},
		{"git hook", func(t *testing.T, dir string) { put(t, dir, ".git/hooks/pre-commit", "#!/bin/sh\n") }, []string{gitViolation}},
		{"git config", func(t *testing.T, dir string) {
			f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString("[core]\n\tfsmonitor = true\n"); err != nil {
				t.Fatal(err)
			}
		}, []string{gitViolation}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, dir := guardRepo(t)
			g := NewRepoGuard(repo, []string{"orchestrator.yaml", "go.mod"})
			ctx := context.Background()
			before, err := g.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			tc.write(t, dir)
			got, err := g.Changed(ctx, before)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Changed = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRepoGuard_PreExistingChange: with require_clean_worktree off, a protected file that
// was already changed before the call is only a violation if the call touches it.
func TestRepoGuard_PreExistingChange(t *testing.T) {
	repo, dir := guardRepo(t)
	put(t, dir, ".envrc", "export A=1\n")
	g := NewRepoGuard(repo, nil)
	ctx := context.Background()

	before, err := g.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := g.Changed(ctx, before); len(got) != 0 {
		t.Errorf("untouched pre-existing change reported: %q", got)
	}

	put(t, dir, ".envrc", "export A=2\n")
	if got, _ := g.Changed(ctx, before); !slices.Equal(got, []string{".envrc"}) {
		t.Errorf("Changed = %q, want [.envrc]", got)
	}

	if err := os.Remove(filepath.Join(dir, ".envrc")); err != nil {
		t.Fatal(err)
	}
	if got, _ := g.Changed(ctx, before); !slices.Equal(got, []string{".envrc"}) {
		t.Errorf("removing the pre-existing change must count too, got %q", got)
	}
}
