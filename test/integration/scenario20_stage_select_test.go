package integration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 20 (specs/stage-select/SPEC.md §16): run --stage names the stages and skips the
// whole decision layer. The fake Jev and the translator must see no request at all.

// stageRun is a scenario-20 setup: fake providers, a fake Jev that must stay unused, and
// a config pointing at it.
type stageRun struct {
	repoDir, cfg, invocationsLog, promptsDir string
	jev                                      *fakeJev
	// remote is the bare repository standing in for GitLab, set by newWorktreeRun.
	remote string
}

func newStageRun(t *testing.T, repoDir string, mutate ...func(*configOptions)) stageRun {
	t.Helper()
	invocationsLog, promptsDir := decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	return stageRun{repoDir: repoDir, cfg: jevConfig(t, j, mutate...), invocationsLog: invocationsLog, promptsDir: promptsDir, jev: j}
}

// clarityOnly reports whether a Jev request body asks the clarity question alone
// (specs/clarify): the one thing a --stage run asks the decision provider.
func clarityOnly(body string) bool {
	var req struct {
		Questions map[string]json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return false
	}
	_, ok := req.Questions["clarity"]
	return ok && len(req.Questions) == 1
}

// noDecisionLayer asserts the decision layer did not run: Jev was asked the clarity question
// and nothing else, the translator was not called, and no project context was collected.
func (r stageRun) noDecisionLayer(t *testing.T) {
	t.Helper()
	bodies := r.jev.Bodies()
	if len(bodies) != 1 || !clarityOnly(bodies[0]) {
		t.Errorf("Jev got %d requests %v, want the clarity question alone, once", len(bodies), bodies)
	}
	for _, name := range invocations(t, r.invocationsLog) {
		if strings.HasPrefix(name, "translate") || strings.HasPrefix(name, "decide") {
			t.Errorf("decision-layer call %q", name)
		}
	}
	if _, ok := runFile(t, r.repoDir, "project_context.json"); ok {
		t.Error("project_context.json was written for a manual run")
	}
}

// fixedDirty replaces the fixture with the fixed source: a hand-made change that makes the
// tests pass.
func fixedDirty(t *testing.T, repoDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repoDir, "fixture.go"), []byte(fakeexec.FixedFixtureSource), 0o644); err != nil {
		t.Fatal(err)
	}
}

func executedActions(run resumedRun) []string {
	var got []string
	for _, e := range run.Executions {
		got = append(got, e.Action)
	}
	return got
}

// a: review alone on a dirty tree: one provider call, no decision layer.
func TestScenario20a_ReviewOnDirtyTree(t *testing.T) {
	r := newStageRun(t, dirtyFixture(t))

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--stage", "review", "review my change")
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"review"}) {
		t.Errorf("invocations = %v, want [review]", got)
	}
	r.noDecisionLayer(t)
	d := readDecision(t, r.repoDir)
	if d.Source != "manual" || d.Provider != "" || d.Request != "review my change" || !equalStrings(d.Workflow, []string{"review"}) {
		t.Errorf("decision.json = %+v", d)
	}
	for _, want := range []string{"Stages (manual): review", "Run completed successfully."} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Deciding the workflow") || strings.Contains(stdout, "Translating") {
		t.Errorf("the decision layer was announced:\n%s", stdout)
	}
	if run := loadResumedRun(t, r.repoDir); run.Status != "completed" {
		t.Errorf("status = %s", run.Status)
	}
}

// b: a review of nothing fails before the model is called.
func TestScenario20b_ReviewOfNothingFails(t *testing.T) {
	r := newStageRun(t, fakeexec.InitGitFixture(t))

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--stage", "review", "review my change")
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) || !strings.Contains(err.Error(), "nothing to review") {
		t.Fatalf("expected 'nothing to review', got %T: %v\nstdout:\n%s\nstderr:\n%s", err, err, stdout, stderr)
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("the model was called: %v", got)
	}
	if run := loadResumedRun(t, r.repoDir); run.Status != "failed" {
		t.Errorf("status = %s, want failed", run.Status)
	}
}

