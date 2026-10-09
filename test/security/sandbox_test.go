//go:build security

package security

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/execution"
	"orchestrator/internal/testexec"
)

// TestTestSandbox checks the real bwrap sandbox of the test step (SPEC.md §8): the same
// command that, run plainly, writes to $HOME, outside the repository, into .git/hooks and
// reaches the network, can do none of that sandboxed - while still writing inside the
// repository. It skips when bwrap cannot sandbox on this host (e.g. AppArmor).
func TestTestSandbox(t *testing.T) {
	if testing.Short() {
		t.Skip("live suite: skipped under -short")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not on PATH")
	}
	executor := execution.NewOSProcessExecutor()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := testexec.Probe(ctx, executor); err != nil {
		t.Skipf("bwrap cannot sandbox on this host:\n%v", err)
	}

	run := func(t *testing.T, sandboxed bool) (repo, home, outside string) {
		f := newFixture(t)
		repo, home, outside = f.dir, t.TempDir(), filepath.Join(t.TempDir(), "outside")
		t.Setenv("HOME", home)
		t.Setenv("ANTHROPIC_API_KEY", "sk-ant-live-planted")
		script := fmt.Sprintf(`printenv ANTHROPIC_API_KEY > env-key || true
printenv PATH > env-path || true
printenv ORCH_BASE_REF > env-base-ref || true
touch inside
touch "$HOME/home-marker"
touch %s
touch .git/hooks/pre-commit
if curl -s -m 5 -o /dev/null https://httpbin.org/get; then touch network-ok; fi
exit 0`, outside)

		runner := testexec.New(executor)
		if sandboxed {
			runner.Sandbox = &testexec.Sandbox{Home: home, RepoRoot: repo, GitDirs: []string{filepath.Join(repo, ".git")}}
		}
		res, err := runner.Run(ctx, script, repo, testexec.RunOptions{Env: []string{"ORCH_BASE_REF=abc123"}})
		if err != nil || !res.Success {
			t.Fatalf("run (sandboxed=%v): %v\nstdout: %s\nstderr: %s", sandboxed, err, res.Stdout, res.Stderr)
		}
		return repo, home, outside
	}

	// Positive control: unsandboxed, every write and the fetch happen.
	repo, home, outside := run(t, false)
	for _, p := range []string{filepath.Join(repo, "inside"), filepath.Join(home, "home-marker"), outside,
		filepath.Join(repo, ".git", "hooks", "pre-commit"), filepath.Join(repo, "network-ok")} {
		if !exists(p) {
			t.Errorf("positive control failed: %s was not written without the sandbox", p)
		}
	}

	// The environment is the allowlist with or without the sandbox (specs/hardening/SPEC.md
	// §6.1): PATH is there, the API key is not.
	checkEnv := func(repo string) {
		t.Helper()
		if data, _ := os.ReadFile(filepath.Join(repo, "env-key")); len(data) != 0 {
			t.Errorf("the test command could read ANTHROPIC_API_KEY: %q", data)
		}
		if data, _ := os.ReadFile(filepath.Join(repo, "env-path")); len(data) == 0 {
			t.Error("the test command lost PATH")
		}
		// The extra pair reaches the command with or without bwrap (specs/quality/SPEC.md §5.2).
		if data, _ := os.ReadFile(filepath.Join(repo, "env-base-ref")); strings.TrimSpace(string(data)) != "abc123" {
			t.Errorf("ORCH_BASE_REF = %q, want abc123", data)
		}
	}
	checkEnv(repo)

	repo, home, outside = run(t, true)
	checkEnv(repo)
	if !exists(filepath.Join(repo, "inside")) {
		t.Error("the sandboxed test could not write inside the repository")
	}
	for name, p := range map[string]string{
		"$HOME":                filepath.Join(home, "home-marker"),
		"outside the repo":     outside,
		".git/hooks":           filepath.Join(repo, ".git", "hooks", "pre-commit"),
		"the network (marker)": filepath.Join(repo, "network-ok"),
	} {
		if exists(p) {
			t.Errorf("the sandboxed test reached %s: %s exists", name, p)
		}
	}
}
