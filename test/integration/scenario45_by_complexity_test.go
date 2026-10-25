package integration

import (
	"encoding/json"
	"os"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 45, by_complexity (specs/quality/SPEC.md §9): a static table in the config picks the
// model of an action from the decided complexity. A simple decision runs implement on the
// table's model, a run with --stage has no complexity and runs on the base model, and a level
// the table lacks runs on the base model too. decision.json shows the route before any step
// runs, and each execution records where its model came from.

// haikuForSimple routes a simple decision's implement to its own model.
func haikuForSimple(o *configOptions) {
	o.byComplexity = map[string]map[string]string{"implement": {"simple": "haiku-fake"}}
}

// decisionModels is the part of decision.json that routes models.
type decisionModels struct {
	Models map[string]struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Source   string `json:"source"`
	} `json:"models"`
}

func readDecisionModels(t *testing.T, repoDir string) decisionModels {
	t.Helper()
	var d decisionModels
	if err := json.Unmarshal([]byte(mustRunFile(t, repoDir, "decision.json")), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestScenario45_SimpleDecisionUsesTheTablesModel(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	argsDir := t.TempDir()
	t.Setenv("FAKE_ARGS_DIR", argsDir)
	// research has no by_complexity entry, so it shows the base model next to implement's table
	// model: a simple decision has no template, and review is not part of it.
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "research", readOnlyResponse)
	script := scriptBugfixLow()
	script.Stages["research"] = 0.9
	j := startJev(t, script)

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j, haikuForSimple), "run", "fix Add() to add instead of subtract")
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"research", "implement"}) {
		t.Fatalf("invocations = %v", got)
	}
	if d := readDecision(t, repoDir); d.Complexity.Selected != "simple" {
		t.Fatalf("the decision's complexity = %q, want simple", d.Complexity.Selected)
	}
	implementArgs := readFile(t, argsDir+"/implement.args")
	researchArgs := readFile(t, argsDir+"/research.args")
	assertContains(t, "implement's argv", implementArgs, "--model\nhaiku-fake\n")
	assertContains(t, "research's argv", researchArgs, "--model\nfake\n")

	// decision.json routes every action of the workflow (not the test step).
	models := readDecisionModels(t, repoDir).Models
	if len(models) != 2 {
		t.Fatalf("decision.json models = %+v, want research and implement", models)
	}
	if m := models["implement"]; m.Provider != "claude" || m.Model != "haiku-fake" || m.Source != "by_complexity.simple" {
		t.Errorf("models[implement] = %+v", m)
	}
	if m := models["research"]; m.Model != "fake" || m.Source != "base" {
		t.Errorf("models[research] = %+v", m)
	}

	run := loadQL(t, repoDir)
	if p := run.ofAction("implement")[0].Provider; p == nil || p.Model != "haiku-fake" || p.ModelSource != "by_complexity.simple" {
		t.Errorf("implement's provider = %+v", p)
	}
	if p := run.ofAction("research")[0].Provider; p == nil || p.Model != "fake" || p.ModelSource != "base" {
		t.Errorf("research's provider = %+v", p)
	}
}

func TestScenario45_ManualStagesUseTheBaseModel(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, haikuForSimple)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	if got := argAfter(r.args(t, "implement"), "--model"); got != "fake" {
		t.Errorf("implement ran on model %q, want the base model fake", got)
	}
	models := readDecisionModels(t, repoDir).Models
	if m := models["implement"]; m.Model != "fake" || m.Source != "base" {
		t.Errorf("models[implement] = %+v", m)
	}
	if p := loadQL(t, repoDir).ofAction("implement")[0].Provider; p == nil || p.ModelSource != "base" {
		t.Errorf("implement's provider = %+v", p)
	}
}

func TestScenario45_LevelWithoutAnEntryUsesTheBaseModel(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	argsDir := t.TempDir()
	t.Setenv("FAKE_ARGS_DIR", argsDir)
	j := startJev(t, scriptBugfixMedium())

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j, haikuForSimple), "run", "fix Add() to add instead of subtract")
	mustSucceed(t, stdout, stderr, err)

	if d := readDecision(t, repoDir); d.Complexity.Selected != "medium" {
		t.Fatalf("the decision's complexity = %q, want medium", d.Complexity.Selected)
	}
	assertContains(t, "implement's argv", readFile(t, argsDir+"/implement.args"), "--model\nfake\n")
	if m := readDecisionModels(t, repoDir).Models["implement"]; m.Model != "fake" || m.Source != "base" {
		t.Errorf("models[implement] = %+v", m)
	}
}
