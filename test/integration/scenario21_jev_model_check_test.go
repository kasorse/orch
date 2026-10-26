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

// Scenario 21 (TASKS.md T14): after the fail-fast chain, a real Jev is asked which models
// it serves (GET /v1/models) before anything is decided.

func withModel(model string) func(*configOptions) {
	return func(o *configOptions) { o.decisionModel = model }
}

func assertNoRun(t *testing.T, repoDir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repoDir, ".orchestrator", "runs")); !os.IsNotExist(err) {
		t.Errorf("no run may be created (stat: %v)", err)
	}
}

func TestScenario21_UnservedModelStopsBeforeAnything(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	script := scriptBugfixMedium()
	script.Models = []string{"jev-latest", "jev-fast"}
	j := startJev(t, script)

	_, _, err := runOrchestrator(t, repoDir, jevConfig(t, j, withModel("jev-turbo")), "run", "fix Add in fixture.go")
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Fatalf("expected a ConfigError (exit 2), got %T: %v", err, err)
	}
	for _, want := range []string{`"jev-turbo" is unavailable`, "jev-latest, jev-fast", "decision.model"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	if len(j.Bodies()) != 0 {
		t.Errorf("no decision round may run, Jev got %d", len(j.Bodies()))
	}
	if got := invocations(t, invocationsLog); len(got) != 0 {
		t.Errorf("no CLI may be called (translation included): %v", got)
	}
	assertNoRun(t, repoDir)
}

func TestScenario21_ListedAliasAndPinnedVersionPass(t *testing.T) {
	for _, model := range []string{"jev-latest", "jev-1.13.0"} { // listed; pinned but unlisted
		t.Run(model, func(t *testing.T) {
			repoDir := fakeexec.InitGitFixture(t)
			decisionEnv(t, repoDir)
			script := scriptBugfixMedium()
			script.Models = []string{"jev-latest"}
			j := startJev(t, script)

			stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j, withModel(model)), "run", "fix Add in fixture.go")
			mustSucceed(t, stdout, stderr, err)
			if j.ModelRequests() != 1 {
				t.Errorf("model requests = %d, want 1", j.ModelRequests())
			}
		})
	}
}

func TestScenario21_UnreachableModelsList(t *testing.T) {
	t.Run("on_failure ask warns and goes on", func(t *testing.T) {
		repoDir := fakeexec.InitGitFixture(t)
		decisionEnv(t, repoDir)
		script := scriptBugfixMedium()
		script.ModelsStatus = 503
		j := startJev(t, script)

		stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j, func(o *configOptions) { o.onFailure = "ask" }), "run", "fix Add in fixture.go")
		mustSucceed(t, stdout, stderr, err)
		if !strings.Contains(stderr, "Warning: could not check decision.model with Jev") {
			t.Errorf("expected the warning on stderr:\n%s", stderr)
		}
		if len(j.Bodies()) == 0 {
			t.Error("the decision rounds must still run")
		}
	})
	t.Run("on_failure fail stops", func(t *testing.T) {
		repoDir := fakeexec.InitGitFixture(t)
		invocationsLog, _ := decisionEnv(t, repoDir)
		script := scriptBugfixMedium()
		script.ModelsStatus = 503
		j := startJev(t, script)

		_, _, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "fix Add in fixture.go")
		var derr *apperr.DecisionError
		if !errors.As(err, &derr) || derr.Kind != apperr.DecisionUnavailable || exitcode.FromError(err) != exitcode.ProviderExecution {
			t.Fatalf("expected an unavailable DecisionError (exit 3), got %T: %v", err, err)
		}
		if j.ModelRequests() != 2 {
			t.Errorf("model requests = %d, want 2 (max_retries: 1)", j.ModelRequests())
		}
		if got := invocations(t, invocationsLog); len(got) != 0 {
			t.Errorf("no CLI may be called: %v", got)
		}
		assertNoRun(t, repoDir)
	})
}

func TestScenario21_ValidateReportsTheModel(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "validate")
	mustSucceed(t, stdout, stderr, err)
	if !strings.Contains(stdout, "✓ jev serves jev-1.13.0") {
		t.Errorf("validate must report the model:\n%s", stdout)
	}
	if j.ModelRequests() != 1 || len(j.Bodies()) != 0 {
		t.Errorf("validate: %d model and %d decision requests, want 1 and 0", j.ModelRequests(), len(j.Bodies()))
	}

	_, _, err = runOrchestrator(t, repoDir, jevConfig(t, j, withModel("jev-turbo")), "validate")
	if exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Errorf("validate with an unserved model: exit %d (%v), want 2", exitcode.FromError(err), err)
	}
}

// A --stage run does not check the model and decides nothing; the one thing it asks Jev is the
// clarity question (specs/clarify), recorded beside the manual decision.
func TestScenario21_StageRunAsksOnlyTheClarityQuestion(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j, withClarify("claude")), "run", "--stage", "investigation", "explain Add")
	mustSucceed(t, stdout, stderr, err)
	if bodies := j.Bodies(); j.ModelRequests() != 0 || len(bodies) != 1 || !clarityOnly(bodies[0]) {
		t.Errorf("run --stage contacted Jev: %d model requests, decision requests %v; want the clarity question alone", j.ModelRequests(), bodies)
	}

	d := readDecision(t, repoDir)
	if d.Source != "manual" || d.Provider != "" {
		t.Errorf("the decision must stay manual: %+v", d)
	}
	if d.Clarity == nil || len(d.Clarity.Checks) != 1 || d.Clarity.Checks[0] != 0.95 || d.Clarity.Status != "clear" {
		t.Errorf("decision.json clarity = %+v, want one check of 0.95, clear", d.Clarity)
	}
}

// With the clarify step off, an unclear task changes nothing in a --stage run: the stages are the
// operator's, and the trace says the check was disabled.
func TestScenario21_StageRunWithAnUnclearTaskAndClarifyOffRunsAsBefore(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	script := scriptBugfixMedium()
	unclear := 0.42
	script.PClear = &unclear
	j := startJev(t, script)

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "--stage", "investigation", "explain it")
	mustSucceed(t, stdout, stderr, err)
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"investigate"}) {
		t.Errorf("invocations = %v, want only the stage", got)
	}
	d := readDecision(t, repoDir)
	if d.Source != "manual" || !equalStrings(d.Workflow, []string{"investigate"}) {
		t.Errorf("decision = %+v", d)
	}
	if d.Clarity == nil || d.Clarity.Status != "disabled" || len(d.Clarity.Checks) != 1 || d.Clarity.Checks[0] != 0.42 {
		t.Errorf("decision.json clarity = %+v, want one disabled check of 0.42", d.Clarity)
	}
}
