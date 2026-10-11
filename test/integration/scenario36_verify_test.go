package integration

import (
	"os"
	"strings"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 36, verify (specs/quality/SPEC.md §6): with actions.implement.verify the implement
// session gets the orchestrator's own MCP server. The fake claude is its client
// (FAKE_VERIFY_CALLS): it speaks initialize and notifications/initialized, then calls
// run_tests, run_check and run_tests with a filter. The calls are recorded in verify_calls,
// their logs go to the run directory, the temporary directory is gone, and the run is green.
//
// The server is the test binary itself (TestMain answers the hidden mcp-verify subcommand),
// and the sandbox its runs need is the fake bwrap of scenario 16.

// verifyRun is a manual run whose fake implement is an MCP client of the verify server.
type verifyRun struct {
	qualityRun
	// verifyDir is FAKE_VERIFY_DIR: what the fake client saved of each implement call.
	verifyDir string
	// bwrapLog is the fake bwrap's log of its argv.
	bwrapLog string
}

// newVerifyRun configures verify (with one check, vet) over a fake bwrap, and the fake implement
// to make calls (FAKE_VERIFY_CALLS syntax: "run_tests;run_check:vet;run_tests:TestX").
func newVerifyRun(t *testing.T, repoDir, calls string, mutate ...func(*configOptions)) verifyRun {
	t.Helper()
	r := newQualityRun(t, repoDir, append([]func(*configOptions){func(o *configOptions) {
		o.verify = true
		o.testSandbox = "bwrap"
		o.checks = []checkOption{{"vet", "go vet ./..."}}
		// The verify calls run inside the implement call, so its timeout has to cover them.
		o.timeout = "120s"
	}}, mutate...)...)
	dir := t.TempDir()
	t.Setenv("FAKE_VERIFY_CALLS", calls)
	t.Setenv("FAKE_VERIFY_DIR", dir)
	return verifyRun{qualityRun: r, verifyDir: dir, bwrapLog: installFakeBwrap(t, false)}
}

func TestScenario36_VerifyCallsAreRecorded(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	v := newVerifyRun(t, repoDir, "run_tests;run_check:vet;run_tests:TestAdd")

	stdout, stderr, err := runOrchestrator(t, repoDir, v.cfg, append(implementAndTest, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	run := loadQL(t, repoDir)
	if run.Status != "completed" || !equalStrings(run.actions(), []string{"implement", "test"}) {
		t.Fatalf("status = %s, executions = %v\nstdout:\n%s\nstderr:\n%s", run.Status, run.actions(), stdout, stderr)
	}

	// What the server recorded, in order. The fake edits first, so the tests pass.
	want := []qlVerifyCall{
		{Tool: "run_tests", ExitCode: 0, Success: true},
		{Tool: "run_check", Check: "vet", ExitCode: 0, Success: true},
		{Tool: "run_tests", Filter: "TestAdd", ExitCode: 0, Success: true},
	}
	got := run.Executions[0].VerifyCalls
	if len(got) != len(want) {
		t.Fatalf("verify_calls = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("verify_calls[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if run.Outcome == nil || run.Outcome.VerifyCalls != 3 {
		t.Errorf("outcome = %+v, want 3 verify calls", run.Outcome)
	}

	// The client's side: the server answered initialize and all three calls.
	responses := readFile(t, v.verifyDir+"/verify_1.responses")
	assertContains(t, "the server's answers", responses, `"protocolVersion"`, `"serverInfo"`)
	if n := strings.Count(responses, "exit code: 0"); n != 3 {
		t.Errorf("the server answered %d calls with exit code 0, want 3:\n%s", n, responses)
	}

	// The prompt names the tools, the argv offers them without widening the built-in tools
	// (specs/quality/SPEC.md §6.9), and the filter reached the sandbox as an environment
	// variable only.
	assertContains(t, "the implement prompt", v.prompt(t, "implement", 1), "mcp__orch__run_tests", "mcp__orch__run_check")
	args := v.args(t, "implement")
	if got := argAfter(args, "--tools"); got != "Read,Grep,Glob,Edit,Write" {
		t.Errorf("--tools = %q, verify must not add a built-in tool", got)
	}
	assertContains(t, "--allowed-tools", argAfter(args, "--allowed-tools"), "mcp__orch__run_tests", "mcp__orch__run_check")
	assertContains(t, "the fake bwrap's log", readFile(t, v.bwrapLog), "--setenv\nORCH_TEST_FILTER\nTestAdd\n")

	// Each call's output is a log of the step (the passing vet printed nothing, so it has none),
	// and the temporary directory is gone.
	assertContains(t, "the first call's log", runDirFile(t, repoDir, "logs/000-implement.verify-1.stdout.log"), "ok")
	assertContains(t, "the third call's log", runDirFile(t, repoDir, "logs/000-implement.verify-3.stdout.log"), "ok")
	dir := strings.TrimSpace(readFile(t, v.verifyDir+"/verify_1.dir"))
	if !strings.Contains(dir, "orch-verify-") {
		t.Fatalf("the fake client saved the server's directory as %q", dir)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the verify directory %s survived the step (stat: %v)", dir, err)
	}
}
