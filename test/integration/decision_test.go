package integration

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Decision-flow scenarios (specs/JEV integration/TASKS.md T13). They drive app.Run
// against a fake Jev (jev_test.go) or an LLM decision provider through the fake CLI.

const envSecret = "DATABASE_PASSWORD=hunter2-integration"

const investigateResponse = `{"summary": "fix the operator", "steps": ["use +"], "affected_files": ["fixture.go"], "risks": []}`

// decisionEnv sets up the fake CLIs with canned responses for every stage and returns
// the invocations log path.
func decisionEnv(t *testing.T, repoDir string) (invocationsLog, promptsDir string) {
	t.Helper()
	installFakeProviders(t, "claude", "codex", "gemini")

	responsesDir := t.TempDir()
	for name, body := range map[string]string{
		"investigate": investigateResponse, "implement": implementResponse, "review": reviewPassResponse,
	} {
		writeResponse(t, responsesDir, name, body)
	}
	invocationsLog = filepath.Join(t.TempDir(), "invocations.log")
	promptsDir = t.TempDir()

	t.Setenv("FAKE_RESPONSES_DIR", responsesDir)
	t.Setenv("FAKE_STATE_DIR", t.TempDir())
	t.Setenv("INVOCATIONS_LOG", invocationsLog)
	t.Setenv("FAKE_PROMPTS_DIR", promptsDir)
	t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", "1")
	t.Setenv("FAKE_FIX_TARGET_FILE", filepath.Join(repoDir, "fixture.go"))
	t.Setenv("FAKE_FIX_CONTENT_FILE", writeFixedFixtureFile(t))
	return invocationsLog, promptsDir
}

func jevConfig(t *testing.T, j *fakeJev, mutate ...func(*configOptions)) string {
	t.Helper()
	opts := defaultConfigOptions()
	opts.jevURL = j.URL()
	for _, m := range mutate {
		m(&opts)
	}
	return writeConfig(t, t.TempDir(), opts)
}

// runFile reads a file from the single run directory; ok is false when it is absent.
func runFile(t *testing.T, repoDir, name string) (string, bool) {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(repoDir, ".orchestrator", "runs", "*"))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("expected exactly one run directory, got %v (err=%v)", dirs, err)
	}
	data, err := os.ReadFile(filepath.Join(dirs[0], name))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data), true
}

