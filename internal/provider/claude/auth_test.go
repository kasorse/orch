package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
)

// testKey is key-shaped but assembled at run time, so no token-shaped literal is in the
// source (CLAUDE.md).
func testKey() string {
	return "sk-" + "ant-api03-" + strings.Repeat("k", 24)
}

func envValue(env []string, name string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

// SPEC §7: every row, both modes, over a fixed environment.
func TestBuildEnv_TableOfSpec7(t *testing.T) {
	key := testKey()
	removedInAPI := []string{
		"ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
		"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_PROFILE", "ANTHROPIC_FEDERATION_RULE_ID",
	}
	base := []string{"PATH=/bin", "HOME=/home/x", "ANTHROPIC_BASE_URL=https://gw.example", "ANTHROPIC_API_KEY=shell-value"}
	for _, name := range removedInAPI {
		base = append(base, name+"=1")
	}

	t.Run("api", func(t *testing.T) {
		env, removed, err := buildEnv(base, domain.ClaudeAuthAPI, key)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range removedInAPI {
			if _, ok := envValue(env, name); ok {
				t.Errorf("%s reached the child in api mode", name)
			}
		}
		if v, ok := envValue(env, "ANTHROPIC_API_KEY"); !ok || v != key {
			t.Errorf("ANTHROPIC_API_KEY = %q, %v; want the key", v, ok)
		}
		if n := strings.Count(strings.Join(env, "\n"), "ANTHROPIC_API_KEY="); n != 1 {
			t.Errorf("ANTHROPIC_API_KEY set %d times", n)
		}
		for _, name := range []string{"PATH", "HOME", "ANTHROPIC_BASE_URL"} {
			if _, ok := envValue(env, name); !ok {
				t.Errorf("%s was not inherited", name)
			}
		}
		want := slices.Sorted(slices.Values(removedInAPI))
		if !slices.Equal(removed, want) {
			t.Errorf("removed = %v, want %v", removed, want)
		}
	})

	t.Run("login", func(t *testing.T) {
		env, removed, err := buildEnv(base, domain.ClaudeAuthLogin, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := envValue(env, "ANTHROPIC_API_KEY"); ok {
			t.Error("ANTHROPIC_API_KEY reached the child in login mode")
		}
		for _, name := range append([]string{"PATH", "HOME", "ANTHROPIC_BASE_URL"}, removedInAPI...) {
			if _, ok := envValue(env, name); !ok {
				t.Errorf("%s was not inherited in login mode", name)
			}
		}
		if len(removed) != 0 {
			t.Errorf("removed = %v, want none", removed)
		}
	})

	t.Run("api without a key, unknown mode", func(t *testing.T) {
		if _, _, err := buildEnv(base, domain.ClaudeAuthAPI, ""); err == nil {
			t.Error("api mode with no key must be an error")
		}
		if _, _, err := buildEnv(base, "bogus", ""); err == nil {
			t.Error("an unknown mode must be an error")
		}
	})
}

// api_key_env with a custom name: the child gets the key as ANTHROPIC_API_KEY and never under
// the name the config gave it (ScrubCredentials removes that).
func TestExecute_CustomKeyEnvReachesTheChildAsAnthropicAPIKey(t *testing.T) {
	key := testKey()
	t.Setenv("ORCH_TEST_CLAUDE_KEY", key)
	t.Setenv("ANTHROPIC_API_KEY", "shell-value")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "gateway-token")
	execution.ScrubCredentials("ORCH_TEST_CLAUDE_KEY")

	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "ok"}}}
	c := New(fake)
	c.SetAuth(domain.ClaudeAuthAPI, key)
	if _, err := c.Execute(context.Background(), provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone}); err != nil {
		t.Fatal(err)
	}
	env := fake.Calls[0].Env
	if v, _ := envValue(env, "ANTHROPIC_API_KEY"); v != key {
		t.Errorf("ANTHROPIC_API_KEY = %q, want the key", v)
	}
	if _, ok := envValue(env, "ORCH_TEST_CLAUDE_KEY"); ok {
		t.Error("the key leaked under its own variable name")
	}
	if _, ok := envValue(env, "ANTHROPIC_AUTH_TOKEN"); ok {
		t.Error("ANTHROPIC_AUTH_TOKEN outranks the key and must be removed in api mode")
	}
	for _, a := range fake.Calls[0].Args {
		if strings.Contains(a, key) {
			t.Errorf("the key is in argv: %q", a)
		}
	}
	if strings.Contains(fake.Calls[0].Stdin, key) {
		t.Error("the key is in the prompt")
	}
}

