package action

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
	"orchestrator/internal/testexec"
	"orchestrator/internal/verify"
)

const verifyTestExe = "/usr/bin/orchestrator"

// sessionClient stands in for the implement session: run gets the verify directory the MCP
// config names, and writes into it what the verify server would have.
type sessionClient struct {
	run     func(ctx context.Context, dir string) (provider.Result, error)
	lastReq provider.Request
	dir     string
	called  bool
}

func (c *sessionClient) Supports(domain.Authority) bool { return true }

func (c *sessionClient) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	c.called = true
	c.lastReq = req
	var doc struct {
		MCPServers map[string]struct {
			Args []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(req.Extensions.MCPConfig), &doc); err == nil {
		if args := doc.MCPServers["orch"].Args; len(args) == 3 {
			c.dir = args[2]
		}
	}
	if c.run == nil {
		return provider.Result{Output: `{"summary":"done"}`}, nil
	}
	return c.run(ctx, c.dir)
}

// verifyGuard is a guard whose state is one verify.Spec accepts: the real State has no
// exported fields, so it is decoded from its JSON.
type verifyGuard struct {
	changed []string
}

func (g *verifyGuard) Snapshot(context.Context) (GuardState, error) {
	var s GuardState
	err := json.Unmarshal([]byte(`{"patterns":[".github/**"],"git_fingerprint":"f","protected":{}}`), &s)
	return s, err
}

func (g *verifyGuard) Changed(context.Context, GuardState) ([]string, error) { return g.changed, nil }

func newVerifyDispatcher(client provider.Client) *Dispatcher {
	d := newDispatcher(client)
	d.Config.Actions.Implement.Verify = true
	d.Runner = &testexec.Runner{Sandbox: &testexec.Sandbox{Home: "/home/u", RepoRoot: "/repo", GitDirs: []string{"/repo/.git"}}}
	d.VerifyExecutable = verifyTestExe
	d.Guard = &verifyGuard{}
	return d
}

func verifyRun() *domain.Run {
	return &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}, PreRunRef: "abc123"}
}

func writeVerifyFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Errorf("write %s: %v", name, err)
	}
}

func assertGone(t *testing.T, dir string) {
	t.Helper()
	if dir == "" {
		t.Fatal("the session was never given a verify directory")
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the verify directory %s still exists: %v", dir, err)
	}
}

const (
	callTests   = `{"tool":"run_tests","exit_code":1,"success":false,"duration":2000000000}`
	callCheck   = `{"tool":"run_check","check":"vet","exit_code":0,"success":true,"duration":1000000000,"output_cut":true}`
	callRefused = `{"tool":"run_tests","exit_code":0,"success":false,"duration":0,"refused":"limit reached"}`
)

