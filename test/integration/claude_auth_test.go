package integration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 58 (specs/claude-auth/SPEC.md §4-§13, CA06): every credential mode end to end with
// the fake CLI, which records each call's environment (FAKE_ENV_DIR) and can report an
// apiKeySource or an api_retry error as the real CLI's stream-json does.

// authKey is a stand-in API key, assembled at run time like every key-shaped value in tests.
func authKey() string { return strings.Join([]string{"ca06", "stand-in", "key"}, "-") }

// authEnvNames are the variables the scenarios set or clear.
var authEnvNames = []string{
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_OAUTH_TOKEN", "ORCH_CLAUDE_KEY",
	"FAKE_API_KEY_SOURCE", "FAKE_API_RETRY_ERROR", "FAKE_API_RETRY_ACTION",
}

// clearAuthEnv unsets every variable of authEnvNames for the test (and restores it after).
func clearAuthEnv(t *testing.T) {
	t.Helper()
	for _, name := range authEnvNames {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}

// authRequest is Russian, so the translator runs.
const authRequest = "исправь Add() в fixture.go, чтобы она складывала, а не вычитала"

// authCalls are the calls of a full run with the LLM decision provider, in order.
var authCalls = []string{"translate", "decide-context", "decide-classify", "analyze", "implement", "review"}

// authRun prepares a full run whose translator and decision provider are claude (or what
// mutate sets) and returns its config and the directory FAKE_ENV_DIR points to.
func authRun(t *testing.T, repoDir string, mutate ...func(*configOptions)) (cfg, envDir string) {
	t.Helper()
	decisionEnv(t, repoDir)
	responses := os.Getenv("FAKE_RESPONSES_DIR")
	t.Setenv("JEV_API_KEY", "")
	for name, body := range map[string]string{
		"translate":      "Fix Add() in fixture.go so that it adds instead of subtracting",
		"decide-context": `{"context_depth": {"none": 0.9, "project": 0.1}}`,
		"decide-classify": `{"complexity": {"simple": 0.1, "medium": 0.7, "complex": 0.2},
 "complexity_score": {"0": 0.1, "1": 0.7, "2": 0.2},
 "stages": {"research": 0.05, "analysis": 0.9, "development": 0.95},
 "scope": 0.2}`,
	} {
		writeResponse(t, responses, name, body)
	}
	envDir = t.TempDir()
	t.Setenv("FAKE_ENV_DIR", envDir)
	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	for _, m := range mutate {
		m(&opts)
	}
	return writeConfig(t, t.TempDir(), opts), envDir
}

// callEnv is the environment the fake recorded for one call.
func callEnv(t *testing.T, envDir, action string) map[string]string {
	t.Helper()
	return readEnvDump(t, filepath.Join(envDir, action+".env"))
}

// authRunJSON is the credential part of run.json.
type authRunJSON struct {
	Status        string      `json:"status"`
	FailureReason string      `json:"failure_reason"`
	ID            string      `json:"id"`
	ClaudeAuth    *authRecord `json:"claude_auth"`
	Resumes       []struct {
		ClaudeAuth *authRecord `json:"claude_auth"`
	} `json:"resumes"`
}

type authRecord struct {
	Requested string   `json:"requested"`
	Mode      string   `json:"mode"`
	DecidedBy string   `json:"decided_by"`
	KeyEnv    string   `json:"key_env"`
	APIEnv    []string `json:"api_env"`
}

func loadAuthRun(t *testing.T, repoDir string) authRunJSON {
	t.Helper()
	var run authRunJSON
	if err := json.Unmarshal([]byte(readFile(t, runJSONPath(t, repoDir))), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

// §13 #1 (no key): auto resolves to login and no call's environment has the key variable.
func TestClaudeAuth_AutoWithoutKeyIsLogin(t *testing.T) {
	clearAuthEnv(t)
	repoDir := fakeexec.InitGitFixture(t)
	cfg, envDir := authRun(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", authRequest)
	mustSucceed(t, stdout, stderr, err)

	for _, action := range authCalls {
		if _, ok := callEnv(t, envDir, action)["ANTHROPIC_API_KEY"]; ok {
			t.Errorf("%s inherited ANTHROPIC_API_KEY in login mode", action)
		}
	}
	assertContains(t, "the console", stdout, "claude auth: CLI login")
	run := loadAuthRun(t, repoDir)
	if run.ClaudeAuth == nil || run.ClaudeAuth.Mode != "login" || run.ClaudeAuth.Requested != "auto" || run.ClaudeAuth.DecidedBy != "default" {
		t.Errorf("claude_auth = %+v, want auto -> login by default", run.ClaudeAuth)
	}
}

// §13 #1, #2: with the key set, auto is api; every claude call has the key, and what outranks
// it in the CLI's precedence is gone.
func TestClaudeAuth_AutoWithKeyIsAPI(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", authKey())
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "gateway-token-value")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	repoDir := fakeexec.InitGitFixture(t)
	cfg, envDir := authRun(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", authRequest)
	mustSucceed(t, stdout, stderr, err)

	for _, action := range authCalls {
		env := callEnv(t, envDir, action)
		if env["ANTHROPIC_API_KEY"] != authKey() {
			t.Errorf("%s did not get the API key", action)
		}
		for _, name := range []string{"ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK"} {
			if _, ok := env[name]; ok {
				t.Errorf("%s inherited %s in api mode", action, name)
			}
		}
	}
	assertContains(t, "the console", stdout+stderr, "claude auth: api key ($ANTHROPIC_API_KEY, auto)",
		"ignored in api mode: ANTHROPIC_AUTH_TOKEN, CLAUDE_CODE_USE_BEDROCK")
	assertNotContains(t, "the console", stdout+stderr, authKey(), "gateway-token-value")
	run := loadAuthRun(t, repoDir)
	if run.ClaudeAuth == nil || run.ClaudeAuth.Mode != "api" || run.ClaudeAuth.KeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("claude_auth = %+v, want api", run.ClaudeAuth)
	}
	// Nothing the run wrote (run.json, decision.json, logs, summary) holds the key.
	runDir := filepath.Dir(runJSONPath(t, repoDir))
	err = filepath.WalkDir(runDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		assertNotContains(t, path, readFile(t, path), authKey())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// §5, §7: the key comes from the variable providers.claude.api_key_env names; the child sees
// it as ANTHROPIC_API_KEY and not under the configured name, and the test command sees neither.
func TestClaudeAuth_ConfiguredKeyVariable(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ORCH_CLAUDE_KEY", authKey())
	repoDir := fakeexec.InitGitFixture(t)
	testEnv := filepath.Join(t.TempDir(), "test.env")
	cfg, envDir := authRun(t, repoDir, func(o *configOptions) {
		o.extraYAML = "\nproviders:\n  claude:\n    api_key_env: ORCH_CLAUDE_KEY\n"
		o.testCommand = "env > '" + testEnv + "'"
	})

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", authRequest)
	mustSucceed(t, stdout, stderr, err)

	for _, action := range authCalls {
		env := callEnv(t, envDir, action)
		if env["ANTHROPIC_API_KEY"] != authKey() {
			t.Errorf("%s did not get the key as ANTHROPIC_API_KEY", action)
		}
		if _, ok := env["ORCH_CLAUDE_KEY"]; ok {
			t.Errorf("%s inherited ORCH_CLAUDE_KEY", action)
		}
	}
	got := readEnvDump(t, testEnv)
	for _, name := range []string{"ANTHROPIC_API_KEY", "ORCH_CLAUDE_KEY"} {
		if _, ok := got[name]; ok {
			t.Errorf("the test command inherited %s", name)
		}
	}
	if run := loadAuthRun(t, repoDir); run.ClaudeAuth == nil || run.ClaudeAuth.KeyEnv != "ORCH_CLAUDE_KEY" || run.ClaudeAuth.DecidedBy != "default" {
		t.Errorf("claude_auth = %+v, want key_env ORCH_CLAUDE_KEY", run.ClaudeAuth)
	}
}

// §4: --claude-auth login with the key set removes it everywhere and says it was not used.
func TestClaudeAuth_LoginFlagDropsTheKey(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", authKey())
	repoDir := fakeexec.InitGitFixture(t)
	cfg, envDir := authRun(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "--claude-auth", "login", "run", authRequest)
	mustSucceed(t, stdout, stderr, err)

	for _, action := range authCalls {
		if _, ok := callEnv(t, envDir, action)["ANTHROPIC_API_KEY"]; ok {
			t.Errorf("%s inherited ANTHROPIC_API_KEY under --claude-auth login", action)
		}
	}
	assertContains(t, "the console", stdout+stderr, "ANTHROPIC_API_KEY is set but not used (--claude-auth login)")
	if run := loadAuthRun(t, repoDir); run.ClaudeAuth == nil || run.ClaudeAuth.Mode != "login" || run.ClaudeAuth.DecidedBy != "flag" {
		t.Errorf("claude_auth = %+v, want login by flag", run.ClaudeAuth)
	}
}

// specs/claude-auth/SPEC.md §4: --claude-auth api with the key variable unset is a config error
// inside the fail-fast chain, so the fake claude is not called once.
func TestClaudeAuth_APIWithoutKeyFailsBeforeAnyCall(t *testing.T) {
	r := newStageRun(t, fakeexec.InitGitFixture(t))
	argsDir := t.TempDir()
	t.Setenv("FAKE_ARGS_DIR", argsDir)
	t.Setenv("ANTHROPIC_API_KEY", "") // registers the restore; the next line unsets it
	os.Unsetenv("ANTHROPIC_API_KEY")

	_, _, err := runOrchestrator(t, r.repoDir, r.cfg, "--claude-auth", "api", "run", "--stage", "research", "look")
	var cerr *apperr.ConfigError
	if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY is not set") {
		t.Fatalf("want a config error naming the variable, got %T: %v", err, err)
	}
	entries, readErr := os.ReadDir(argsDir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Errorf("claude was called before the failure: %v", entries)
	}
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("calls before the refusal: %v", got)
	}
}

// §13 #7: under api the CLI reports apiKeySource none (a gateway session): the first call fails
// the run with FailureAuth, and the translator does not fall back to the original text.
func TestClaudeAuth_APIWithSourceNoneIsFatalAtTheTranslator(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", authKey())
	t.Setenv("FAKE_API_KEY_SOURCE", "none")
	repoDir := fakeexec.InitGitFixture(t)
	cfg, _ := authRun(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", authRequest)
	var perr *apperr.ProviderError
	if !errors.As(err, &perr) || perr.Kind != apperr.FailureAuth {
		t.Fatalf("want a FailureAuth provider error, got %T: %v\nstdout:\n%s\nstderr:\n%s", err, err, stdout, stderr)
	}
	if perr.Action != "translate" {
		t.Errorf("Action = %q, want translate", perr.Action)
	}
	if !strings.Contains(err.Error(), "claude auth (api)") {
		t.Errorf("the error does not name the mode:\n%v", err)
	}
	if got := invocations(t, os.Getenv("INVOCATIONS_LOG")); !equalStrings(got, []string{"translate"}) {
		t.Errorf("invocations = %v, want only translate: no fallback, no later call", got)
	}
	assertNotContains(t, "the output", err.Error()+stdout+stderr, authKey())
}

// authFailedRun fails a manual implement run with a billing error under api and returns its
// config and the failed run; the fake would otherwise fix the fixture, so the failed call
// leaves the tree alone.
func authFailedRun(t *testing.T, repoDir string) (cfg string, run authRunJSON) {
	t.Helper()
	clearAuthEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", authKey())
	t.Setenv("FAKE_API_KEY_SOURCE", "auto")
	t.Setenv("FAKE_API_RETRY_ERROR", "billing_error")
	t.Setenv("FAKE_API_RETRY_ACTION", "implement")
	neverFix(t)
	r := newStageRun(t, repoDir)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "fix Add in fixture.go")
	if err == nil {
		t.Fatalf("a billing error in a step must fail the run\nstdout:\n%s", stdout)
	}
	if !strings.Contains(err.Error()+stdout+stderr, "claude auth (api): billing_error") {
		t.Errorf("the failure does not name the mode and the error:\n%v\n%s%s", err, stdout, stderr)
	}
	var perr *apperr.ProviderError
	if !errors.As(err, &perr) || perr.Kind != apperr.FailureAuth {
		t.Errorf("want a FailureAuth provider error, got %T", err)
	}
	run = loadAuthRun(t, repoDir)
	if run.Status != "failed" {
		t.Fatalf("run status = %s, want failed", run.Status)
	}
	return r.cfg, run
}

// §9: an api_retry billing_error in a step fails the run, and the message names the mode.
func TestClaudeAuth_BillingErrorInAStepFailsTheRun(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	authFailedRun(t, repoDir)
}

// §11: a run stopped by that error continues under --claude-auth login, and the resume records
// its own mode.
func TestClaudeAuth_ResumeSwitchesMode(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	cfg, failed := authFailedRun(t, repoDir)
	if failed.ClaudeAuth == nil || failed.ClaudeAuth.Mode != "api" {
		t.Fatalf("claude_auth = %+v, want api", failed.ClaudeAuth)
	}

	t.Setenv("FAKE_API_RETRY_ERROR", "")
	fixOnCall(t, 1)
	stdout, stderr, err := resume(t, repoDir, cfg, failed.ID, "--claude-auth", "login")
	mustSucceed(t, stdout, stderr, err)

	assertContains(t, "the console", stdout+stderr, "claude auth changed since the run started: api → login")
	run := loadAuthRun(t, repoDir)
	if run.Status != "completed" {
		t.Errorf("status = %s, want completed\n%s", run.Status, stdout)
	}
	if run.ClaudeAuth == nil || run.ClaudeAuth.Mode != "api" {
		t.Errorf("the run's claude_auth = %+v, want the original api", run.ClaudeAuth)
	}
	if len(run.Resumes) != 1 || run.Resumes[0].ClaudeAuth == nil || run.Resumes[0].ClaudeAuth.Mode != "login" || run.Resumes[0].ClaudeAuth.DecidedBy != "flag" {
		t.Errorf("resumes = %+v, want one entry with login by flag", run.Resumes)
	}
}

// apiEnvYAML is a gateway setup for providers.claude.api_env (SPEC §17.2), with stand-in values.
const apiEnvYAML = "\nproviders:\n  claude:\n    api_env:\n" +
	"      ANTHROPIC_BASE_URL: https://gw.ca09.example/v1\n" +
	"      AWS_PROFILE: ca09-profile\n" +
	"      ANTHROPIC_DEFAULT_OPUS_MODEL: ca09-opus-alias\n"

// apiEnvValues are the values apiEnvYAML sets, by name.
var apiEnvValues = map[string]string{
	"ANTHROPIC_BASE_URL":           "https://gw.ca09.example/v1",
	"AWS_PROFILE":                  "ca09-profile",
	"ANTHROPIC_DEFAULT_OPUS_MODEL": "ca09-opus-alias",
}

// apiEnvNames are apiEnvValues' names, sorted, as run.json records them.
var apiEnvNames = []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "AWS_PROFILE"}

// clearAPIEnv unsets the api_env variables for the test (and restores them after).
func clearAPIEnv(t *testing.T) {
	t.Helper()
	for _, name := range apiEnvNames {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}

// §17.4, §17.5: in api mode every claude call gets api_env and the test command none of it;
// run.json records the names, never a value, and summary.md counts them.
func TestClaudeAuth_APIEnvInAPIMode(t *testing.T) {
	clearAuthEnv(t)
	clearAPIEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", authKey())
	repoDir := fakeexec.InitGitFixture(t)
	testEnv := filepath.Join(t.TempDir(), "test.env")
	cfg, envDir := authRun(t, repoDir, func(o *configOptions) {
		o.extraYAML = apiEnvYAML
		o.testCommand = "env > '" + testEnv + "'"
	})

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", authRequest)
	mustSucceed(t, stdout, stderr, err)

	for _, action := range authCalls {
		env := callEnv(t, envDir, action)
		for name, want := range apiEnvValues {
			if env[name] != want {
				t.Errorf("%s: %s = %q, want the api_env value", action, name, env[name])
			}
		}
		if env["ANTHROPIC_API_KEY"] != authKey() {
			t.Errorf("%s did not get the API key", action)
		}
	}
	got := readEnvDump(t, testEnv)
	for _, name := range apiEnvNames {
		if _, ok := got[name]; ok {
			t.Errorf("the test command inherited %s", name)
		}
	}
	console := stdout + stderr
	assertContains(t, "the console", console,
		"claude env (api): ANTHROPIC_BASE_URL (gw.ca09.example), ANTHROPIC_DEFAULT_OPUS_MODEL, AWS_PROFILE",
		"ANTHROPIC_BASE_URL is set (providers.claude.api_env): the API key goes to gw.ca09.example")
	assertNotContains(t, "the console", console, "ca09-profile", "ca09-opus-alias", "gw.ca09.example/v1")

	run := loadAuthRun(t, repoDir)
	if run.ClaudeAuth == nil || run.ClaudeAuth.Mode != "api" || !equalStrings(run.ClaudeAuth.APIEnv, apiEnvNames) {
		t.Errorf("claude_auth = %+v, want api with the api_env names", run.ClaudeAuth)
	}
	runJSON := readFile(t, runJSONPath(t, repoDir))
	for _, v := range apiEnvValues {
		assertNotContains(t, "run.json", runJSON, v)
	}
	assertContains(t, "summary.md", mustRunFile(t, repoDir, "summary.md"), "env: 3 variables")
}

// §17.4, D9: under --claude-auth login the same config removes the api_env names, so values
// exported in the shell do not reach claude; the notice and run.json name them.
func TestClaudeAuth_APIEnvRemovedInLoginMode(t *testing.T) {
	clearAuthEnv(t)
	clearAPIEnv(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://shell.ca09.example")
	t.Setenv("AWS_PROFILE", "ca09-shell-profile")
	t.Setenv("ANTHROPIC_DEFAULT_OPUS_MODEL", "ca09-shell-alias")
	repoDir := fakeexec.InitGitFixture(t)
	cfg, envDir := authRun(t, repoDir, func(o *configOptions) { o.extraYAML = apiEnvYAML })

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "--claude-auth", "login", "run", authRequest)
	mustSucceed(t, stdout, stderr, err)

	for _, action := range authCalls {
		env := callEnv(t, envDir, action)
		for _, name := range apiEnvNames {
			if v, ok := env[name]; ok {
				t.Errorf("%s got %s=%q under --claude-auth login", action, name, v)
			}
		}
	}
	console := stdout + stderr
	assertContains(t, "the console", console, "ignored in login mode: ANTHROPIC_BASE_URL, ANTHROPIC_DEFAULT_OPUS_MODEL, AWS_PROFILE")
	assertNotContains(t, "the console", console, "claude env (api)", "ca09-shell-profile", "shell.ca09.example")

	run := loadAuthRun(t, repoDir)
	if run.ClaudeAuth == nil || run.ClaudeAuth.Mode != "login" || !equalStrings(run.ClaudeAuth.APIEnv, apiEnvNames) {
		t.Errorf("claude_auth = %+v, want login with the removed names", run.ClaudeAuth)
	}
	assertNotContains(t, "summary.md", mustRunFile(t, repoDir, "summary.md"), "env: 3 variables")
}

// §12: the verify server runs the test command with the TestEnv alone, so the key the claude
// process holds does not reach it.
func TestClaudeAuth_VerifyServerGetsNoKey(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", authKey())
	repoDir := fakeexec.InitGitFixture(t)
	testEnv := filepath.Join(t.TempDir(), "test.env")
	envDir := t.TempDir()
	t.Setenv("FAKE_ENV_DIR", envDir)
	v := newVerifyRun(t, repoDir, "run_tests", func(o *configOptions) {
		o.testCommand = "env > '" + testEnv + "'"
	})

	stdout, stderr, err := runOrchestrator(t, repoDir, v.cfg, append(implementAndTest, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	if got := callEnv(t, envDir, "implement")["ANTHROPIC_API_KEY"]; got != authKey() {
		t.Fatalf("the implement call did not get the key in api mode")
	}
	run := loadQL(t, repoDir)
	if calls := run.Executions[0].VerifyCalls; len(calls) != 1 || calls[0].Tool != "run_tests" {
		t.Fatalf("verify_calls = %+v, want one run_tests", calls)
	}
	// The test command ran in the verify server (and again as the testing step); neither saw the key.
	if _, ok := readEnvDump(t, testEnv)["ANTHROPIC_API_KEY"]; ok {
		t.Error("the test command inherited ANTHROPIC_API_KEY")
	}
	assertNotContains(t, "the verify server's answers", readFile(t, v.verifyDir+"/verify_1.responses"), authKey())
}