type decisionJSON struct {
	Source        string `json:"source"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Request       string `json:"request"`
	RequestEN     string `json:"request_en"`
	DecisionError string `json:"decision_error"`
	Translation   struct {
		Translated bool   `json:"translated"`
		Failed     bool   `json:"failed"`
		Reason     string `json:"reason"`
	} `json:"translation"`
	ContextDepth *struct {
		Selected string `json:"selected"`
	} `json:"context_depth"`
	ContextLimited bool   `json:"context_limited"`
	ContextError   string `json:"context_error"`
	Complexity     struct {
		Primary  string `json:"primary"`
		Selected string `json:"selected"`
		Agree    bool   `json:"agree"`
		Sources  map[string]struct {
			Selected string `json:"selected"`
		} `json:"sources"`
	} `json:"complexity"`
	Stages []struct {
		Stage   string `json:"stage"`
		Forced  bool   `json:"forced"`
		Enabled bool   `json:"enabled"`
		Reason  string `json:"reason"`
	} `json:"stages"`
	Scope *struct {
		Story float64 `json:"story"`
	} `json:"scope"`
	Clarity *struct {
		Threshold float64   `json:"threshold"`
		Checks    []float64 `json:"checks"`
		Status    string    `json:"status"`
		Reason    string    `json:"reason"`
	} `json:"clarity"`
	Usage *struct {
		ClarityRecheck []json.RawMessage `json:"clarity_recheck"`
	} `json:"usage"`
	Workflow []string `json:"workflow"`
}

func readDecision(t *testing.T, repoDir string) decisionJSON {
	t.Helper()
	raw, ok := runFile(t, repoDir, "decision.json")
	if !ok {
		t.Fatal("decision.json was not written")
	}
	var d decisionJSON
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("decision.json: %v", err)
	}
	return d
}

func mustSucceed(t *testing.T, stdout, stderr string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
}

// 1. PROJECT: the context sent to Jev never contains .env or its contents, and the
// workflow and the order of actions follow the decision.
func TestDecision_ProjectContextExcludesSecrets(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	if err := os.WriteFile(filepath.Join(repoDir, ".env"), []byte(envSecret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Committed, the worst realistic case: git lists it, so only the deny list keeps it
	// out - and the worktree stays clean, as a workflow with implement requires.
	commitAll(t, repoDir, "add .env")
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j, withClarify("claude")), "run", "fix Add in fixture.go, check the env settings too")
	mustSucceed(t, stdout, stderr, err)

	for i, body := range j.Bodies() {
		if strings.Contains(body, ".env") || strings.Contains(body, "hunter2") {
			t.Errorf("Jev request %d leaks .env:\n%s", i, body)
		}
	}
	pc, ok := runFile(t, repoDir, "project_context.json")
	if !ok || !strings.Contains(pc, "fixture.go") || strings.Contains(pc, ".env") {
		t.Errorf("project_context.json = %q (present=%v)", pc, ok)
	}
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"investigate", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
	d := readDecision(t, repoDir)
	// The shadow scope question is asked and its probability recorded (specs/story/SPEC.md §13).
	if d.Scope == nil || d.Scope.Story != 0.15 {
		t.Errorf("decision.json scope = %+v, want story 0.15", d.Scope)
	}
	// The clarity question rides in the pre-check's call: one value, no extra request.
	if d.Clarity == nil || len(d.Clarity.Checks) != 1 || d.Clarity.Checks[0] != 0.95 || d.Clarity.Threshold != 0.8 || d.Clarity.Status != "clear" {
		t.Errorf("decision.json clarity = %+v, want one check of 0.95 against 0.8, clear", d.Clarity)
	}
	if n := len(j.Bodies()); n != 2 {
		t.Errorf("Jev got %d requests, want the pre-check and the classification", n)
	}
	if !equalStrings(d.Workflow, []string{"investigate", "implement", "test", "review"}) || d.ContextDepth.Selected != "project" {
		t.Errorf("decision = %+v", d)
	}
}

// 2. NONE: the Context Manager is not called, no project_context.json is written, and
// Jev is told no project context is available.
func TestDecision_DepthNoneSkipsContext(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	script := scriptBugfixLow()
	script.PProject = 0.1
	j := startJev(t, script)

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "fix Add() to add instead of subtract")
	mustSucceed(t, stdout, stderr, err)

	if _, ok := runFile(t, repoDir, "project_context.json"); ok {
		t.Error("project_context.json must not exist for depth none")
	}
	bodies := j.Bodies()
	if len(bodies) != 2 || !strings.Contains(bodies[1], `"project_context_available":false`) || strings.Contains(bodies[1], `"project":`) {
		t.Errorf("classification request = %v", bodies)
	}
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations = %v", got)
	}
}

// 3. complex with low stage probabilities: the forced stages run anyway.
func TestDecision_ComplexForcesStages(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, jevScript{
		PProject: 0.9,
		Choice:   [3]float64{0.1, 0.2, 0.7},
		Stages:   map[string]float64{"development": 0.9, "testing": 0.9},
	})

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "rework the fixture package")
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"investigate", "implement", "review"}) {
		t.Errorf("invocations = %v, want the forced investigation and review", got)
	}
	d := readDecision(t, repoDir)
	for _, s := range d.Stages {
		if (s.Stage == "investigation" || s.Stage == "review") && (!s.Forced || !s.Enabled || !strings.Contains(s.Reason, "forced: complexity=complex")) {
			t.Errorf("stage %+v should be forced on", s)
		}
	}
}

// 4. The Context Manager fails: the run continues with context_limited.
func TestDecision_ContextFailureContinuesLimited(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixLow())

	// A git that fails only the file listing the Context Manager relies on (ls-files with
	// --cached); review's listing of untracked files must still work.
	wrapperDir := t.TempDir()
	fakeexec.WriteExecutable(t, wrapperDir, "git", "#!/bin/sh\n"+
		"if [ \"$1\" = \"ls-files\" ]; then case \" $* \" in *\" --cached \"*) echo 'fatal: simulated ls-files failure' >&2; exit 128;; esac; fi\n"+
		"exec "+realGit+" \"$@\"\n")
	fakeexec.PrependPath(t, wrapperDir)

	stdout, stderr, runErr := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "fix Add() to add instead of subtract")
	mustSucceed(t, stdout, stderr, runErr)

	d := readDecision(t, repoDir)
	if !d.ContextLimited || !strings.Contains(d.ContextError, "simulated ls-files failure") {
		t.Errorf("decision = %+v, want context_limited with the error", d)
	}
	if _, ok := runFile(t, repoDir, "project_context.json"); ok {
		t.Error("no context was sent, so no project_context.json")
	}
	if !strings.Contains(stdout, "limited: ") {
		t.Errorf("console does not show the limited context:\n%s", stdout)
	}
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations = %v", got)
	}
}

// 5a. A Russian request: the translator runs, Jev gets English, stages get the original.
func TestDecision_RussianRequestIsTranslated(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, promptsDir := decisionEnv(t, repoDir)
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "translate", "Fix Add() in fixture.go so that it adds instead of subtracting")
	j := startJev(t, scriptBugfixMedium())

	request := "исправь Add() в fixture.go, чтобы она складывала, а не вычитала"
	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", request)
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"translate", "investigate", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
	for i, body := range j.Bodies() {
		if !strings.Contains(body, "Fix Add() in fixture.go so that it adds") || strings.Contains(body, "исправь") {
			t.Errorf("Jev request %d must carry the English text only:\n%s", i, body)
		}
	}
	prompt, _ := os.ReadFile(filepath.Join(promptsDir, "investigate.txt"))
	if !strings.Contains(string(prompt), request) {
		t.Errorf("the investigate stage must get the original request:\n%s", prompt)
	}
	d := readDecision(t, repoDir)
	if !d.Translation.Translated || d.RequestEN == "" || d.Request != request {
		t.Errorf("decision = %+v", d)
	}
	if !strings.Contains(stdout, "translation: ru → en") {
		t.Errorf("console does not show the translation:\n%s", stdout)
	}
}

// 5b. The translation fails: Jev gets the original, and the trace says so.
func TestDecision_TranslationFailureSendsOriginal(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	t.Setenv("FAKE_FAIL_ACTION", "translate")
	j := startJev(t, scriptBugfixMedium())

	request := "исправь Add() в fixture.go, чтобы она складывала"
	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", request)
	mustSucceed(t, stdout, stderr, err)

	if body := j.Bodies()[0]; !strings.Contains(body, "исправь") {
		t.Errorf("Jev must get the original text:\n%s", body)
	}
	d := readDecision(t, repoDir)
	if !d.Translation.Failed || d.Translation.Reason == "" || d.RequestEN != "" {
		t.Errorf("translation = %+v", d.Translation)
	}
	if !strings.Contains(stdout, "Warning: translation failed") {
		t.Errorf("console does not warn:\n%s", stdout)
	}
}

// 6. Jev answers 529 twice (the request and its one retry) with on_failure: ask: the
// user picks complexity and stages on stdin, the forced stages are added, and the run
// goes on with source user.
func TestDecision_JevDownManualChoice(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, jevScript{Fail: []int{529, 529}})
	configPath := jevConfig(t, j, func(o *configOptions) { o.onFailure = "ask" })

	// complexity 3 (complex), stages 3,4 (development, testing); approve; mode auto.
	stdout, stderr, err := runOrchestratorInteractive(t, repoDir, configPath, "3\n3,4\na\n4\n", "run", "fix Add() to add instead of subtract")
	mustSucceed(t, stdout, stderr, err)

	if len(j.Bodies()) != 2 {
		t.Errorf("Jev got %d requests, want the request and one retry", len(j.Bodies()))
	}
	if !strings.Contains(stdout, "Decision provider failed:") || !strings.Contains(stdout, "HTTP 529") {
		t.Errorf("the failure is not shown before the choice:\n%s", stdout)
	}
	d := readDecision(t, repoDir)
	if d.Source != "user" || d.Complexity.Selected != "complex" || !strings.Contains(d.DecisionError, "529") {
		t.Errorf("decision = %+v", d)
	}
	if !equalStrings(d.Workflow, []string{"investigate", "implement", "test", "review"}) {
		t.Errorf("workflow = %v, want the user's stages plus the forced ones", d.Workflow)
	}
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"investigate", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
}

// 7a. Jev down in --mode auto: no one to ask - the run fails (exit 3), no stage runs.
func TestDecision_JevDownInAutoMode(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, jevScript{Fail: []int{503, 503, 503, 503}})

	_, _, err := runOrchestrator(t, repoDir, jevConfig(t, j, func(o *configOptions) { o.onFailure = "ask" }), "run", "fix Add()")
	var derr *apperr.DecisionError
	if !errors.As(err, &derr) || derr.Kind != apperr.DecisionUnavailable || !strings.Contains(err.Error(), "without --mode auto") {
		t.Fatalf("err = %v", err)
	}
	if code := exitcode.FromError(err); code != exitcode.ProviderExecution {
		t.Errorf("exit code = %d, want %d", code, exitcode.ProviderExecution)
	}
	if got := invocations(t, invocationsLog); len(got) != 0 {
		t.Errorf("no stage may run: %v", got)
	}
}

// 7b. Jev rejects the key with on_failure: fail: exit 2, no retry, no stage runs.
func TestDecision_JevAuthFailure(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixLow())
	t.Setenv("JEV_API_KEY", "wrong-key")

	_, _, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "fix Add()")
	var derr *apperr.DecisionError
	if !errors.As(err, &derr) || derr.Kind != apperr.DecisionAuth {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "wrong-key") {
		t.Errorf("the API key leaked into the error: %v", err)
	}
	if code := exitcode.FromError(err); code != exitcode.ConfigOrDependency {
		t.Errorf("exit code = %d, want %d", code, exitcode.ConfigOrDependency)
	}
	// The model check (TASKS.md T14) meets the 401 first: one request, never retried, and
	// no decision round, translation or run follows.
	if j.ModelRequests() != 1 || len(j.Bodies()) != 0 {
		t.Errorf("Jev got %d model and %d decision requests, want 1 and 0", j.ModelRequests(), len(j.Bodies()))
	}
	if got := invocations(t, invocationsLog); len(got) != 0 {
		t.Errorf("no stage may run: %v", got)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".orchestrator", "runs")); !os.IsNotExist(err) {
		t.Errorf("a rejected key must create no run (stat: %v)", err)
	}
}

// 8. No JEV_API_KEY: the run fails before any HTTP request or CLI call.
func TestDecision_MissingAPIKeyFailsFast(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixLow())
	t.Setenv("JEV_API_KEY", "")

	_, _, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "исправь Add()")
	var depErr *apperr.DependencyError
	if !errors.As(err, &depErr) || !strings.Contains(err.Error(), "JEV_API_KEY is not configured") {
		t.Fatalf("err = %v", err)
	}
	if len(j.Bodies()) != 0 {
		t.Error("no request may reach Jev")
	}
	if got := invocations(t, invocationsLog); len(got) != 0 {
		t.Errorf("no CLI may run, not even the translator: %v", got)
	}
}

// 9. choice and score disagree: agree is false, the decision follows the primary.
func TestDecision_ChoiceAndScoreDisagree(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	script := scriptBugfixMedium()
	script.Score = &[3]float64{0.05, 0.15, 0.8}
	j := startJev(t, script)

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "fix Add() to add instead of subtract")
	mustSucceed(t, stdout, stderr, err)

	d := readDecision(t, repoDir)
	c := d.Complexity
	if c.Agree || c.Primary != "choice" || c.Selected != "medium" || c.Sources["score"].Selected != "complex" {
		t.Errorf("complexity = %+v", c)
	}
	if !strings.Contains(stdout, "✗ disagrees") {
		t.Errorf("console does not flag the disagreement:\n%s", stdout)
	}
}

// 11. decision.provider: claude - the decision comes from the fake CLI, nothing talks to
// Jev and no key is needed, and decision.json has the same shape with source llm.
func TestDecision_LLMProvider(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	invocationsLog, promptsDir := decisionEnv(t, repoDir)
	responsesDir := os.Getenv("FAKE_RESPONSES_DIR")
	writeResponse(t, responsesDir, "decide-context", `{"context_depth": {"none": 0.1, "project": 0.9}, "clarity": 0.9}`)
	writeResponse(t, responsesDir, "decide-classify", `{
  "complexity": {"simple": 0.1, "medium": 0.7, "complex": 0.2},
  "complexity_score": {"0": 0.1, "1": 0.7, "2": 0.2},
  "stages": {"research": 0.05, "development": 0.95},
  "scope": 0.2
}`)
	t.Setenv("JEV_API_KEY", "")

	argsDir := t.TempDir()
	t.Setenv("FAKE_ARGS_DIR", argsDir)

	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	opts.providers.Clarify = "claude"
	stdout, stderr, err := runOrchestrator(t, repoDir, writeConfig(t, t.TempDir(), opts), "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)
	assertAuthorities(t, repoDir, argsDir, stdout)

	want := []string{"decide-context", "decide-classify", "investigate", "implement", "review"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v", got, want)
	}
	d := readDecision(t, repoDir)
	if d.Source != "llm" || d.Provider != "claude" || d.Model != "fake" ||
		!equalStrings(d.Workflow, []string{"investigate", "implement", "test", "review"}) {
		t.Errorf("decision = %+v", d)
	}
	if d.Clarity == nil || len(d.Clarity.Checks) != 1 || d.Clarity.Checks[0] != 0.9 || d.Clarity.Status != "clear" {
		t.Errorf("decision.json clarity = %+v, want one check of 0.9, clear", d.Clarity)
	}
	prompt, _ := os.ReadFile(filepath.Join(promptsDir, "decide-classify.txt"))
	if !strings.Contains(string(prompt), `"project_context_available": true`) || !strings.Contains(string(prompt), "fixture.go") {
		t.Errorf("the classify prompt must carry the collected context:\n%s", prompt)
	}
	if !strings.Contains(stdout, "decision: claude (fake)") {
		t.Errorf("console:\n%s", stdout)
	}
}
