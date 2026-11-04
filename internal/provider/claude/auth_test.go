package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/provider"
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
		env, removed, err := buildEnv(base, domain.ClaudeAuthAPI, key, nil)
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
		env, removed, err := buildEnv(base, domain.ClaudeAuthLogin, "", nil)
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
		if _, _, err := buildEnv(base, domain.ClaudeAuthAPI, "", nil); err == nil {
			t.Error("api mode with no key must be an error")
		}
		if _, _, err := buildEnv(base, "bogus", "", nil); err == nil {
			t.Error("an unknown mode must be an error")
		}
	})
}

// SPEC §17.4: api_env is set in api mode, replacing an inherited value, and its names are
// removed in login mode; an empty map changes nothing.
func TestBuildEnv_APIEnv(t *testing.T) {
	key := testKey()
	base := []string{"PATH=/bin", "HOME=/home/x", "ANTHROPIC_BASE_URL=https://shell.example", "AWS_REGION=us-east-1",
		"CLAUDE_CODE_USE_BEDROCK=1"}
	apiEnv := map[string]string{
		"ANTHROPIC_BASE_URL":           "https://gw.example",
		"AWS_PROFILE":                  "claude-bedrock",
		"ANTHROPIC_DEFAULT_OPUS_MODEL": "bedrock/eu.anthropic.claude-opus-5-5",
	}

	t.Run("api sets and overrides", func(t *testing.T) {
		env, removed, err := buildEnv(base, domain.ClaudeAuthAPI, key, apiEnv)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(env, "\n")
		for name, want := range apiEnv {
			if v, ok := envValue(env, name); !ok || v != want {
				t.Errorf("%s = %q, %v; want %q", name, v, ok, want)
			}
			if n := strings.Count(joined, name+"="); n != 1 {
				t.Errorf("%s set %d times", name, n)
			}
		}
		for _, name := range []string{"PATH", "HOME", "AWS_REGION"} {
			if _, ok := envValue(env, name); !ok {
				t.Errorf("%s was not inherited", name)
			}
		}
		if v, _ := envValue(env, "ANTHROPIC_API_KEY"); v != key {
			t.Errorf("ANTHROPIC_API_KEY = %q, want the key", v)
		}
		if !slices.Equal(removed, []string{"CLAUDE_CODE_USE_BEDROCK"}) {
			t.Errorf("removed = %v, want only the §7 variable", removed)
		}
	})

	t.Run("login removes the names that were exported", func(t *testing.T) {
		env, removed, err := buildEnv(base, domain.ClaudeAuthLogin, "", apiEnv)
		if err != nil {
			t.Fatal(err)
		}
		for name := range apiEnv {
			if _, ok := envValue(env, name); ok {
				t.Errorf("%s reached the child in login mode", name)
			}
		}
		for _, name := range []string{"PATH", "HOME", "AWS_REGION", "CLAUDE_CODE_USE_BEDROCK"} {
			if _, ok := envValue(env, name); !ok {
				t.Errorf("%s was not inherited in login mode", name)
			}
		}
		// AWS_PROFILE and the alias were not exported, so only the base URL is named.
		if !slices.Equal(removed, []string{"ANTHROPIC_BASE_URL"}) {
			t.Errorf("removed = %v, want [ANTHROPIC_BASE_URL]", removed)
		}
	})

	t.Run("an empty map changes nothing", func(t *testing.T) {
		for _, mode := range []string{domain.ClaudeAuthAPI, domain.ClaudeAuthLogin} {
			wantEnv, wantRemoved, _ := buildEnv(base, mode, key, nil)
			env, removed, err := buildEnv(base, mode, key, map[string]string{})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(env, wantEnv) || !slices.Equal(removed, wantRemoved) {
				t.Errorf("%s: an empty api_env changed the environment: %v, %v", mode, env, removed)
			}
		}
	})

	t.Run("never the key or what outranks it", func(t *testing.T) {
		env, _, err := buildEnv(base, domain.ClaudeAuthAPI, key, map[string]string{"ANTHROPIC_API_KEY": "other", "ANTHROPIC_AUTH_TOKEN": "x"})
		if err != nil {
			t.Fatal(err)
		}
		if v, _ := envValue(env, "ANTHROPIC_API_KEY"); v != key {
			t.Errorf("api_env replaced the key")
		}
		if _, ok := envValue(env, "ANTHROPIC_AUTH_TOKEN"); ok {
			t.Error("api_env brought ANTHROPIC_AUTH_TOKEN back")
		}
	})
}

