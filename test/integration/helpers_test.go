// Package integration exercises the orchestrator end-to-end (MVP.md §29) via
// app.Run, using fake "claude"/"codex"/"gemini" executables on PATH instead of real
// AI provider CLIs, and a real throwaway git fixture repository.
package integration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/app"
	"orchestrator/internal/testutil/fakeexec"
)

// providerSet names which provider each action/classifier is configured to use.
// Every field defaults to "claude" via newProviderSet - tests override only what they
// need to exercise a specific missing-executable scenario.
type providerSet struct {
	Classifier, Analyze, Plan, Implement, Review, Answer string
}

func defaultProviderSet() providerSet {
	return providerSet{
		Classifier: "claude", Analyze: "claude", Plan: "claude",
		Implement: "claude", Review: "claude", Answer: "claude",
	}
}

type configOptions struct {
	providers        providerSet
	maxSteps         int
	maxFixIterations int
	testCommand      string
	// timeout is the rendered execution.timeout value (e.g. "30s"). Defaults to "30s"
	// via defaultConfigOptions - tests only override it to exercise the run-wide
	// timeout (SPEC-diagnostics.md §6.6) with a short deadline.
	timeout string
}

func defaultConfigOptions() configOptions {
	return configOptions{
		providers:        defaultProviderSet(),
		maxSteps:         12,
		maxFixIterations: 2,
		testCommand:      "go test ./...",
		timeout:          "30s",
	}
}

// writeConfig renders a minimal orchestrator.yaml matching internal/config's schema
// and returns its absolute path. It deliberately does not import internal/config, to
// keep these as true black-box tests driven only through app.Run's public surface.
func writeConfig(t *testing.T, dir string, opts configOptions) string {
	t.Helper()
	p := opts.providers
	content := fmt.Sprintf(`version: 1

classifier:
  provider: %s
  model: fake

actions:
  analyze:
    provider: %s
    model: fake
  plan:
    provider: %s
    model: fake
  implement:
    provider: %s
    model: fake
  review:
    provider: %s
    model: fake
  answer:
    provider: %s
    model: fake

test:
  command: %q

limits:
  max_steps: %d
  max_fix_iterations: %d

execution:
  timeout: %s
`, p.Classifier, p.Analyze, p.Plan, p.Implement, p.Review, p.Answer,
		opts.testCommand, opts.maxSteps, opts.maxFixIterations, opts.timeout)

	path := filepath.Join(dir, "orchestrator.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// runOrchestrator chdirs into repoDir (app.Run resolves cwd itself, with no injectable
// override) for the duration of the call, restoring the original cwd afterward, and
// returns whatever app.Run returns plus its stdout/stderr.
func runOrchestrator(t *testing.T, repoDir, configPath string, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	origWD, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("getwd: %v", wdErr)
	}
	if chdirErr := os.Chdir(repoDir); chdirErr != nil {
		t.Fatalf("chdir %s: %v", repoDir, chdirErr)
	}
	defer func() {
		if chdirErr := os.Chdir(origWD); chdirErr != nil {
			t.Fatalf("restore chdir: %v", chdirErr)
		}
	}()

	fullArgs := append([]string{"--config", configPath, "--mode", "auto"}, args...)

	var outBuf, errBuf bytes.Buffer
	err = app.Run(context.Background(), fullArgs, strings.NewReader(""), &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), err
}

// installFakeProviders puts a fake executable on PATH for every name in names (e.g.
// "claude", "codex", "gemini"), all sharing fakeexec.ProviderScript, and returns the
// directory they live in. The rest of PATH (go toolchain, git, sh, ...) is preserved,
// but any existing directory that contains a real claude/codex/gemini executable is
// stripped out - so a provider deliberately left out of names is genuinely
// unresolvable via exec.LookPath, even if a real install happens to be on the host
// running these tests.
func installFakeProviders(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		fakeexec.WriteExecutable(t, dir, name, fakeexec.ProviderScript)
	}
	t.Setenv("PATH", pathWithoutRealProviders(dir))
	return dir
}

var providerExecutableNames = []string{"claude", "codex", "gemini"}

// pathWithoutRealProviders returns fakeDir prepended to the current PATH, with any
// other directory that contains a real claude/codex/gemini executable removed.
func pathWithoutRealProviders(fakeDir string) string {
	entries := filepath.SplitList(os.Getenv("PATH"))
	filtered := []string{fakeDir}
	for _, entry := range entries {
		if entry == fakeDir || dirHasProviderExecutable(entry) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return strings.Join(filtered, string(os.PathListSeparator))
}

func dirHasProviderExecutable(dir string) bool {
	for _, name := range providerExecutableNames {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// gitOutput runs a real git command in dir and returns its trimmed stdout, for
// assertions about the fixture repository's state (never used to mutate it).
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// writeFixedFixtureFile writes fakeexec.FixedFixtureSource to a temp file and returns
// its path, for use as FAKE_FIX_CONTENT_FILE.
func writeFixedFixtureFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixed.go")
	if err := os.WriteFile(path, []byte(fakeexec.FixedFixtureSource), 0o644); err != nil {
		t.Fatalf("write fixed fixture file: %v", err)
	}
	return path
}

// equalStrings reports whether a and b contain the same strings in the same order.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// writeResponse writes a canned JSON response for one action into responsesDir.
func writeResponse(t *testing.T, responsesDir, name, json string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(responsesDir, name+".json"), []byte(json), 0o644); err != nil {
		t.Fatalf("write response %s: %v", name, err)
	}
}

// invocations reads the INVOCATIONS_LOG file, returning nil if it was never created
// (i.e. zero AI calls happened).
func invocations(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read invocations log: %v", err)
	}
	var lines []string
	for _, line := range bytesSplitLines(data) {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func bytesSplitLines(data []byte) []string {
	var lines []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, string(data[start:i]))
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}

const classifyBugfixMedium = `{
  "type": "bugfix",
  "complexity": "medium",
  "requires_code_change": true,
  "requires_tests": true,
  "requires_review": true
}`

const classifyBugfixLow = `{
  "type": "bugfix",
  "complexity": "low",
  "requires_code_change": true,
  "requires_tests": true,
  "requires_review": true
}`

const analyzeResponse = `{
  "summary": "Add() subtracts instead of adding.",
  "root_cause": "wrong operator",
  "affected_files": ["fixture.go"],
  "recommendation": "use + instead of -",
  "risks": []
}`

const implementResponse = `{
  "summary": "fixed Add to use +",
  "changed_files": ["fixture.go"],
  "tests_added": []
}`

const reviewPassResponse = `{
  "status": "pass",
  "summary": "change looks correct",
  "issues": []
}`

const reviewFailResponse = `{
  "status": "fail",
  "summary": "found a problem",
  "issues": [
    {"severity": "high", "file": "fixture.go", "description": "double check the fix"}
  ]
}`
