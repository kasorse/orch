package integration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// fakeBwrap records its argv (one argument per line, "---" after each call), then skips
// bwrap's own options - honouring --chdir - and executes the command, unsandboxed. With
// FAKE_BWRAP_FAIL set it fails the way AppArmor makes the real one fail.
const fakeBwrap = `#!/bin/sh
printf '%s\n' "$@" >> "$FAKE_BWRAP_LOG"
echo --- >> "$FAKE_BWRAP_LOG"
if [ -n "${FAKE_BWRAP_FAIL:-}" ]; then
  echo "bwrap: setting up uid map: Permission denied" >&2
  exit 1
fi
while [ $# -gt 0 ]; do
  case "$1" in
    --chdir) cd "$2"; shift 2 ;;
    --ro-bind|--bind) shift 3 ;;
    --dev|--proc|--tmpfs) shift 2 ;;
    --*) shift ;;
    *) exec "$@" ;;
  esac
done
`

// Scenario 16 (specs/permissions/SPEC.md §8): with test.sandbox: bwrap the test command
// runs under bwrap, after a probe in the fail-fast chain.
func TestScenario16_TestRunsSandboxed(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	bwrapLog := installFakeBwrap(t)
	j := startJev(t, scriptBugfixMedium())
	cfg := withTestSandbox(t, jevConfig(t, j))

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	calls := bwrapCalls(t, bwrapLog)
	if len(calls) != 2 {
		t.Fatalf("expected the probe and one test run, got %d bwrap calls:\n%q", len(calls), calls)
	}
	if probe := calls[0]; probe[len(probe)-1] != "true" {
		t.Errorf("the first call must be the probe, got %q", probe)
	}
	home, _ := os.UserHomeDir()
	top, _ := gitOutput(repoDir, "rev-parse", "--show-toplevel")
	gitDir, _ := gitOutput(repoDir, "rev-parse", "--path-format=absolute", "--git-dir")
	run := strings.Join(calls[1], "\n")
	for _, want := range []string{
		"--unshare-net", "--tmpfs\n/tmp", "--tmpfs\n" + home, "--bind\n" + top + "\n" + top,
		"--ro-bind\n" + gitDir + "\n" + gitDir, "sh\n-c\ngo test ./...",
	} {
		if !strings.Contains(run, want) {
			t.Errorf("the test run's bwrap argv lacks %q:\n%s", want, run)
		}
	}
	if !strings.Contains(stdout, "test runs (sandboxed): go test ./...") {
		t.Errorf("expected the sandboxed test in the authority line:\n%s", stdout)
	}
	raw, _ := runFile(t, repoDir, "run.json")
	if !strings.Contains(raw, `"sandboxed": true`) {
		t.Errorf("run.json must record the sandboxed test result")
	}
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"analyze", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
}

func TestScenario16_UnusableSandboxFailsFast(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	installFakeBwrap(t)
	t.Setenv("FAKE_BWRAP_FAIL", "1")
	j := startJev(t, scriptBugfixMedium())
	cfg := withTestSandbox(t, jevConfig(t, j))

	_, _, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
	var depErr *apperr.DependencyError
	if !errors.As(err, &depErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Fatalf("expected a DependencyError (exit 2), got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "AppArmor") {
		t.Errorf("the message must point at the AppArmor profile:\n%v", err)
	}
	if got := invocations(t, invocationsLog); len(got) != 0 {
		t.Errorf("no AI call may happen, got %v", got)
	}
	if n := len(j.Bodies()); n != 0 {
		t.Errorf("no Jev request may happen, got %d", n)
	}
}

// installFakeBwrap puts fakeBwrap first on PATH and returns the log it writes.
func installFakeBwrap(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fakeexec.WriteExecutable(t, dir, "bwrap", fakeBwrap)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := filepath.Join(t.TempDir(), "bwrap.log")
	t.Setenv("FAKE_BWRAP_LOG", log)
	return log
}

// withTestSandbox rewrites the config's test block to use bwrap.
func withTestSandbox(t *testing.T, configPath string) string {
	t.Helper()
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Replace(string(data), "test:\n  command:", "test:\n  sandbox: bwrap\n  command:", 1)
	if out == string(data) {
		t.Fatal("no test block to rewrite")
	}
	if err := os.WriteFile(configPath, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func bwrapCalls(t *testing.T, log string) [][]string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("bwrap was never called: %v", err)
	}
	var calls [][]string
	for _, block := range strings.Split(strings.TrimSuffix(string(data), "---\n"), "---\n") {
		calls = append(calls, strings.Split(strings.TrimSuffix(block, "\n"), "\n"))
	}
	return calls
}
