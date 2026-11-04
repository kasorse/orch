package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/guard"
	"github.com/kasorse/orch/internal/repository"
	"github.com/kasorse/orch/internal/testexec"
	"github.com/kasorse/orch/internal/verify"
)

func TestRun_MCPVerifyUsageErrorsExitTwo(t *testing.T) {
	abs := t.TempDir()
	for _, args := range [][]string{
		{"mcp-verify"},
		{"mcp-verify", "--dir"},
		{"mcp-verify", "--dir="},
		{"mcp-verify", "--dir", "rel/dir"},
		{"mcp-verify", "--dir=rel"},
		{"mcp-verify", "--dir", abs, "extra"},
		{"mcp-verify", "extra", "--dir", abs},
		{"mcp-verify", "--dir", abs, "--dir", abs},
		{"mcp-verify", "--bogus"},
		{"--mode", "plan", "mcp-verify"},
	} {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
		if err == nil || exitcode.FromError(err) != exitcode.ConfigOrDependency {
			t.Errorf("Run(%q) = %v (exit %d), want a usage error with exit 2", args, err, exitcode.FromError(err))
			continue
		}
		if stdout.Len() != 0 {
			t.Errorf("Run(%q) wrote to the MCP channel: %q", args, stdout.String())
		}
	}
}

func TestRun_MCPVerifyUsageNamesNothingHidden(t *testing.T) {
	if strings.Contains(usage, "mcp-verify") {
		t.Error("the usage text mentions the hidden subcommand")
	}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), nil, strings.NewReader(""), &stdout, &stderr)
	if err == nil || strings.Contains(err.Error(), "mcp-verify") {
		t.Errorf("Run() = %v", err)
	}
}

func TestRun_MCPVerifyNeedsAValidSpec(t *testing.T) {
	missing := t.TempDir()
	badVersion := t.TempDir()
	if err := os.WriteFile(filepath.Join(badVersion, verify.SpecFile), []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loose := t.TempDir()
	if err := os.WriteFile(filepath.Join(loose, verify.SpecFile), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"no verify.json": missing, "version 2": badVersion, "readable by others": loose} {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), []string{"mcp-verify", "--dir", dir}, strings.NewReader(""), &stdout, &stderr)
		if err == nil || exitcode.FromError(err) != exitcode.ConfigOrDependency || !strings.Contains(err.Error(), "verify.json") {
			t.Errorf("%s: Run = %v (exit %d)", name, err, exitcode.FromError(err))
		}
	}
}

// mcpRepo makes a repository and the verify directory for it, with the guard's before-state
// taken as the implement step would take it.
func mcpRepo(t *testing.T) (repoDir, verifyDir string) {
	t.Helper()
	repoDir = t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")

	repo := &repository.Repository{Dir: repoDir, Executor: execution.NewOSProcessExecutor()}
	before, err := guard.NewRepoGuard(repo, []string{".github/workflows/**"}).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	verifyDir = t.TempDir()
	spec := &verify.Spec{
		Version:     verify.SpecVersion,
		RepoRoot:    repoDir,
		TestCommand: "true",
		Checks:      []verify.Check{{Name: "vet", Command: "true"}},
		Sandbox:     &testexec.Sandbox{Home: t.TempDir(), RepoRoot: repoDir},
		TestEnv:     []string{"PATH=/usr/bin:/bin"},
		Before:      before,
		MaxCalls:    4,
		CallTimeout: time.Minute,
		OutputBytes: 4096,
	}
	if err := verify.WriteSpec(verifyDir, spec); err != nil {
		t.Fatal(err)
	}
	return repoDir, verifyDir
}

func decodeLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var msgs []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdout carries something that is not a JSON-RPC message: %q", line)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func TestRun_MCPVerifyServesUntilEOF(t *testing.T) {
	_, dir := mcpRepo(t)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n") + "\n"
	for _, flag := range [][]string{{"--dir", dir}, {"--dir=" + dir}} {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), append([]string{"mcp-verify"}, flag...), strings.NewReader(input), &stdout, &stderr)
		if err != nil {
			t.Fatalf("Run(%q) = %v", flag, err)
		}
		msgs := decodeLines(t, stdout.String())
		if len(msgs) != 2 {
			t.Fatalf("stdout = %q, want two replies", stdout.String())
		}
		info, _ := json.Marshal(msgs[0]["result"].(map[string]any)["serverInfo"])
		if string(info) != `{"name":"orch","version":"`+Version+`"}` {
			t.Errorf("serverInfo = %s", info)
		}
		if tools := msgs[1]["result"].(map[string]any)["tools"].([]any); len(tools) != 2 {
			t.Errorf("tools = %v", tools)
		}
	}
}