func TestExecuteImplement_VerifyExtensionsAndSpec(t *testing.T) {
	for _, withChecks := range []bool{false, true} {
		client := &sessionClient{}
		d := newVerifyDispatcher(client)
		if withChecks {
			d.Config.Checks = []config.CheckConfig{{Name: "vet", Command: "go vet ./..."}}
		}
		var spec *verify.Spec
		var dirMode os.FileMode
		client.run = func(_ context.Context, dir string) (provider.Result, error) {
			info, err := os.Stat(dir)
			if err != nil {
				t.Errorf("stat %s: %v", dir, err)
			} else {
				dirMode = info.Mode().Perm()
			}
			if spec, err = verify.ReadSpec(dir); err != nil {
				t.Errorf("the server could not read its input: %v", err)
			}
			return provider.Result{Output: `{"summary":"done"}`}, nil
		}

		if _, err := d.Execute(context.Background(), domain.ActionImplement, verifyRun()); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		ext := client.lastReq.Extensions
		wantTools := "mcp__orch__run_tests"
		if withChecks {
			wantTools += ",mcp__orch__run_check"
		}
		if got := strings.Join(ext.MCPTools, ","); got != wantTools {
			t.Errorf("checks=%v: tools = %s, want %s", withChecks, got, wantTools)
		}
		want := `{"mcpServers":{"orch":{"args":["mcp-verify","--dir","` + client.dir + `"],"command":"` + verifyTestExe + `"}}}`
		if ext.MCPConfig != want {
			t.Errorf("MCPConfig = %s, want %s", ext.MCPConfig, want)
		}
		if !filepath.IsAbs(client.dir) || !strings.HasPrefix(filepath.Base(client.dir), "orch-verify-") {
			t.Errorf("verify directory = %q", client.dir)
		}
		if dirMode != 0o700 {
			t.Errorf("verify directory mode = %o, want 700", dirMode)
		}
		if strings.Contains(client.lastReq.Prompt, "Extra tools are available") {
			t.Errorf("the verify tools are not the operator's: no knowledge-graph hint\n%s", client.lastReq.Prompt)
		}
		if !strings.Contains(client.lastReq.Prompt, "`mcp__orch__run_tests`") || strings.Contains(client.lastReq.Prompt, "run_check") != withChecks {
			t.Errorf("checks=%v: the prompt lacks the verify paragraph or names run_check wrongly\n%s", withChecks, client.lastReq.Prompt)
		}
		if spec == nil {
			t.Fatal("no spec was written")
		}
		if spec.TestCommand != "go test ./..." || spec.PreRunRef != "abc123" || spec.RepoRoot != "/repo" ||
			spec.MaxCalls != 8 || spec.CallTimeout != 10*time.Minute || len(spec.Checks) != map[bool]int{false: 0, true: 1}[withChecks] ||
			spec.Sandbox == nil || spec.Sandbox.RepoRoot != "/repo" || len(spec.Before.Patterns()) == 0 {
			t.Errorf("spec = %+v", spec)
		}
		for _, kv := range spec.TestEnv {
			if strings.HasPrefix(kv, "ORCH_") {
				t.Errorf("test_env carries %s", kv)
			}
		}
		assertGone(t, client.dir)
	}
}

func TestExecuteImplement_VerifyOffAddsNothing(t *testing.T) {
	client := &fakeClient{output: `{"summary":"done"}`}
	d := newDispatcher(client)
	outcome, err := d.Execute(context.Background(), domain.ActionImplement, verifyRun())
	if err != nil {
		t.Fatal(err)
	}
	if client.lastReq.Extensions.MCPConfig != "" || len(client.lastReq.Extensions.MCPTools) != 0 {
		t.Errorf("extensions = %+v", client.lastReq.Extensions)
	}
	if strings.Contains(client.lastReq.Prompt, "mcp__orch") || outcome.VerifyCalls != nil || outcome.VerifyLogs != nil {
		t.Errorf("verify is off, but the step shows it: %+v", outcome)
	}
}

func TestExecuteImplement_VerifyMergesOperatorExtensions(t *testing.T) {
	client := &sessionClient{}
	d := newVerifyDispatcher(client)
	d.Config.Extensions = config.ExtensionsConfig{MCP: map[string]config.MCPServerConfig{
		"graphify": {Command: "graphify-mcp", Tools: []string{"query_graph"}, Actions: []string{"implement"}},
	}}
	if _, err := d.Execute(context.Background(), domain.ActionImplement, verifyRun()); err != nil {
		t.Fatal(err)
	}
	ext := client.lastReq.Extensions
	if got := strings.Join(ext.MCPTools, ","); got != "mcp__graphify__query_graph,mcp__orch__run_tests" {
		t.Errorf("tools = %s", got)
	}
	if !strings.Contains(ext.MCPConfig, `"graphify"`) || !strings.Contains(ext.MCPConfig, `"orch"`) {
		t.Errorf("MCPConfig = %s", ext.MCPConfig)
	}
	// The hint names the operator's tools only.
	if !strings.Contains(client.lastReq.Prompt, "Extra tools are available in this step: mcp__graphify__query_graph. ") {
		t.Errorf("hint missing or wrong\n%s", client.lastReq.Prompt)
	}
}