// c: the flag order is irrelevant; test failure fails the run, review failure completes it.
func TestScenario20c_TestingThenReview(t *testing.T) {
	t.Run("passes", func(t *testing.T) {
		repoDir := fakeexec.InitGitFixture(t)
		fixedDirty(t, repoDir)
		r := newStageRun(t, repoDir)
		stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "review", "--stage", "testing", "check it")
		mustSucceed(t, stdout, stderr, err)
		run := loadResumedRun(t, repoDir)
		if got := executedActions(run); !equalStrings(got, []string{"test", "review"}) {
			t.Errorf("executions = %v, want test then review", got)
		}
		if !strings.Contains(stdout, "Stages (manual): testing → review") {
			t.Errorf("stdout:\n%s", stdout)
		}
		r.noDecisionLayer(t)
	})

	t.Run("test failure fails the run", func(t *testing.T) {
		repoDir := dirtyFixture(t) // the fixture's bug is still there
		r := newStageRun(t, repoDir)
		_, _, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "testing", "--stage", "review", "check it")
		if err == nil {
			t.Fatal("expected the failing tests to fail the run")
		}
		run := loadResumedRun(t, repoDir)
		if run.Status != "failed" || !equalStrings(executedActions(run), []string{"test"}) {
			t.Errorf("status = %s, executions = %v", run.Status, executedActions(run))
		}
		if got := invocations(t, r.invocationsLog); len(got) != 0 {
			t.Errorf("no provider call expected, got %v", got)
		}
	})

	t.Run("review failure completes with the verdict", func(t *testing.T) {
		repoDir := fakeexec.InitGitFixture(t)
		fixedDirty(t, repoDir)
		r := newStageRun(t, repoDir)
		writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "review", reviewFailResponse)
		stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "testing", "--stage", "review", "check it")
		mustSucceed(t, stdout, stderr, err)
		run := loadResumedRun(t, repoDir)
		if got := executedActions(run); !equalStrings(got, []string{"test", "review"}) {
			t.Errorf("executions = %v: no implement without development\nstdout:\n%s\nstderr:\n%s", got, stdout, stderr)
		}
		if run.Status != "completed" {
			t.Errorf("status = %s, want completed", run.Status)
		}
		if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"review"}) {
			t.Errorf("invocations = %v", got)
		}
	})
}

// d: development needs a clean tree; a refused run is resumable once it is clean.
func TestScenario20d_DevelopmentRefusedOnDirtyTree(t *testing.T) {
	r := newStageRun(t, dirtyFixture(t))

	_, _, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--stage", "implement", "fix Add in fixture.go")
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("expected the dirty-tree refusal, got %T: %v", err, err)
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Fatalf("no step may run, got %v", got)
	}
	refused := loadResumedRun(t, r.repoDir)
	if refused.Status != "failed" || refused.Workflow == nil || len(refused.Workflow.Steps) != 1 {
		t.Fatalf("a refused run keeps its workflow: %+v", refused)
	}

	gitRun(t, r.repoDir, "add", "notes.txt")
	gitRun(t, r.repoDir, "commit", "-qm", "notes")
	stdout, stderr, err := resume(t, r.repoDir, r.cfg, refused.ID)
	mustSucceed(t, stdout, stderr, err)
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations = %v, want [implement]", got)
	}
	r.noDecisionLayer(t)
}

// e: with development the existing fix loop applies, bounded by max_fix_iterations.
func TestScenario20e_FixLoop(t *testing.T) {
	t.Run("fixes on the second implement", func(t *testing.T) {
		r := newStageRun(t, fakeexec.InitGitFixture(t))
		t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", "2")
		stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run",
			"--stage", "review", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
		mustSucceed(t, stdout, stderr, err)
		if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "implement", "review"}) {
			t.Errorf("invocations = %v", got)
		}
		r.noDecisionLayer(t)
	})
	t.Run("bounded", func(t *testing.T) {
		r := newStageRun(t, fakeexec.InitGitFixture(t), func(o *configOptions) { o.maxFixIterations = 1 })
		t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", "999")
		_, _, err := runOrchestrator(t, r.repoDir, r.cfg, "run",
			"--stage", "development", "--stage", "testing", "--stage", "review", "fix Add in fixture.go")
		var limitErr *apperr.LimitExceededError
		if !errors.As(err, &limitErr) {
			t.Fatalf("expected the fix-iteration limit, got %T: %v", err, err)
		}
		if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "implement"}) {
			t.Errorf("invocations = %v", got)
		}
	})
}

// f: investigation alone: the investigation is the answer, and nothing is missing before it.
func TestScenario20f_InvestigationAlone(t *testing.T) {
	r := newStageRun(t, fakeexec.InitGitFixture(t))

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--stage", "investigate", "investigate the fix")
	mustSucceed(t, stdout, stderr, err)
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"investigate"}) {
		t.Errorf("invocations = %v", got)
	}
	if strings.Contains(stderr, "runs without") {
		t.Errorf("stderr has a gap warning for investigation alone:\n%s", stderr)
	}
	if !strings.Contains(stdout, "fix the operator") {
		t.Errorf("stdout lacks the investigation as the answer:\n%s", stdout)
	}
	r.noDecisionLayer(t)
}

// g: the same stage by both spellings is one step.
func TestScenario20g_AliasesCollapse(t *testing.T) {
	r := newStageRun(t, fakeexec.InitGitFixture(t))

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--stage", "investigate", "--stage", "investigation", "explain Add")
	mustSucceed(t, stdout, stderr, err)
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"investigate"}) {
		t.Errorf("invocations = %v", got)
	}
	if n := strings.Count(stdout, "Stages (manual): investigation"); n != 1 {
		t.Errorf("the stage line appears %d times:\n%s", n, stdout)
	}
}