func TestIgnoredEnvNamesRemovedVariablesOnly(t *testing.T) {
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	c := New(&execution.FakeProcessExecutor{})
	if got := c.IgnoredEnv(); got != nil {
		t.Errorf("IgnoredEnv before SetAuth = %v", got)
	}
	c.SetAuth(domain.ClaudeAuthLogin, "")
	if got := c.IgnoredEnv(); len(got) != 0 {
		t.Errorf("login mode ignores nothing, got %v", got)
	}
	c.SetAuth(domain.ClaudeAuthAPI, testKey())
	got := c.IgnoredEnv()
	if !slices.Contains(got, "CLAUDE_CODE_USE_BEDROCK") || slices.Contains(got, "ANTHROPIC_API_KEY") {
		t.Errorf("IgnoredEnv = %v", got)
	}
}

func TestClient_WithoutAuthRefusesToRun(t *testing.T) {
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "ok"}}}
	c := New(fake)
	if _, err := c.Execute(context.Background(), provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone}); err == nil {
		t.Error("Execute ran without a credential mode")
	}
	if _, _, err := c.CLIVersion(context.Background()); err == nil {
		t.Error("CLIVersion ran without a credential mode")
	}
	if len(fake.Calls) != 0 {
		t.Errorf("a process ran: %d calls", len(fake.Calls))
	}
}

func TestCLIVersion_RunsUnderTheModesEnvironment(t *testing.T) {
	key := testKey()
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "gateway-token")
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "2.1.299 (Claude Code)"}}}
	c := New(fake)
	c.SetAuth(domain.ClaudeAuthAPI, key)
	if _, _, err := c.CLIVersion(context.Background()); err != nil {
		t.Fatal(err)
	}
	env := fake.Calls[0].Env
	if v, _ := envValue(env, "ANTHROPIC_API_KEY"); v != key {
		t.Errorf("the probe's ANTHROPIC_API_KEY = %q, want the key", v)
	}
	if _, ok := envValue(env, "ANTHROPIC_AUTH_TOKEN"); ok {
		t.Error("the probe inherited ANTHROPIC_AUTH_TOKEN in api mode")
	}
}

const (
	okResult  = `{"type":"result","subtype":"success","is_error":false,"result":"done","num_turns":1}` + "\n"
	errResult = `{"type":"result","subtype":"success","is_error":true,"result":"API Error: 402 billing","num_turns":1}` + "\n"
)

func initEvent(source string) string {
	return fmt.Sprintf(`{"type":"system","subtype":"init","apiKeySource":%q,"cwd":"/repo"}`+"\n", source)
}

func executeUnder(mode, key string, res execution.ProcessResult) (provider.Result, error) {
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{res}}
	c := New(fake)
	c.SetAuth(mode, key)
	return c.Execute(context.Background(), provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone})
}

// SPEC §8, with the exit code 0 and an answer that looks fine.
func TestExecute_ChecksAPIKeySourceAgainstTheMode(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		stdout  string
		failure string // "" = the call is fine
		source  string // recorded in the report
	}{
		{"api used", domain.ClaudeAuthAPI, initEvent("ANTHROPIC_API_KEY") + okResult, "", "ANTHROPIC_API_KEY"},
		{"api reported none", domain.ClaudeAuthAPI, initEvent("none") + okResult, `reported "none"`, "none"},
		{"api reported apiKeyHelper", domain.ClaudeAuthAPI, initEvent("apiKeyHelper") + okResult, `reported "apiKeyHelper"`, "apiKeyHelper"},
		{"api reported managed key", domain.ClaudeAuthAPI, initEvent("/login managed key") + okResult, `reported "/login managed key"`, "/login managed key"},
		{"login used the key", domain.ClaudeAuthLogin, initEvent("ANTHROPIC_API_KEY") + okResult, "used an API key", "ANTHROPIC_API_KEY"},
		{"login reported none", domain.ClaudeAuthLogin, initEvent("none") + okResult, "", "none"},
		{"login reported apiKeyHelper", domain.ClaudeAuthLogin, initEvent("apiKeyHelper") + okResult, "", "apiKeyHelper"},
		{"api without init", domain.ClaudeAuthAPI, okResult, "", ""},
		{"login without init", domain.ClaudeAuthLogin, okResult, "", ""},
		{"api plain text", domain.ClaudeAuthAPI, "plain answer", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := ""
			if tc.mode == domain.ClaudeAuthAPI {
				key = testKey()
			}
			res, err := executeUnder(tc.mode, key, execution.ProcessResult{Stdout: tc.stdout})
			if tc.source != "" && (res.Call == nil || res.Call.APIKeySource != tc.source) {
				t.Errorf("call = %+v, want api_key_source %q", res.Call, tc.source)
			}
			if tc.source == "" && res.Call != nil && res.Call.APIKeySource != "" {
				t.Errorf("api_key_source = %q, want none", res.Call.APIKeySource)
			}
			if tc.failure == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var pe *apperr.ProviderError
			if !errors.As(err, &pe) || pe.Kind != apperr.FailureAuth {
				t.Fatalf("err = %v, want a FailureAuth ProviderError", err)
			}
			if msg := err.Error(); !strings.Contains(msg, tc.failure) || !strings.Contains(msg, "("+tc.mode+")") {
				t.Errorf("message should name the mode and %q:\n%s", tc.failure, msg)
			}
		})
	}
}