func TestExecuteImplement_VerifyRecordsCallsAndLogs(t *testing.T) {
	client := &sessionClient{}
	d := newVerifyDispatcher(client)
	client.run = func(_ context.Context, dir string) (provider.Result, error) {
		writeVerifyFile(t, dir, "calls.jsonl", callTests+"\n"+callCheck+"\n"+callRefused+"\n")
		writeVerifyFile(t, dir, "call-1.stdout.log", "FAIL: TestX\n")
		writeVerifyFile(t, dir, "call-1.stderr.log", "exit status 1\n")
		writeVerifyFile(t, dir, "call-2.stdout.log", "ok\n")
		return provider.Result{Output: `{"summary":"done"}`}, nil
	}
	outcome, err := d.Execute(context.Background(), domain.ActionImplement, verifyRun())
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.VerifyCalls) != 3 {
		t.Fatalf("VerifyCalls = %+v", outcome.VerifyCalls)
	}
	if c := outcome.VerifyCalls[1]; c.Tool != domain.VerifyToolRunCheck || c.Check != "vet" || !c.Success || !c.OutputCut || c.Duration != time.Second {
		t.Errorf("second call = %+v", c)
	}
	if c := outcome.VerifyCalls[2]; c.Refused != "limit reached" {
		t.Errorf("third call = %+v", c)
	}
	want := []domain.CheckOutput{
		{Name: "verify-1", Stdout: "FAIL: TestX\n", Stderr: "exit status 1\n"},
		{Name: "verify-2", Stdout: "ok\n"},
	}
	if len(outcome.VerifyLogs) != 2 || outcome.VerifyLogs[0] != want[0] || outcome.VerifyLogs[1] != want[1] {
		t.Errorf("VerifyLogs = %+v, want %+v", outcome.VerifyLogs, want)
	}
	assertGone(t, client.dir)
}

func TestExecuteImplement_VerifyViolationFailsTheStep(t *testing.T) {
	client := &sessionClient{}
	d := newVerifyDispatcher(client)
	client.run = func(_ context.Context, dir string) (provider.Result, error) {
		writeVerifyFile(t, dir, "calls.jsonl", callTests+"\n"+callRefused+"\n")
		writeVerifyFile(t, dir, "violation", ".github/workflows/ci.yml\n.git (config or hooks)\n")
		return provider.Result{Output: `{"summary":"done"}`}, nil
	}
	outcome, err := d.Execute(context.Background(), domain.ActionImplement, verifyRun())
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) {
		t.Fatalf("want a *apperr.WorkflowError, got %T: %v", err, err)
	}
	for _, want := range []string{
		"the model's code changed protected files during a verify call",
		".github/workflows/ci.yml", "Restore point: abc123", "Do not run git",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	if len(outcome.VerifyCalls) != 2 {
		t.Errorf("the calls must be recorded on the failed step, got %+v", outcome.VerifyCalls)
	}
	if outcome.Provider == nil || outcome.RawStdout == "" {
		t.Errorf("the outcome must still carry the call's output, got %+v", outcome)
	}
	assertGone(t, client.dir)
}

func TestExecuteImplement_VerifyMalformedCallsFailTheStep(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":      callTests + "\n{oops\n",
		"unknown field": callTests + "\n" + `{"tool":"run_tests","success":true,"extra":1}` + "\n",
		"unknown tool":  callTests + "\n" + `{"tool":"rm","success":true}` + "\n",
		"two objects":   callTests + "\n" + callTests + " " + callTests + "\n",
		"too many":      strings.Repeat(callRefused+"\n", domain.MaxVerifyCalls+1),
	} {
		t.Run(name, func(t *testing.T) {
			client := &sessionClient{}
			d := newVerifyDispatcher(client)
			client.run = func(_ context.Context, dir string) (provider.Result, error) {
				writeVerifyFile(t, dir, "calls.jsonl", content)
				return provider.Result{Output: `{"summary":"done"}`}, nil
			}
			outcome, err := d.Execute(context.Background(), domain.ActionImplement, verifyRun())
			var wfErr *apperr.WorkflowError
			if !errors.As(err, &wfErr) || !strings.Contains(err.Error(), "calls.jsonl") {
				t.Fatalf("want a WorkflowError naming calls.jsonl, got %v", err)
			}
			if name != "too many" && len(outcome.VerifyCalls) != 1 {
				t.Errorf("the lines before the bad one are kept, got %+v", outcome.VerifyCalls)
			}
			assertGone(t, client.dir)
		})
	}
}

