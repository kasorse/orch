package integration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
)

// The scenarios of specs/clarify CL08 that clarify_test.go does not cover: an operator at a
// terminal, instructions that answer the question, a stage run on development, a story task and
// the old names in a config. They share clarifyEnv and clarifyConfig.

// exportDoc is a repository document the clarify call can answer from: the heading and the words
// of the request below make it the excerpt Go selects.
const exportDoc = "# Export\n\n## Formats\n\nThe export is CSV; JSON is not supported.\n"

const exportRequest = "add the export button: fix Add in fixture.go"

// commitDoc commits docs/export.md, so git lists it and the worktree stays clean.
func commitDoc(t *testing.T, repoDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repoDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "docs", "export.md"), []byte(exportDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, repoDir, "add the export doc")
}

// An operator at a terminal answers the points the model could not settle; what was agreed reaches
// the investigate prompt, and nothing was assumed (interactive mode has no assumptions).
func TestClarify_OperatorAtATerminalClarifiesAndInvestigateHoldsTheAnswer(t *testing.T) {
	repoDir, invocationsLog, promptsDir, _ := clarifyEnv(t, 0.42, 0.9)
	commitDoc(t, repoDir)
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "clarify", `{
  "points": [
    {"unclear": "export format", "question": "Which export format is meant?", "answer": "CSV", "basis": "docs", "source": "docs/export.md#Formats"},
    {"unclear": "who gets the button", "question": "Which users get the button?", "basis": "none"}
  ],
  "summary_short": "two points"
}`)

	// Enter keeps the docs' answer, the second point is answered, then the workflow is approved
	// and the mode is auto.
	stdin := "\nadmins only\na\n4\n"
	stdout, stderr, err := runOrchestratorInteractive(t, repoDir, clarifyConfig(t), stdin, "run", exportRequest)
	mustSucceed(t, stdout, stderr, err)

	want := []string{"decide-context", "clarify", "decide-clarity", "decide-classify", "investigate", "implement", "review"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v", got, want)
	}
	assertContains(t, "stdout", stdout, "The task is not clear enough (P(clear) 0.42 < 0.80). 2 questions:", "Which users get the button?")

	c := readClarifyRun(t, repoDir).Task.Clarification
	if c == nil || c.Status != "clear" || len(c.Rounds) != 1 || c.Rounds[0].Mode != "interactive" || len(c.Rounds[0].Points) != 2 {
		t.Fatalf("task.clarification = %+v, want the one interactive round of two points, clear", c)
	}
	points := c.Rounds[0].Points
	if points[0].By != "docs" || points[0].Answer != "CSV" || points[0].Source != "docs/export.md#Formats" {
		t.Errorf("point 0 = %+v, want the docs' answer kept", points[0])
	}
	if points[1].By != "operator" || points[1].Answer != "admins only" {
		t.Errorf("point 1 = %+v, want the operator's answer", points[1])
	}

	// The clarify call was given the excerpt Go chose; the investigate prompt holds the agreement.
	assertContains(t, "the clarify prompt", readFile(t, filepath.Join(promptsDir, "clarify.txt")),
		"source: docs/export.md#Formats", "JSON is not supported")
	assertContains(t, "the investigate prompt", readFile(t, filepath.Join(promptsDir, "investigate.txt")),
		"Which export format is meant? CSV  [from docs/export.md#Formats]", "Which users get the button? admins only  [operator]")
}

// --mode auto with instructions that answer the question goes on; the model's answers are taken as
// they are and nothing is asked.
func TestClarify_AutoWithInstructionsThatAnswerGoesOn(t *testing.T) {
	repoDir, invocationsLog, promptsDir, _ := clarifyEnv(t, 0.42, 0.9)
	commitDoc(t, repoDir)
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "clarify", `{
  "points": [
    {"unclear": "export format", "question": "Which export format is meant?", "answer": "CSV", "basis": "docs", "source": "docs/export.md#Formats"}
  ],
  "summary_short": "one point"
}`)

	stdout, stderr, err := runOrchestrator(t, repoDir, clarifyConfig(t), "run", exportRequest)
	mustSucceed(t, stdout, stderr, err)

	want := []string{"decide-context", "clarify", "decide-clarity", "decide-classify", "investigate", "implement", "review"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v", got, want)
	}
	assertContains(t, "the clarify prompt", readFile(t, filepath.Join(promptsDir, "clarify.txt")), "source: docs/export.md#Formats")
	investigate := readFile(t, filepath.Join(promptsDir, "investigate.txt"))
	assertContains(t, "the investigate prompt", investigate, "Which export format is meant? CSV  [from docs/export.md#Formats]")
	if strings.Contains(investigate, "Open:") {
		t.Errorf("the investigate prompt has an open point:\n%s", investigate)
	}
}