// SPEC §9: an auth-category api_retry is FailureAuth; a rate limit is not.
func TestExecute_AuthRetryCategories(t *testing.T) {
	retry := func(category string) string {
		return fmt.Sprintf(`{"type":"system","subtype":"api_retry","attempt":1,"error":%q}`+"\n", category)
	}
	for _, category := range []string{"authentication_failed", "oauth_org_not_allowed", "account_on_hold", "billing_error"} {
		t.Run(category, func(t *testing.T) {
			stdout := initEvent("ANTHROPIC_API_KEY") + retry(category) + errResult
			_, err := executeUnder(domain.ClaudeAuthAPI, testKey(), execution.ProcessResult{Stdout: stdout, ExitCode: 1})
			var pe *apperr.ProviderError
			if !errors.As(err, &pe) || pe.Kind != apperr.FailureAuth {
				t.Fatalf("err = %v, want FailureAuth", err)
			}
			if msg := err.Error(); !strings.Contains(msg, category) || !strings.Contains(msg, "(api)") {
				t.Errorf("message should name the category and the mode:\n%s", msg)
			}
			// A retry that then succeeded is still an authentication problem worth stopping for.
			_, err = executeUnder(domain.ClaudeAuthLogin, "", execution.ProcessResult{Stdout: initEvent("none") + retry(category) + okResult})
			if !errors.As(err, &pe) || pe.Kind != apperr.FailureAuth || !strings.Contains(err.Error(), "(login)") {
				t.Errorf("login: err = %v, want FailureAuth naming login", err)
			}
		})
	}
	for _, category := range []string{"rate_limit", "overloaded", "server_error"} {
		stdout := initEvent("ANTHROPIC_API_KEY") + retry(category) + okResult
		if _, err := executeUnder(domain.ClaudeAuthAPI, testKey(), execution.ProcessResult{Stdout: stdout}); err != nil {
			t.Errorf("%s: unexpected error %v", category, err)
		}
	}
}

// A timeout whose stream already showed the credential was refused is reported as that.
func TestExecute_AuthFailureWinsOverTimeout(t *testing.T) {
	stdout := initEvent("ANTHROPIC_API_KEY") + `{"type":"system","subtype":"api_retry","error":"authentication_failed"}` + "\n"
	fake := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: stdout, ExitCode: -1}},
		Errs:    []error{context.DeadlineExceeded},
	}
	c := New(fake)
	c.SetAuth(domain.ClaudeAuthAPI, testKey())
	_, err := c.Execute(context.Background(), provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone})
	var pe *apperr.ProviderError
	if !errors.As(err, &pe) || pe.Kind != apperr.FailureAuth || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want FailureAuth wrapping the deadline", err)
	}
}