func TestExecuteImplement_VerifyDoesNotFollowSymlinks(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte(callTests+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &sessionClient{}
	d := newVerifyDispatcher(client)
	client.run = func(_ context.Context, dir string) (provider.Result, error) {
		if err := os.Symlink(secret, filepath.Join(dir, "calls.jsonl")); err != nil {
			t.Error(err)
		}
		return provider.Result{Output: `{"summary":"done"}`}, nil
	}
	outcome, err := d.Execute(context.Background(), domain.ActionImplement, verifyRun())
	if err == nil || !strings.Contains(err.Error(), "calls.jsonl") || len(outcome.VerifyCalls) != 0 {
		t.Errorf("a symlinked calls.jsonl must fail the step unread, got %v / %+v", err, outcome.VerifyCalls)
	}
	assertGone(t, client.dir)
	if _, err := os.Stat(secret); err != nil {
		t.Errorf("the link's target must not be touched: %v", err)
	}
}

func TestExecuteImplement_VerifyProviderErrorKeepsTheCalls(t *testing.T) {
	boom := errors.New("provider died")
	client := &sessionClient{}
	d := newVerifyDispatcher(client)
	client.run = func(_ context.Context, dir string) (provider.Result, error) {
		writeVerifyFile(t, dir, "calls.jsonl", callTests+"\n")
		writeVerifyFile(t, dir, "call-1.stdout.log", "FAIL\n")
		return provider.Result{Output: "partial"}, boom
	}
	outcome, err := d.Execute(context.Background(), domain.ActionImplement, verifyRun())
	if !errors.Is(err, boom) {
		t.Fatalf("the provider's error must be kept, got %v", err)
	}
	if len(outcome.VerifyCalls) != 1 || len(outcome.VerifyLogs) != 1 || outcome.RawStdout != "partial" {
		t.Errorf("outcome = %+v", outcome)
	}
	assertGone(t, client.dir)
}

func TestExecuteImplement_VerifyDirRemovedOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &sessionClient{}
	d := newVerifyDispatcher(client)
	client.run = func(ctx context.Context, dir string) (provider.Result, error) {
		writeVerifyFile(t, dir, "calls.jsonl", callTests+"\n")
		cancel()
		<-ctx.Done()
		return provider.Result{}, ctx.Err()
	}
	outcome, err := d.Execute(ctx, domain.ActionImplement, verifyRun())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want the cancellation, got %v", err)
	}
	if len(outcome.VerifyCalls) != 1 {
		t.Errorf("VerifyCalls = %+v", outcome.VerifyCalls)
	}
	assertGone(t, client.dir)
}

func TestExecuteImplement_VerifyNeedsTheSandbox(t *testing.T) {
	for name, runner := range map[string]*testexec.Runner{"no runner": nil, "no sandbox": {}} {
		t.Run(name, func(t *testing.T) {
			client := &sessionClient{}
			d := newVerifyDispatcher(client)
			d.Runner = runner
			_, err := d.Execute(context.Background(), domain.ActionImplement, verifyRun())
			var wfErr *apperr.WorkflowError
			if !errors.As(err, &wfErr) || !strings.Contains(err.Error(), "sandbox") {
				t.Fatalf("want a WorkflowError about the sandbox, got %v", err)
			}
			if client.called {
				t.Error("the session must not start without a sandbox for verify")
			}
		})
	}
}

