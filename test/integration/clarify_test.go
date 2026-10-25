package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// The clarify loop end to end (specs/clarify CL06): an unclear task is clarified before the
// decision by one call with no tools, re-checked by the decision provider, and either goes on or
// stops with "task needs input". The decision provider is the fake claude CLI, so the re-check
// can be scripted: decide-clarity_<N>.json is the N-th re-check. The tests run with --mode auto,
// which is also what a run without a terminal is.

func withClarify(provider string) func(*configOptions) {
	return func(o *configOptions) { o.providers.Clarify = provider }
}

// clarifyAnswer holds a point of each basis: docs, assumption, and one nothing settles.
const clarifyAnswer = `{
  "points": [
    {"unclear": "export format", "question": "Which export format is meant?", "answer": "CSV", "basis": "docs", "source": "docs/export.md#Formats"},
    {"unclear": "who gets the button", "question": "Which users get the button?", "answer": "admins only", "basis": "assumption"},
    {"unclear": "date locale", "question": "Which locale is the date in?", "basis": "none"}
  ],
  "summary_short": "three points"
}`

// clarifyRun is the part of run.json these scenarios read.
type clarifyRun struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
	Task          struct {
		Clarification *struct {
			Threshold float64   `json:"threshold"`
			Checks    []float64 `json:"checks"`
			Status    string    `json:"status"`
			Rounds    []struct {
				Kind   string `json:"kind"`
				Mode   string `json:"mode"`
				Points []struct {
					Question string `json:"question"`
					Answer   string `json:"answer"`
					By       string `json:"by"`
					Source   string `json:"source"`
				} `json:"points"`
			} `json:"rounds"`
		} `json:"clarification"`
	} `json:"task"`
	Decision *json.RawMessage `json:"decision"`
	Workflow *json.RawMessage `json:"workflow"`
}

func readClarifyRun(t *testing.T, repoDir string) clarifyRun {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var run clarifyRun
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		t.Fatalf("run.json: %v", err)
	}
	return run
}

// clarifyEnv installs the fake CLIs for a run whose decision provider is claude: the pre-check
// rates the task pPre, and the N-th re-check rechecks[N-1]. It returns the repository, the
// invocations log, the directory of the prompts and the directory of the arguments.
func clarifyEnv(t *testing.T, pPre float64, rechecks ...float64) (repoDir, invocationsLog, promptsDir, argsDir string) {
	t.Helper()
	repoDir = fakeexec.InitGitFixture(t)
	invocationsLog, promptsDir = decisionEnv(t, repoDir)
	responsesDir := os.Getenv("FAKE_RESPONSES_DIR")
	writeResponse(t, responsesDir, "decide-context", fmt.Sprintf(`{"context_depth": {"none": 0.1, "project": 0.9}, "clarity": %v}`, pPre))
	writeResponse(t, responsesDir, "decide-classify", `{
  "complexity": {"simple": 0.1, "medium": 0.7, "complex": 0.2},
  "complexity_score": {"0": 0.1, "1": 0.7, "2": 0.2},
  "stages": {"research": 0.05, "development": 0.95},
  "scope": 0.2
}`)
	writeResponse(t, responsesDir, "clarify", clarifyAnswer)
	for i, p := range rechecks {
		writeResponse(t, responsesDir, fmt.Sprintf("decide-clarity_%d", i+1), fmt.Sprintf(`{"clarity": %v}`, p))
	}
	t.Setenv("JEV_API_KEY", "")
	argsDir = t.TempDir()
	t.Setenv("FAKE_ARGS_DIR", argsDir)
	return repoDir, invocationsLog, promptsDir, argsDir
}

func clarifyConfig(t *testing.T, mutate ...func(*configOptions)) string {
	t.Helper()
	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	opts.providers.Clarify = "claude"
	for _, m := range mutate {
		m(&opts)
	}
	return writeConfig(t, t.TempDir(), opts)
}

func TestClarify_ClearTaskMakesNoClarifyCall(t *testing.T) {
	repoDir, invocationsLog, _, _ := clarifyEnv(t, 0.85)

	stdout, stderr, err := runOrchestrator(t, repoDir, clarifyConfig(t), "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	want := []string{"decide-context", "decide-classify", "investigate", "implement", "review"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v", got, want)
	}
	if run := readClarifyRun(t, repoDir); run.Task.Clarification != nil {
		t.Errorf("run.json has task.clarification %+v for a task that was clear at the first check", run.Task.Clarification)
	}
}

