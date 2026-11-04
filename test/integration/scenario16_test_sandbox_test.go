package integration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// fakeBwrap records its argv (one argument per line, "---" after each call), then skips
// bwrap's own options - honouring --chdir - and executes the command, unsandboxed. With
// FAKE_BWRAP_FAIL set it fails the way AppArmor makes the real one fail.
// fakeBwrap takes its log path and whether to fail as literals: the probe and the test
// run get only the test command's allowlisted environment (specs/hardening/SPEC.md §6.1),
// so a variable set by the test would never reach it.
const fakeBwrap = `#!/bin/sh
log='{{LOG}}'
printf '%s\n' "$@" >> "$log"
echo --- >> "$log"
if [ '{{FAIL}}' = 1 ]; then
  echo "bwrap: setting up uid map: Permission denied" >&2
  exit 1
fi
while [ $# -gt 0 ]; do
  case "$1" in
    --chdir) cd "$2"; shift 2 ;;
    --ro-bind|--bind) shift 3 ;;
    --setenv) export "$2=$3"; shift 3 ;;
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
	bwrapLog := installFakeBwrap(t, false)
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
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"investigate", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
}

func TestScenario16_UnusableSandboxFailsFast(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	installFakeBwrap(t, true)
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

// installFakeBwrap puts fakeBwrap first on PATH and returns the log it writes; with fail
// every call fails as an AppArmor-blocked bwrap does.
func installFakeBwrap(t *testing.T, fail bool) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(t.TempDir(), "bwrap.log")
	failFlag := "0"
	if fail {
		failFlag = "1"
	}
	script := strings.NewReplacer("{{LOG}}", log, "{{FAIL}}", failFlag).Replace(fakeBwrap)
	fakeexec.WriteExecutable(t, dir, "bwrap", script)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// withTestSandbox rewrites the config's test block to use bwrap.
func withTestSandbox(t *testing.T, configPath string) string {
	t.Helper()
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Replace(string(data), "test:\n  sandbox: none\n", "test:\n  sandbox: bwrap\n", 1)
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

// 16c (specs/hardening/SPEC.md §6.3): without test.sandbox the test command is sandboxed - a
// host whose bwrap cannot sandbox fails fast, and the refusal offers test.sandbox: none.
func TestScenario16_SandboxIsTheDefault(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	installFakeBwrap(t, true)
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j)
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	withoutKey := strings.Replace(string(data), "  sandbox: none\n", "", 1)
	if withoutKey == string(data) {
		t.Fatal("no sandbox key to remove")
	}
	if err := os.WriteFile(cfg, []byte(withoutKey), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err = runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
	var depErr *apperr.DependencyError
	if !errors.As(err, &depErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Fatalf("expected a DependencyError (exit 2), got %T: %v", err, err)
	}
	for _, want := range []string{"test.sandbox: bwrap, the default", "set test.sandbox: none to run tests unsandboxed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in:\n%v", want, err)
		}
	}
	if got := invocations(t, invocationsLog); len(got) != 0 {
		t.Errorf("no AI call may happen, got %v", got)
	}
}

// 16d: test.sandbox: none is announced in the authority line of every run.
func TestScenario16_ExplicitNoneWarns(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)
	if !strings.Contains(stdout, "NOT sandboxed: network, $HOME and your files are reachable") {
		t.Errorf("the authority line does not warn about test.sandbox: none:\n%s", stdout)
	}
}