func TestReadVerifyDir_Bounds(t *testing.T) {
	dir := t.TempDir()
	if v := readVerifyDir(dir); v.err != nil || len(v.calls) != 0 || len(v.violation) != 0 {
		t.Errorf("an empty directory holds nothing: %+v", v)
	}

	var long strings.Builder
	for i := 0; i < maxVerifyViolationLines+5; i++ {
		long.WriteString(strings.Repeat("p", 400) + "\n")
	}
	writeVerifyFile(t, dir, "violation", long.String())
	writeVerifyFile(t, dir, "calls.jsonl", callTests+"\n")
	writeVerifyFile(t, dir, "call-1.stdout.log", strings.Repeat("x", maxVerifyLogBytes+10))
	v := readVerifyDir(dir)
	if len(v.violation) != maxVerifyViolationLines+1 || !strings.HasSuffix(v.violation[0], "...") || v.violation[len(v.violation)-1] != "(more not shown)" {
		t.Errorf("violation = %d lines, first %q", len(v.violation), v.violation[0])
	}
	if len(v.logs) != 1 || !strings.Contains(v.logs[0].Stdout, "[cut:") || len(v.logs[0].Stdout) > maxVerifyLogBytes+100 {
		t.Errorf("a long log must be cut and marked: %d bytes", len(v.logs[0].Stdout))
	}
}

// mcpClient is a session whose adapter passes an MCP server to its CLI, as claude and codex do.
type mcpClient struct{ sessionClient }

func (c *mcpClient) SupportsMCP() bool { return true }

func TestExecuteImplement_ReplaceIsOnWheneverTheProviderSpeaksMCP(t *testing.T) {
	client := &mcpClient{}
	d := newDispatcher(client)
	d.VerifyExecutable = verifyTestExe
	d.Guard = &verifyGuard{}
	d.Config.Context.ExtraDeny = []string{"vault/**"}
	var spec *verify.Spec
	client.run = func(_ context.Context, dir string) (provider.Result, error) {
		var err error
		if spec, err = verify.ReadSpec(dir); err != nil {
			t.Errorf("the server could not read its input: %v", err)
		}
		writeVerifyFile(t, dir, verify.CallsFile, `{"tool":"replace","exit_code":0,"success":true,"duration":1000,"files":3,"replacements":7}`+"\n")
		return provider.Result{Output: `{"summary":"done"}`}, nil
	}

	outcome, err := d.Execute(context.Background(), domain.ActionImplement, verifyRun())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := strings.Join(client.lastReq.Extensions.MCPTools, ","); got != "mcp__orch__replace" {
		t.Errorf("tools = %s, want replace alone without verify", got)
	}
	if spec == nil || spec.Tests() || spec.Replace == nil || strings.Join(spec.Replace.ExtraDeny, ",") != "vault/**" || spec.Sandbox != nil {
		t.Errorf("spec = %+v", spec)
	}
	if p := client.lastReq.Prompt; !strings.Contains(p, "`mcp__orch__replace`") || strings.Contains(p, "run_tests") {
		t.Errorf("the prompt does not name replace alone:\n%s", p)
	}
	if len(outcome.VerifyCalls) != 1 || outcome.VerifyCalls[0].Replacements != 7 {
		t.Errorf("verify calls = %+v", outcome.VerifyCalls)
	}
	assertGone(t, client.dir)

	// With verify on as well, the one server serves all three tools.
	client = &mcpClient{}
	d = newVerifyDispatcher(client)
	d.Config.Checks = []config.CheckConfig{{Name: "vet", Command: "go vet ./..."}}
	if _, err := d.Execute(context.Background(), domain.ActionImplement, verifyRun()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(client.lastReq.Extensions.MCPTools, ","); got != "mcp__orch__run_tests,mcp__orch__run_check,mcp__orch__replace" {
		t.Errorf("tools = %s", got)
	}
}

// replace alone is a convenience: a server that cannot be set up is a warning, and the step
// runs without it.
func TestExecuteImplement_ReplaceSetupFailureIsAWarning(t *testing.T) {
	client := &mcpClient{}
	d := newDispatcher(client)
	d.VerifyExecutable = "relative/orchestrator"
	d.Guard = &verifyGuard{}
	var warnings []string
	d.OnWarning = func(_ domain.ActionType, msg string) { warnings = append(warnings, msg) }

	if _, err := d.Execute(context.Background(), domain.ActionImplement, verifyRun()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !client.called || client.lastReq.Extensions.MCPConfig != "" || strings.Contains(client.lastReq.Prompt, "mcp__orch") {
		t.Errorf("the step did not run plainly: %+v", client.lastReq.Extensions)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "replace tool is not available") {
		t.Errorf("warnings = %v", warnings)
	}
}