func TestClarify_AutoTakesDocsAndAssumptionsAndLeavesTheRestOpen(t *testing.T) {
	repoDir, invocationsLog, promptsDir, argsDir := clarifyEnv(t, 0.42, 0.9)

	stdout, stderr, err := runOrchestrator(t, repoDir, clarifyConfig(t), "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	want := []string{"decide-context", "clarify", "decide-clarity", "decide-classify", "investigate", "implement", "review"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v", got, want)
	}

	run := readClarifyRun(t, repoDir)
	c := run.Task.Clarification
	if c == nil || c.Status != "clear" || c.Threshold != 0.8 || len(c.Checks) != 2 || c.Checks[0] != 0.42 || c.Checks[1] != 0.9 {
		t.Fatalf("task.clarification = %+v, want clear, checks [0.42 0.9] against 0.8", c)
	}
	if len(c.Rounds) != 1 || c.Rounds[0].Kind != "clarify" || c.Rounds[0].Mode != "auto" || len(c.Rounds[0].Points) != 3 {
		t.Fatalf("rounds = %+v, want the one auto clarify round of three points", c.Rounds)
	}
	points := c.Rounds[0].Points
	if points[0].By != "docs" || points[0].Answer != "CSV" || points[0].Source != "docs/export.md#Formats" {
		t.Errorf("point 0 = %+v, want answered by docs from its source", points[0])
	}
	if points[1].By != "assumption" || points[1].Answer != "admins only" {
		t.Errorf("point 1 = %+v, want an assumption", points[1])
	}
	if points[2].By != "" || points[2].Answer != "" {
		t.Errorf("point 2 = %+v, want it left open", points[2])
	}

	// The clarify call has no tools: authority none.
	args := readArgs(t, argsDir, "clarify")
	if got, ok := flagValue(args, "--tools"); !ok || got != "" {
		t.Errorf("clarify: --tools = %q (present %v), want an empty tool list\nargv: %q", got, ok, args)
	}

	// The re-check and the decision see what was agreed.
	for _, name := range []string{"decide-clarity.txt", "decide-classify.txt"} {
		prompt, _ := os.ReadFile(filepath.Join(promptsDir, name))
		for _, want := range []string{"Which export format is meant? CSV", "Assumed: admins only", "Open: Which locale is the date in?"} {
			if !strings.Contains(string(prompt), want) {
				t.Errorf("%s lacks %q:\n%s", name, want, prompt)
			}
		}
	}

	d := readDecision(t, repoDir)
	if d.Clarity == nil || d.Clarity.Status != "clear" || len(d.Clarity.Checks) != 2 {
		t.Errorf("decision.json clarity = %+v, want two checks, clear", d.Clarity)
	}
	for _, rel := range []string{"logs/clarify-1.stdout.log", "artifacts/clarify-1.json", "logs/decide-clarity-1.stdout.log"} {
		if _, ok := runFile(t, repoDir, rel); !ok {
			t.Errorf("the run directory lacks %s", rel)
		}
	}
	if art, _ := runFile(t, repoDir, "artifacts/clarify-1.json"); !strings.Contains(art, `"prompt"`) || !strings.Contains(art, "ACTION: clarify") {
		t.Errorf("artifacts/clarify-1.json lacks the prompt as sent:\n%s", art)
	}
}

