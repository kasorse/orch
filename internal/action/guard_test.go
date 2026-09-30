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

// TestRepoGuard_GitChangeStopsBeforeGitStatus: a planted core.fsmonitor would run on the
// guard's own git status, so a moved fingerprint must be reported without running it.
func TestRepoGuard_GitChangeStopsBeforeGitStatus(t *testing.T) {
	repo, dir := guardRepo(t)
	g := NewRepoGuard(repo, nil)
	ctx := context.Background()
	before, err := g.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	hook := filepath.Join(t.TempDir(), "fsmonitor.sh")
	put(t, filepath.Dir(hook), filepath.Base(hook), "#!/bin/sh\ntouch "+marker+"\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("[core]\n\tfsmonitor = " + hook + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := g.Changed(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{gitViolation}) {
		t.Errorf("Changed = %q, want [%s]", got, gitViolation)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the planted core.fsmonitor ran during the guard's check")
	}
}

// recordingExecutor runs real processes and records every argv, so a test can assert
// which git commands ran.
type recordingExecutor struct {
	inner execution.ProcessExecutor
	argv  [][]string
}

func (r *recordingExecutor) Run(ctx context.Context, spec execution.Spec) (execution.ProcessResult, error) {
	r.argv = append(r.argv, append([]string{spec.Executable}, spec.Args...))
	return r.inner.Run(ctx, spec)
}

func (r *recordingExecutor) ran(sub string) bool {
	for _, a := range r.argv {
		if len(a) > 1 && a[0] == "git" && slices.Contains(a[1:], sub) {
			return true
		}
	}
	return false
}

func baselineOf(t *testing.T, g *RepoGuard) []byte {
	t.Helper()
	state, err := g.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGuardState_JSONRoundTrip(t *testing.T) {
	repo, dir := guardRepo(t)
	// A pre-existing protected change is part of the baseline, not a violation.
	put(t, dir, ".claude/settings.local.json", `{"x":1}`)
	g := NewRepoGuard(repo, []string{"orchestrator.yaml"})
	data := baselineOf(t, g)

	var back GuardState
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !slices.Equal(back.patterns, g.Patterns) || back.gitFingerprint == "" || back.protected[".claude/settings.local.json"] == "" {
		t.Errorf("round trip lost state: %+v", back)
	}
	changed, err := g.Changed(context.Background(), back)
	if err != nil || len(changed) != 0 {
		t.Errorf("Changed(round-tripped) on an unchanged tree = %v, %v", changed, err)
	}

	for name, raw := range map[string]string{
		"unknown field":  `{"patterns":["a"],"git_fingerprint":"f","protected":{},"extra":1}`,
		"no fingerprint": `{"patterns":["a"],"git_fingerprint":"","protected":{}}`,
		"no patterns":    `{"patterns":[],"git_fingerprint":"f","protected":{}}`,
		"no protected":   `{"patterns":["a"],"git_fingerprint":"f"}`,
	} {
		if err := json.Unmarshal([]byte(raw), &back); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCheckBaseline(t *testing.T) {
	t.Run("unchanged", func(t *testing.T) {
		repo, dir := guardRepo(t)
		baseline := baselineOf(t, NewRepoGuard(repo, nil))
		put(t, dir, "main.go", "package main // a normal source edit\n")
		if changed, err := CheckBaseline(context.Background(), repo, baseline); err != nil || len(changed) != 0 {
			t.Errorf("CheckBaseline = %v, %v", changed, err)
		}
	})

	t.Run("uses the baseline's own patterns", func(t *testing.T) {
		repo, dir := guardRepo(t)
		baseline := baselineOf(t, NewRepoGuard(repo, []string{"go.mod"}))
		put(t, dir, "go.mod", "module x\n")
		changed, err := CheckBaseline(context.Background(), repo, baseline)
		if err != nil || !slices.Equal(changed, []string{"go.mod"}) {
			t.Errorf("CheckBaseline = %v, %v; want [go.mod]", changed, err)
		}
	})

	t.Run("a protected change left by a crash or a failed step", func(t *testing.T) {
		repo, dir := guardRepo(t)
		baseline := baselineOf(t, NewRepoGuard(repo, nil))
		put(t, dir, ".claude/settings.json", `{"hooks":{}}`)
		changed, _ := CheckBaseline(context.Background(), repo, baseline)
		if !slices.Equal(changed, []string{".claude/settings.json"}) {
			t.Errorf("changed = %v", changed)
		}
		// Reverting it makes the run resumable again.
		os.Remove(filepath.Join(dir, ".claude/settings.json"))
		if changed, _ := CheckBaseline(context.Background(), repo, baseline); len(changed) != 0 {
			t.Errorf("after revert: %v", changed)
		}
	})

	t.Run("fingerprint before any git status", func(t *testing.T) {
		repo, dir := guardRepo(t)
		baseline := baselineOf(t, NewRepoGuard(repo, nil))
		f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("[core]\n\tfsmonitor = true\n")
		f.Close()

		rec := &recordingExecutor{inner: execution.NewOSProcessExecutor()}
		changed, err := CheckBaseline(context.Background(), &repository.Repository{Dir: dir, Executor: rec}, baseline)
		if err != nil || !slices.Equal(changed, []string{gitViolation}) {
			t.Fatalf("CheckBaseline = %v, %v", changed, err)
		}
		if rec.ran("status") {
			t.Errorf("git status ran after the fingerprint changed: %v", rec.argv)
		}
		if msg := BaselineViolation(changed, "abc").Error(); !strings.Contains(msg, "since the run started") ||
			!strings.Contains(msg, "Do not run git") || !strings.Contains(msg, "Restore point: abc") ||
			!strings.Contains(msg, "Moving or renaming the repository") {
			t.Errorf("violation message:\n%s", msg)
		}
	})

	t.Run("invalid baseline", func(t *testing.T) {
		repo, _ := guardRepo(t)
		if _, err := CheckBaseline(context.Background(), repo, []byte(`{}`)); err == nil {
			t.Error("accepted an empty baseline")
		}
	})
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
