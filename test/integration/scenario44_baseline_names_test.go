package integration

import (
	"os"
	"path/filepath"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 44, old and new failures (specs/quality/SPEC.md §8.2-§8.3). The run starts red with
// --allow-red-tests, and the test command's output is shaped like `go test -json`. The retry
// prompt splits the failing tests into those that failed before the run and the new ones. It is
// a hint only: a red suite whose failures are all old still fails the step, on the exit code.

// fakeJSONTests is a test command that fails the way the scenario scripts it, one run per call
// (the count is kept next to the script, outside the repository):
//
//	run 1 (the pre-run test) TestA and TestB fail
//	run 2                    TestB and TestC fail: A is fixed, C is new
//	run 3                    TestB fails: every failure is an old one
//	run 4                    passes
const fakeJSONTests = `dir=$(dirname "$0")
n=$(cat "$dir/count" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$dir/count"
fail() {
  for name in "$@"; do
    printf '{"Action":"fail","Package":"fixture","Test":"%s"}\n' "$name"
  done
  printf '{"Action":"fail","Package":"fixture"}\n'
  exit 1
}
case "$n" in
  1) fail TestA TestB ;;
  2) fail TestB TestC ;;
  3) fail TestB ;;
esac
printf '{"Action":"pass","Package":"fixture"}\n'
`

func TestScenario44_OldAndNewFailuresAreSplitInThePrompt(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	script := filepath.Join(t.TempDir(), "tests.sh")
	if err := os.WriteFile(script, []byte(fakeJSONTests), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newQualityPreRunRun(t, repoDir, func(o *configOptions) { o.testCommand = "sh " + script })

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "--allow-red-tests", "fix the failing tests")...)
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "implement", "implement", "review"}) {
		t.Fatalf("invocations = %v: every red suite, old failures only included, goes back to implement", got)
	}

	// The first implement is shown the red start, with the names the output gave.
	assertContains(t, "the first implement prompt", r.prompt(t, "implement", 1),
		"The test command already FAILS before any change",
		"Failing tests (parsed from the output): fixture.TestA, fixture.TestB")

	// After the second run: TestB is old, TestC is new, and TestA is no longer failing.
	second := r.prompt(t, "implement", 2)
	assertContains(t, "the second implement prompt", second,
		"Failing before this run started (the task may be to fix them): fixture.TestB\n",
		"New failures since the run started: fixture.TestC\n")
	assertNotContains(t, "the second implement prompt", second, "fixture.TestA", "Failing tests (parsed from the output)")

	// After the third run every failure is an old one: no "new" line, and the step still failed.
	third := r.prompt(t, "implement", 3)
	assertContains(t, "the third implement prompt", third,
		"Failing before this run started (the task may be to fix them): fixture.TestB\n")
	assertNotContains(t, "the third implement prompt", third, "New failures since the run started")

	run := loadQL(t, repoDir)
	if run.Status != "completed" || run.FixIterations != 2 {
		t.Errorf("status = %s, fix_iterations = %d", run.Status, run.FixIterations)
	}
	if pre := run.PreRunTest; pre == nil || pre.Success || pre.Format != "gojson" || !equalStrings(pre.FailedTests, []string{"fixture.TestA", "fixture.TestB"}) {
		t.Errorf("pre_run_test = %+v", pre)
	}
	tests := run.ofAction("test")
	if len(tests) != 3 {
		t.Fatalf("test steps = %d, want 3", len(tests))
	}
	wantNames := [][]string{{"fixture.TestB", "fixture.TestC"}, {"fixture.TestB"}, nil}
	for i, exec := range tests {
		res := exec.TestResult
		if res == nil || !equalStrings(res.FailedTests, wantNames[i]) {
			t.Errorf("test %d: %+v, want failed_tests %v", i+1, res, wantNames[i])
			continue
		}
		// Pass or fail is the exit code: the third run's only failure is an old one, and it failed.
		if wantFail := i < 2; res.Success == wantFail || (wantFail && (res.ExitCode != 1 || res.Format != "gojson")) {
			t.Errorf("test %d: success = %t, exit_code = %d, format = %q", i+1, res.Success, res.ExitCode, res.Format)
		}
	}
}