func TestClarify_AutoStillUnclearStopsWithTaskNeedsInput(t *testing.T) {
	repoDir, invocationsLog, _, _ := clarifyEnv(t, 0.42, 0.55)
	cfg := clarifyConfig(t)

	_, _, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
	if err == nil || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("exit %d (%v), want 4 for a task that needs input", exitcode.FromError(err), err)
	}
	for _, want := range []string{"task needs input", "Which locale is the date in?", "0.55 < 0.80"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q:\n%v", want, err)
		}
	}
	if got, want := invocations(t, invocationsLog), []string{"decide-context", "clarify", "decide-clarity"}; !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v", got, want)
	}

	run := readClarifyRun(t, repoDir)
	if run.Status != "stopped" || run.FailureReason != "task needs input" || run.Decision != nil || run.Workflow != nil {
		t.Errorf("run = status %q, reason %q, decision %v, workflow %v; want stopped before any workflow", run.Status, run.FailureReason, run.Decision, run.Workflow)
	}
	if c := run.Task.Clarification; c == nil || c.Status != "unclear" || len(c.Checks) != 2 {
		t.Errorf("task.clarification = %+v, want unclear with two checks", c)
	}
	if _, ok := runFile(t, repoDir, "decision.json"); ok {
		t.Error("a run that needs input has no decision.json")
	}
	if summary, _ := runFile(t, repoDir, "summary.md"); !strings.Contains(summary, "Open: Which locale is the date in?") {
		t.Errorf("summary.md lacks the open point:\n%s", summary)
	}

	// Nothing to resume: the task has to change.
	_, _, err = runOrchestrator(t, repoDir, cfg, "resume", run.ID)
	var stateErr *apperr.RunStateError
	if !errors.As(err, &stateErr) || stateErr.Kind != apperr.RunNotResumable || !strings.Contains(err.Error(), "nothing to resume") {
		t.Errorf("resume: %T %v, want a not-resumable refusal saying there is nothing to resume", err, err)
	}
}

func TestClarify_FailedClarifyCallFailsTheRun(t *testing.T) {
	repoDir, invocationsLog, _, _ := clarifyEnv(t, 0.42, 0.9)
	t.Setenv("FAKE_FAIL_ACTION", "clarify")
	t.Setenv("FAKE_FAIL_STDERR", "clarify exploded")

	_, _, err := runOrchestrator(t, repoDir, clarifyConfig(t), "run", "fix Add in fixture.go")
	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) || provErr.Action != "clarify" || exitcode.FromError(err) != exitcode.ProviderExecution {
		t.Fatalf("err = %T %v (exit %d), want a provider error of the clarify call, exit 3", err, err, exitcode.FromError(err))
	}
	// The fake CLI logs the failed clarify call too; nothing comes after it.
	if got, want := invocations(t, invocationsLog), []string{"decide-context", "clarify"}; !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v: an unclear task never goes on unclarified", got, want)
	}
	if run := readClarifyRun(t, repoDir); run.Status != "failed" || run.Workflow != nil {
		t.Errorf("run = status %q, workflow %v; want failed before any workflow", run.Status, run.Workflow)
	}
}

func TestClarify_DisabledMakesNoClarifyCallWhateverTheProbability(t *testing.T) {
	repoDir, invocationsLog, _, _ := clarifyEnv(t, 0.1)

	stdout, stderr, err := runOrchestrator(t, repoDir, clarifyConfig(t, func(o *configOptions) { o.providers.Clarify = "" }), "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	want := []string{"decide-context", "decide-classify", "investigate", "implement", "review"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v", got, want)
	}
	d := readDecision(t, repoDir)
	if d.Clarity == nil || d.Clarity.Status != "disabled" || len(d.Clarity.Checks) != 1 || d.Clarity.Checks[0] != 0.1 {
		t.Errorf("decision.json clarity = %+v, want one disabled check of 0.1", d.Clarity)
	}
	if run := readClarifyRun(t, repoDir); run.Task.Clarification != nil {
		t.Errorf("run.json has task.clarification %+v with clarify off", run.Task.Clarification)
	}
}

// run --stage runs the same loop: the stages are the operator's, the decision stays manual and the
// clarification is recorded beside it.
func TestClarify_StageRunClarifiesToo(t *testing.T) {
	repoDir, invocationsLog, _, _ := clarifyEnv(t, 0.5, 0.3, 0.9)

	stdout, stderr, err := runOrchestrator(t, repoDir, clarifyConfig(t), "run", "--stage", "investigation", "explain Add")
	mustSucceed(t, stdout, stderr, err)

	want := []string{"decide-clarity", "clarify", "decide-clarity", "investigate"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v", got, want)
	}
	d := readDecision(t, repoDir)
	if d.Source != "manual" || d.Clarity == nil || d.Clarity.Status != "clear" || len(d.Clarity.Checks) != 2 {
		t.Errorf("decision = %+v clarity %+v, want manual with two checks, clear", d, d.Clarity)
	}
	if c := readClarifyRun(t, repoDir).Task.Clarification; c == nil || c.Status != "clear" || len(c.Checks) != 2 {
		t.Errorf("task.clarification = %+v, want clear with two checks", c)
	}
}