// g2: analysis and analyze are not stages any more, and plan and planning became investigate
// and investigation (specs/clarify): --stage refuses all four by name, before any call.
func TestScenario20g2_RemovedStagesAreRefused(t *testing.T) {
	r := newStageRun(t, fakeexec.InitGitFixture(t))

	for _, name := range []string{"analysis", "analyze", "planning", "plan"} {
		_, _, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--stage", name, "explain Add")
		if err == nil || !strings.Contains(err.Error(), "specs/clarify") {
			t.Errorf("--stage %s: err = %v, want a refusal naming specs/clarify", name, err)
		}
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("a refused stage called %v", got)
	}
}

// h: --jira gives the task; the only request is the Jira GET.
func TestScenario20h_StageWithJira(t *testing.T) {
	r := newJiraRun(t)
	if err := os.WriteFile(filepath.Join(r.repoDir, "notes.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--stage", "review", "--jira", "PMC-1")
	mustSucceed(t, stdout, stderr, err)
	if n := len(r.jira.Requests()); n != 1 {
		t.Errorf("Jira got %d requests, want 1", n)
	}
	if bodies := r.jev.Bodies(); len(bodies) != 1 || !clarityOnly(bodies[0]) {
		t.Errorf("Jev got %d requests, want the clarity question alone, once", len(bodies))
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"review"}) {
		t.Errorf("invocations = %v", got)
	}
	if ts := loadTaskSource(t, r.repoDir); ts.Task.Source == nil || ts.Task.Source.Key != "PMC-1" {
		t.Errorf("task.source = %+v", ts.Task.Source)
	}
}

// i: resume continues a manual run at the step that failed and decides nothing.
func TestScenario20i_ResumeManualRun(t *testing.T) {
	r := newStageRun(t, dirtyFixture(t))

	t.Setenv("FAKE_FAIL_ACTION", "review")
	if _, _, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--stage", "review", "review my change"); err == nil {
		t.Fatal("expected the review to fail the run")
	}
	t.Setenv("FAKE_FAIL_ACTION", "")
	failed := loadResumedRun(t, r.repoDir)
	if failed.Status != "failed" {
		t.Fatalf("status = %s", failed.Status)
	}

	stdout, stderr, err := resume(t, r.repoDir, r.cfg, failed.ID)
	mustSucceed(t, stdout, stderr, err)
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"review", "review"}) {
		t.Errorf("invocations = %v, want review twice", got)
	}
	run := loadResumedRun(t, r.repoDir)
	if run.Status != "completed" || len(run.Resumes) != 1 || run.Resumes[0].Entry != "review" {
		t.Errorf("status = %s, resumes = %+v", run.Status, run.Resumes)
	}
	r.noDecisionLayer(t)
}

// j: a missing executable fails before anything is called; --stage belongs to run alone.
func TestScenario20j_FailFast(t *testing.T) {
	t.Run("missing codex for review", func(t *testing.T) {
		repoDir := dirtyFixture(t)
		installFakeProviders(t, "claude", "gemini")
		invocationsLog := filepath.Join(t.TempDir(), "invocations.log")
		t.Setenv("INVOCATIONS_LOG", invocationsLog)
		t.Setenv("JEV_API_KEY", jevKey)
		opts := defaultConfigOptions()
		opts.providers.Review = "codex"
		cfg := writeConfig(t, t.TempDir(), opts)

		_, _, err := runOrchestrator(t, repoDir, cfg, "run", "--stage", "review", "review my change")
		var depErr *apperr.DependencyError
		if !errors.As(err, &depErr) {
			t.Fatalf("expected a DependencyError, got %T: %v", err, err)
		}
		if got := invocations(t, invocationsLog); len(got) != 0 {
			t.Errorf("calls before the fail-fast chain finished: %v", got)
		}
	})
	t.Run("validate with --stage", func(t *testing.T) {
		_, _, err := runOrchestrator(t, t.TempDir(), "/nonexistent.yaml", "validate", "--stage", "review")
		var cfgErr *apperr.ConfigError
		if !errors.As(err, &cfgErr) || err.Error() != "--stage is only valid with run" {
			t.Fatalf("got %T: %v", err, err)
		}
	})
}

// k: plan mode shows the stages and authorities, then stops without calling anything.
func TestScenario20k_PlanMode(t *testing.T) {
	r := newStageRun(t, dirtyFixture(t))

	stdout, stderr, err := runOrchestratorInteractive(t, r.repoDir, r.cfg, "a\n", "--mode", "plan", "run", "--stage", "review", "review my change")
	mustSucceed(t, stdout, stderr, err)
	for _, want := range []string{"Stages (manual): review", "authority: review=read", "Not executing (plan mode)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("plan mode called %v", got)
	}
	r.noDecisionLayer(t)
}
