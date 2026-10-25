package integration

import (
	"errors"
	"os"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenarios 57-62, the decision shape (specs/decision-shape/SPEC.md): the level is cut from one
// size number, two stages are asked and the rest come from the level's template, a fix after
// a failed test runs one level up, and decisions calibrate proposes the cuts. The integration
// config cuts at 0.5 and 1.4, and its template is medium: [planning, review], complex:
// [planning, review].

// decisionStage is the stage of decision.json named st.
func decisionStage(t *testing.T, d decisionJSON, st string) (enabled bool, reason string) {
	t.Helper()
	for _, s := range d.Stages {
		if s.Stage == st {
			return s.Enabled, s.Reason
		}
	}
	t.Fatalf("decision.json has no stage %s", st)
	return false, ""
}

// 57. A simple size has no template, so a change is implement and, with it, testing, which comes
// with development without being asked.
func TestScenario57_SimpleSizeNoTemplate(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, startJev(t, scriptBugfixLow())), "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations = %v", got)
	}
	d := readDecision(t, repoDir)
	if d.Complexity.Selected != "simple" || !equalStrings(d.Workflow, []string{"implement", "test"}) {
		t.Errorf("complexity %s, workflow %v", d.Complexity.Selected, d.Workflow)
	}
	if on, reason := decisionStage(t, d, "testing"); !on || reason != "with development" {
		t.Errorf("testing = %v, %q", on, reason)
	}
	if on, reason := decisionStage(t, d, "review"); on || reason != "not asked, not in the template" {
		t.Errorf("review = %v, %q", on, reason)
	}
}

// 58. A medium size gets the template (planning and review), and implement runs on the medium
// model.
func TestScenario58_MediumSizeTemplateAndModel(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	argsDir := t.TempDir()
	t.Setenv("FAKE_ARGS_DIR", argsDir)
	cfg := jevConfig(t, startJev(t, scriptBugfixMedium()), func(o *configOptions) {
		o.byComplexity = map[string]map[string]string{"implement": {"medium": "medium-fake"}}
	})

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"plan", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
	d := readDecision(t, repoDir)
	if on, reason := decisionStage(t, d, "planning"); !on || reason != "forced: complexity=medium" {
		t.Errorf("planning = %v, %q", on, reason)
	}
	assertContains(t, "implement's argv", readFile(t, argsDir+"/implement.args"), "--model\nmedium-fake\n")
	if p := loadQL(t, repoDir).ofAction("implement")[0].Provider; p == nil || p.ModelSource != "by_complexity.medium" {
		t.Errorf("implement's provider = %+v", p)
	}
}

// 59. No code change: the run only answers, with the template's planning as its last read-only
// step. Medium's review is dropped, and the answer is printed.
func TestScenario59_NoCodeChangeAnswersOnly(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "plan", readOnlyAnswerResponse)
	script := scriptBugfixMedium()
	script.Stages = map[string]float64{"development": 0.1}

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, startJev(t, script)), "run", "explain Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"plan"}) {
		t.Errorf("invocations = %v", got)
	}
	if !strings.Contains(stdout, "Answer:\nAdd returns a - b; it should return a + b.") {
		t.Errorf("the answer is not printed:\n%s", stdout)
	}
	d := readDecision(t, repoDir)
	if d.Complexity.Selected != "medium" {
		t.Errorf("complexity = %s, want medium", d.Complexity.Selected)
	}
	if on, reason := decisionStage(t, d, "review"); on || reason != "dropped: no code change" {
		t.Errorf("review = %v, %q", on, reason)
	}
	if on, _ := decisionStage(t, d, "testing"); on {
		t.Error("testing is on without development")
	}
}

// 60. A config written for the old decision fails before any call, naming what replaced it.
func TestScenario60_OldDecisionKeysFailFast(t *testing.T) {
	for name, tc := range map[string]struct{ old, new, want string }{
		"complexity thresholds": {"cuts: {medium: 0.50, complex: 1.40}", "complex: 0.55\n    medium: 0.70",
			"policy.complexity.medium was replaced by policy.complexity.cuts"},
		"a stage no longer asked": {"development: 0.60}", "development: 0.60, review: 0.70}",
			"policy.stages.thresholds.review: review is not asked any more"},
	} {
		t.Run(name, func(t *testing.T) {
			repoDir := fakeexec.InitGitFixture(t)
			invocationsLog, _ := decisionEnv(t, repoDir)
			j := startJev(t, scriptBugfixMedium())
			cfg := jevConfig(t, j)
			content := readFile(t, cfg)
			if !strings.Contains(content, tc.old) {
				t.Fatalf("the config has no %q", tc.old)
			}
			if err := os.WriteFile(cfg, []byte(strings.Replace(content, tc.old, tc.new, 1)), 0o644); err != nil {
				t.Fatal(err)
			}

			_, _, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
			if code := exitcode.FromError(err); code != exitcode.ConfigOrDependency || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v (exit %d), want a config error containing %q", err, code, tc.want)
			}
			if len(j.Bodies()) != 0 || len(invocations(t, invocationsLog)) != 0 {
				t.Errorf("calls were made: %d Jev requests, invocations %v", len(j.Bodies()), invocations(t, invocationsLog))
			}
		})
	}
}