// TestRun_MCPVerifyRefusesAfterAProtectedChange: the real guard, wired by the subcommand,
// sees a protected file appear and the server runs nothing.
func TestRun_MCPVerifyRefusesAfterAProtectedChange(t *testing.T) {
	repoDir, dir := mcpRepo(t)
	ci := filepath.Join(repoDir, ".github", "workflows", "ci.yml")
	if err := os.MkdirAll(filepath.Dir(ci), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ci, []byte("on: push\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// If the command ran, it would leave this behind.
	marker := filepath.Join(t.TempDir(), "ran")
	spec, err := verify.ReadSpec(dir)
	if err != nil {
		t.Fatal(err)
	}
	spec.TestCommand = "touch " + marker
	if err := verify.WriteSpec(dir, spec); err != nil {
		t.Fatal(err)
	}

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_tests"}}` + "\n"
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"mcp-verify", "--dir", dir}, strings.NewReader(input), &stdout, &stderr); err != nil {
		t.Fatalf("Run = %v", err)
	}
	msgs := decodeLines(t, stdout.String())
	result, _ := msgs[0]["result"].(map[string]any)
	text, _ := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if len(msgs) != 1 || result["isError"] != true || !strings.Contains(text, "protected files changed: .github/workflows/ci.yml") {
		t.Errorf("reply = %v", msgs)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the test command ran after a protected change")
	}
	data, err := os.ReadFile(filepath.Join(dir, "violation"))
	if err != nil || string(data) != ".github/workflows/ci.yml\n" {
		t.Errorf("violation = %q, %v", data, err)
	}
}

// TestRun_MCPVerifyReplaceOverARealRepository: replace alone, with the real guard and git's
// file list: tracked and untracked files are rewritten, an ignored one is not.
func TestRun_MCPVerifyReplaceOverARealRepository(t *testing.T) {
	repoDir, dir := mcpRepo(t)
	for name, content := range map[string]string{
		"main.go":    "package main\n\nvar OldName = 1\nvar OldNameX = 2\n",
		"new.go":     "package main\n\nvar _ = OldName\n",
		".gitignore": "ignored.go\n",
		"ignored.go": "OldName\n",
	} {
		if err := os.WriteFile(filepath.Join(repoDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spec, err := verify.ReadSpec(dir)
	if err != nil {
		t.Fatal(err)
	}
	spec.TestCommand, spec.Checks, spec.Sandbox, spec.TestEnv = "", nil, nil, nil
	spec.Replace = &verify.ReplaceSpec{}
	if err := verify.WriteSpec(dir, spec); err != nil {
		t.Fatal(err)
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"replace","arguments":{"old":"OldName","new":"NewName","word":true}}}`,
	}, "\n") + "\n"
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"mcp-verify", "--dir", dir}, strings.NewReader(input), &stdout, &stderr); err != nil {
		t.Fatalf("Run = %v (%s)", err, stderr.String())
	}
	msgs := decodeLines(t, stdout.String())
	if tools := msgs[0]["result"].(map[string]any)["tools"].([]any); len(tools) != 1 {
		t.Errorf("tools = %v, want replace alone", tools)
	}
	result, _ := msgs[1]["result"].(map[string]any)
	text, _ := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if result["isError"] != false || !strings.Contains(text, "replaced 2 occurrences in 2 files") {
		t.Errorf("reply = %v", msgs[1])
	}
	for name, want := range map[string]string{
		"main.go":    "package main\n\nvar NewName = 1\nvar OldNameX = 2\n",
		"new.go":     "package main\n\nvar _ = NewName\n",
		"ignored.go": "OldName\n",
	} {
		if got, _ := os.ReadFile(filepath.Join(repoDir, name)); string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