func TestParseStream_APIKeySourceAndAuthRetries(t *testing.T) {
	for _, source := range []string{"ANTHROPIC_API_KEY", "apiKeyHelper", "/login managed key", "none"} {
		rep := parseStream(initEvent(source) + okResult)
		if rep.apiKeySource != source || rep.call == nil || rep.call.APIKeySource != source || rep.authErr != "" {
			t.Errorf("%s: report = %+v, call = %+v", source, rep, rep.call)
		}
	}

	billing := initEvent("ANTHROPIC_API_KEY") + `{"type":"system","subtype":"api_retry","attempt":2,"error":"billing_error"}` + "\n"
	if rep := parseStream(billing); rep.authErr != "billing_error" {
		t.Errorf("authErr = %q, want billing_error", rep.authErr)
	}

	// No init event: not checked, recorded as "".
	rep := parseStream(okResult)
	if rep.apiKeySource != "" || rep.authErr != "" || rep.call != nil {
		t.Errorf("no init: report = %+v, call = %+v", rep, rep.call)
	}

	// An init whose apiKeySource is not a string, or an error that is an object, costs the
	// field and not the event.
	odd := `{"type":"system","subtype":"init","apiKeySource":{"x":1},"mcp_servers":[{"name":"m","status":"failed"}]}` + "\n" +
		`{"type":"system","subtype":"api_retry","error":{"type":"billing_error"}}` + "\n"
	rep = parseStream(odd)
	if rep.apiKeySource != "" || rep.authErr != "" || rep.call == nil || len(rep.call.MCPFailed) != 1 {
		t.Errorf("odd shapes: report = %+v, call = %+v", rep, rep.call)
	}
}

// The key must not leave the adapter when the CLI echoes it: not in the error, the result,
// the live stream or the activity.
func TestExecute_DoesNotLeakTheKey(t *testing.T) {
	key := testKey()
	echo := func(s string) string { return strings.ReplaceAll(s, "KEY", key) }
	tool := echo(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/repo/KEY"}}]}}` + "\n")
	cases := []struct {
		name string
		res  execution.ProcessResult
		err  error
	}{
		{"auth mismatch", execution.ProcessResult{Stdout: initEvent("none") + tool + echo(`{"type":"result","result":"KEY","is_error":false}`+"\n"), Stderr: echo("auth with KEY\n")}, nil},
		{"auth retry", execution.ProcessResult{Stdout: initEvent("ANTHROPIC_API_KEY") + echo(`{"type":"system","subtype":"api_retry","error":"authentication_failed","detail":"KEY"}`+"\n"), Stderr: echo("bad KEY\n"), ExitCode: 1}, nil},
		{"plain exit", execution.ProcessResult{Stdout: echo("Invalid KEY"), Stderr: echo("KEY"), ExitCode: 1}, nil},
		{"timeout", execution.ProcessResult{Stdout: tool, Stderr: echo("KEY"), ExitCode: -1}, fmt.Errorf("killed: %w", context.DeadlineExceeded)},
		{"spawn error", execution.ProcessResult{}, errors.New(echo("cannot start with KEY"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errs []error
			if tc.err != nil {
				errs = []error{tc.err}
			}
			fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{tc.res}, Errs: errs}
			c := New(fake)
			c.SetAuth(domain.ClaudeAuthAPI, key)
			var live bytes.Buffer
			var activity []string
			res, err := c.Execute(context.Background(), provider.Request{
				Prompt: "p", Model: "m", Authority: domain.AuthorityRead, WorkingDir: "/repo",
				Stream: &live, Activity: func(s string) { activity = append(activity, s) },
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			var pe *apperr.ProviderError
			if !errors.As(err, &pe) {
				t.Fatalf("err = %T %v", err, err)
			}
			surfaces := map[string]string{
				"Error()": err.Error(), "Stdout": pe.Stdout, "Stderr": pe.Stderr,
				"Output": res.Output, "Result.Stderr": res.Stderr, "live": live.String(),
				"activity": strings.Join(activity, "|"), "Detail": pe.Detail,
			}
			if pe.Err != nil {
				surfaces["Err"] = pe.Err.Error()
			}
			for name, s := range surfaces {
				if strings.Contains(s, key) {
					t.Errorf("%s contains the key:\n%s", name, s)
				}
			}
			if tc.name == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Error("redaction lost the error chain")
			}
		})
	}
}

// A key split across the CLI's writes is still caught by the live stream.
func TestRedactWriter_KeySplitAcrossWrites(t *testing.T) {
	key := testKey()
	var out bytes.Buffer
	w := &redactWriter{w: &out, redact: redactor(key)}
	line := "token " + key + " end\n"
	_, _ = w.Write([]byte(line[:20]))
	_, _ = w.Write([]byte(line[20:]))
	_, _ = w.Write([]byte("tail " + key))
	w.Flush()
	if strings.Contains(out.String(), key) || !strings.Contains(out.String(), "token "+redacted+" end\n") || !strings.HasSuffix(out.String(), "tail "+redacted) {
		t.Errorf("output = %q", out.String())
	}
}