// --mode auto with no instructions that answer anything: every point stays open, the re-check
// still rates the task unclear and the run stops with exit 4 before any decision.
func TestClarify_AutoWithoutInstructionsThatAnswerStopsWithExit4(t *testing.T) {
	repoDir, invocationsLog, promptsDir, _ := clarifyEnv(t, 0.42, 0.45)
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "clarify", `{
  "points": [
    {"unclear": "export format", "question": "Which export format is meant?", "basis": "none"},
    {"unclear": "who gets the button", "question": "Which users get the button?", "basis": "none"}
  ],
  "summary_short": "nothing settled"
}`)

	_, _, err := runOrchestrator(t, repoDir, clarifyConfig(t), "run", exportRequest)
	if err == nil || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("exit %d (%v), want 4 for a task that needs input", exitcode.FromError(err), err)
	}
	for _, want := range []string{"task needs input", "Which export format is meant?", "Which users get the button?", "0.45 < 0.80"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q:\n%v", want, err)
		}
	}
	assertContains(t, "the clarify prompt", readFile(t, filepath.Join(promptsDir, "clarify.txt")),
		"nothing can be answered from docs")
	if got, want := invocations(t, invocationsLog), []string{"decide-context", "clarify", "decide-clarity"}; !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v: no stage runs for a task that needs input", got, want)
	}
	run := readClarifyRun(t, repoDir)
	if run.Status != "stopped" || run.FailureReason != "task needs input" || run.Decision != nil || run.Workflow != nil {
		t.Errorf("run = status %q, reason %q, decision %v, workflow %v; want stopped before any workflow", run.Status, run.FailureReason, run.Decision, run.Workflow)
	}
}

// run --stage development on an unclear task runs the clarify step too; the stages are the
// operator's.
func TestClarify_StageDevelopmentOnAnUnclearTaskRunsClarify(t *testing.T) {
	repoDir, invocationsLog, _, _ := clarifyEnv(t, 0.5, 0.3, 0.9)

	stdout, stderr, err := runOrchestrator(t, repoDir, clarifyConfig(t), "run", "--stage", "development", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	got := invocations(t, invocationsLog)
	want := []string{"decide-clarity", "clarify", "decide-clarity", "implement"}
	if len(got) < len(want) || !equalStrings(got[:len(want)], want) {
		t.Errorf("invocations = %v, want them to start with %v", got, want)
	}
	if d := readDecision(t, repoDir); d.Source != "manual" || d.Clarity == nil || d.Clarity.Status != "clear" {
		t.Errorf("decision = %+v clarity %+v, want manual, clear", d, d.Clarity)
	}
}

// A story task never calls clarify, whatever the config says, and records no clarity: the task file
// is the plan. (The pre-check's one call still carries the question; its answer is not used.)
func TestClarify_StoryTaskNeverCallsClarify(t *testing.T) {
	r := newStoryRun(t)
	cfg := jevConfig(t, r.jev, withClarify("claude"))

	stdout, stderr, err := runOrchestrator(t, r.repoDir, cfg, "run", "--story-task", r.input)
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "review"}) {
		t.Errorf("invocations = %v, want implement and review only", got)
	}
	if run := readClarifyRun(t, r.repoDir); run.Task.Clarification != nil {
		t.Errorf("run.json has task.clarification %+v for a story task", run.Task.Clarification)
	}
	if d := readDecision(t, r.repoDir); d.Clarity != nil {
		t.Errorf("decision.json has clarity %+v for a story task", d.Clarity)
	}
}

// A config that still names analysis or planning fails before any call and creates no run, with a
// message naming the replacement and specs/clarify (D9).
func TestClarify_OldStageNamesInTheConfigFailBeforeAnyCall(t *testing.T) {
	const (
		thresholds = "thresholds: {research: 0.60, development: 0.60}"
		force      = "medium: [investigation, review]"
	)
	tests := []struct {
		name        string
		old, new    string
		wantMessage []string
	}{
		{"analysis threshold", thresholds, "thresholds: {research: 0.60, analysis: 0.60, development: 0.60}",
			[]string{"policy.stages.thresholds.analysis", "policy.clarify.threshold", "specs/clarify"}},
		{"planning in the template", force, "medium: [planning, review]",
			[]string{"policy.stages.force.medium", "planning was renamed investigation", "specs/clarify"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repoDir, invocationsLog, _, _ := clarifyEnv(t, 0.9)
			cfg := clarifyConfig(t)
			data, err := os.ReadFile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			edited := strings.Replace(string(data), tc.old, tc.new, 1)
			if edited == string(data) {
				t.Fatalf("the config has no %q to replace:\n%s", tc.old, data)
			}
			if err := os.WriteFile(cfg, []byte(edited), 0o644); err != nil {
				t.Fatal(err)
			}

			_, _, err = runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
			var cfgErr *apperr.ConfigError
			if !errors.As(err, &cfgErr) {
				t.Fatalf("err = %T %v, want a config error", err, err)
			}
			for _, want := range tc.wantMessage {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error lacks %q:\n%v", want, err)
				}
			}
			if got := invocations(t, invocationsLog); len(got) != 0 {
				t.Errorf("a refused config called %v", got)
			}
			noRunDirectory(t, repoDir)
		})
	}
}