// SetAuth keeps its own copy: a later change to the caller's map does not reach the child.
func TestSetAuth_ClonesAPIEnv(t *testing.T) {
	apiEnv := map[string]string{"AWS_PROFILE": "a"}
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "ok"}}}
	c := New(fake)
	c.SetAuth(domain.ClaudeAuthAPI, testKey(), apiEnv)
	apiEnv["AWS_PROFILE"] = "b"
	if _, err := c.Execute(context.Background(), provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone}); err != nil {
		t.Fatal(err)
	}
	if v, _ := envValue(fake.Calls[0].Env, "AWS_PROFILE"); v != "a" {
		t.Errorf("AWS_PROFILE = %q, want the value SetAuth was given", v)
	}
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
	c.SetAuth(domain.ClaudeAuthAPI, key, nil)
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
	c.SetAuth(domain.ClaudeAuthLogin, "", nil)
	if got := c.IgnoredEnv(); len(got) != 0 {
		t.Errorf("login mode without api_env ignores nothing, got %v", got)
	}
	c.SetAuth(domain.ClaudeAuthAPI, testKey(), nil)
	got := c.IgnoredEnv()
	if !slices.Contains(got, "CLAUDE_CODE_USE_BEDROCK") || slices.Contains(got, "ANTHROPIC_API_KEY") {
		t.Errorf("IgnoredEnv = %v", got)
	}
	t.Setenv("ORCH_TEST_API_ENV_EXPORTED", "shell-value")
	c.SetAuth(domain.ClaudeAuthLogin, "", map[string]string{"ORCH_TEST_API_ENV_EXPORTED": "a", "ORCH_TEST_API_ENV_UNSET": "b"})
	if got := c.IgnoredEnv(); !slices.Equal(got, []string{"ORCH_TEST_API_ENV_EXPORTED"}) {
		t.Errorf("login IgnoredEnv = %v, want the exported api_env name only", got)
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
	c.SetAuth(domain.ClaudeAuthAPI, key, map[string]string{"AWS_PROFILE": "claude-bedrock"})
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
	if v, _ := envValue(env, "AWS_PROFILE"); v != "claude-bedrock" {
		t.Errorf("the probe's AWS_PROFILE = %q, want the api_env value", v)
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
	c.SetAuth(mode, key, nil)
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

// claude 2.1.287 does not retry an invalid key: it answers with a synthetic message whose
// top-level error is the category, and exits 1.
func TestExecute_AuthErrorAnswer(t *testing.T) {
	stdout := initEvent("ANTHROPIC_API_KEY") +
		`{"type":"assistant","message":{"id":"x","model":"<synthetic>","content":[{"type":"text","text":"Invalid API key · Fix external API key"}]},"error":"authentication_failed","is_api_error_message":true}` + "\n" +
		`{"type":"result","subtype":"success","is_error":true,"api_error_status":401,"result":"Invalid API key · Fix external API key"}` + "\n"
	_, err := executeUnder(domain.ClaudeAuthAPI, testKey(), execution.ProcessResult{Stdout: stdout, ExitCode: 1})
	var pe *apperr.ProviderError
	if !errors.As(err, &pe) || pe.Kind != apperr.FailureAuth || !strings.Contains(err.Error(), "authentication_failed") {
		t.Fatalf("err = %v, want FailureAuth naming authentication_failed", err)
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
	c.SetAuth(domain.ClaudeAuthAPI, testKey(), nil)
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
			c.SetAuth(domain.ClaudeAuthAPI, key, nil)
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

// retryingExecutor behaves like a CLI that keeps retrying a refused key: it streams its
// stdout to the sink, then runs until the context ends, and reports the context's error as
// the real executor does.
type retryingExecutor struct{ stdout string }

func (e retryingExecutor) Run(ctx context.Context, spec execution.Spec) (execution.ProcessResult, error) {
	if spec.StdoutSink != nil {
		_, _ = spec.StdoutSink.Write([]byte(e.stdout))
	}
	select {
	case <-ctx.Done():
		return execution.ProcessResult{Stdout: e.stdout, ExitCode: -1}, ctx.Err()
	case <-time.After(5 * time.Second):
		return execution.ProcessResult{Stdout: e.stdout}, errors.New("the call was not stopped")
	}
}

// DECISIONS D17: the CLI retries a 401 for minutes; the first auth api_retry ends the call.
func TestExecute_StopsOnTheFirstAuthRetry(t *testing.T) {
	stdout := initEvent("ANTHROPIC_API_KEY") + `{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"error_status":401,"error":"authentication_failed"}` + "\n"
	c := New(retryingExecutor{stdout: stdout})
	c.SetAuth(domain.ClaudeAuthAPI, testKey(), nil)
	start := time.Now()
	_, err := c.Execute(context.Background(), provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone})
	var pe *apperr.ProviderError
	if !errors.As(err, &pe) || pe.Kind != apperr.FailureAuth {
		t.Fatalf("err = %v, want FailureAuth", err)
	}
	if errors.Is(err, context.Canceled) || !strings.Contains(pe.Detail, "stopped at the CLI's first retry") {
		t.Errorf("the stop must read as an auth failure, not a cancellation: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("the call was not stopped early: %v", time.Since(start))
	}

	// A rate limit is retried by the CLI as before: the call is not stopped.
	limited := initEvent("ANTHROPIC_API_KEY") + `{"type":"system","subtype":"api_retry","attempt":1,"error":"rate_limit"}` + "\n"
	c = New(retryingExecutor{stdout: limited})
	c.SetAuth(domain.ClaudeAuthAPI, testKey(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = c.Execute(ctx, provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone})
	if !errors.As(err, &pe) || pe.Kind != apperr.FailureTimeout {
		t.Errorf("rate limit: err = %v, want the caller's timeout", err)
	}

	// The caller's own cancellation stays a cancellation.
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	_, err = c.Execute(ctx, provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone})
	if !errors.As(err, &pe) || pe.Kind != apperr.FailureCancelled {
		t.Errorf("caller cancel: err = %v, want FailureCancelled", err)
	}
}

func TestAuthRetryWatcher(t *testing.T) {
	auth := `{"type":"system","subtype":"api_retry","error":"billing_error"}` + "\n"
	cases := map[string][]string{
		"one write":         {auth},
		"split mid-line":    {auth[:17], auth[17:]},
		"byte by byte":      strings.Split(auth, ""),
		"after other lines": {initEvent("x") + `{"type":"assistant"}` + "\n", auth},
		"after a long line": {strings.Repeat("a", maxWatchedLine+10), `"api_retry"}` + "\n" + auth},
	}
	for name, writes := range cases {
		stops := 0
		w := &authRetryWatcher{stop: func() { stops++ }}
		for _, p := range writes {
			if n, err := w.Write([]byte(p)); n != len(p) || err != nil {
				t.Fatalf("%s: Write = %d, %v", name, n, err)
			}
		}
		_, _ = w.Write([]byte(auth))
		if stops != 1 {
			t.Errorf("%s: stop called %d times, want 1", name, stops)
		}
	}
	for name, stream := range map[string]string{
		"rate limit":          `{"type":"system","subtype":"api_retry","error":"rate_limit"}` + "\n",
		"no newline yet":      strings.TrimSuffix(auth, "\n"),
		"category as text":    `{"type":"assistant","message":{"content":[{"type":"text","text":"api_retry authentication_failed"}]}}` + "\n",
		"tail of a long line": strings.Repeat("a", maxWatchedLine+10) + `,"type":"system","subtype":"api_retry","error":"billing_error"}` + "\n",
	} {
		stops := 0
		w := &authRetryWatcher{stop: func() { stops++ }}
		_, _ = w.Write([]byte(stream))
		if stops != 0 {
			t.Errorf("%s: stop called", name)
		}
	}
}