// 61. escalate: the first implement (simple model) leaves the test failing; the run stops at
// the step limit, and the resume's fix runs on the medium model, recorded as escalated.
func TestScenario61_EscalateAFixAcrossAResume(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", "2")
	argsDir := t.TempDir()
	t.Setenv("FAKE_ARGS_DIR", argsDir)
	j := startJev(t, scriptBugfixLow())
	escalating := func(maxSteps int) func(*configOptions) {
		return func(o *configOptions) {
			o.byComplexity = map[string]map[string]string{"implement": {"simple": "simple-fake", "medium": "medium-fake"}}
			o.escalate = true
			o.maxSteps = maxSteps
		}
	}

	_, _, err := runOrchestrator(t, repoDir, jevConfig(t, j, escalating(2)), "run", "fix Add in fixture.go")
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected the step limit after implement and a failed test, got %T: %v", err, err)
	}
	assertContains(t, "the first implement's argv", readFile(t, argsDir+"/implement.args"), "--model\nsimple-fake\n")
	stopped := loadQL(t, repoDir)

	stdout, stderr, err := resume(t, repoDir, jevConfig(t, j, escalating(12)), stopped.ID)
	mustSucceed(t, stdout, stderr, err)

	assertContains(t, "the fix's argv", readFile(t, argsDir+"/implement.args"), "--model\nmedium-fake\n")
	run := loadQL(t, repoDir)
	impl := run.ofAction("implement")
	if len(impl) != 2 || run.Status != "completed" {
		t.Fatalf("status %s, %d implements", run.Status, len(impl))
	}
	if p := impl[0].Provider; p == nil || p.Model != "simple-fake" || p.ModelSource != "by_complexity.simple" {
		t.Errorf("first implement = %+v", p)
	}
	if p := impl[1].Provider; p == nil || p.Model != "medium-fake" || p.ModelSource != "escalated.medium" {
		t.Errorf("the fix = %+v", p)
	}
}

// 63. A level's own escalate: the simple entry names the model of its fix, without escalate:
// true, so the run fixes the failed test on it and records by_complexity.simple.escalate.
func TestScenario63_EscalateWithinTheLevel(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", "2")
	argsDir := t.TempDir()
	t.Setenv("FAKE_ARGS_DIR", argsDir)
	j := startJev(t, scriptBugfixLow())

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j, func(o *configOptions) {
		o.byComplexity = map[string]map[string]string{"implement": {"simple": "simple-fake"}}
		o.escalateTo = map[string]string{"simple": "strong-fake"}
	}), "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	assertContains(t, "the fix's argv", readFile(t, argsDir+"/implement.args"), "--model\nstrong-fake\n")
	run := loadQL(t, repoDir)
	impl := run.ofAction("implement")
	if len(impl) != 2 || run.Status != "completed" {
		t.Fatalf("status %s, %d implements", run.Status, len(impl))
	}
	if p := impl[0].Provider; p == nil || p.Model != "simple-fake" || p.ModelSource != "by_complexity.simple" {
		t.Errorf("first implement = %+v", p)
	}
	if p := impl[1].Provider; p == nil || p.Model != "strong-fake" || p.ModelSource != "by_complexity.simple.escalate" {
		t.Errorf("the fix = %+v", p)
	}
}

// 62. decisions calibrate over three answered runs prints the shape and proposes the cuts for
// the target, as a diff from the cuts the runs were decided with.
func TestScenario62_CalibrateProposesCuts(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "plan", readOnlyAnswerResponse)
	var cfg string
	// Sizes 0.2, 0.6 and 1.2; no code change, so each run only answers.
	for _, choice := range [][3]float64{{0.8, 0.2, 0}, {0.4, 0.6, 0}, {0, 0.8, 0.2}} {
		script := jevScript{PProject: 0.9, Choice: choice}
		cfg = jevConfig(t, startJev(t, script))
		stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", "explain Add in fixture.go")
		mustSucceed(t, stdout, stderr, err)
	}

	stdout, _, err := runOrchestrator(t, repoDir, cfg, "decisions", "calibrate", "--min-runs", "1", "--size-target", "34/33/33")
	if err != nil {
		t.Fatalf("calibrate: %v", err)
	}
	assertContains(t, "the calibration report", stdout,
		"size and stages",
		"levels decided: simple 1, medium 2, complex 0",
		"cuts for 34/33/33: medium 0.40, complex 0.90",
		"-   cuts: {medium: 0.50, complex: 1.40}",
		"+   cuts: {medium: 0.40, complex: 0.90}")
}
