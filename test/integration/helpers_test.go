// Package integration exercises the orchestrator end-to-end (MVP.md §29) via
// app.Run, using fake "claude"/"codex"/"gemini" executables on PATH instead of real
// AI provider CLIs, and a real throwaway git fixture repository.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"orchestrator/internal/app"
	"orchestrator/internal/testutil/fakeexec"
)

// providerSet names which provider each action, the translator and the decision
// provider are configured to use. Every field defaults to "claude" (Decision to "jev")
// via defaultProviderSet - tests override only what they need.
type providerSet struct {
	Research, Analyze, Plan, Implement, Review, Translation string
	// Decision is "jev" or a CLI provider (TASKS.md T22).
	Decision string
}

func defaultProviderSet() providerSet {
	return providerSet{
		Research: "claude", Analyze: "claude", Plan: "claude", Implement: "claude", Review: "claude",
		Translation: "claude", Decision: "jev",
	}
}

type configOptions struct {
	providers        providerSet
	maxSteps         int
	maxFixIterations int
	// stopWithoutProgress is limits.stop_without_progress. defaultConfigOptions leaves it
	// off: the fake implement either fixes the fixture or never changes it, so scenarios that
	// loop to max_fix_iterations would otherwise stop earlier for making no progress. The
	// no-progress scenarios turn it on.
	stopWithoutProgress bool
	testCommand         string
	// testEnvPass renders test.env_pass when set.
	testEnvPass []string
	// testBaseline is test.baseline. defaultConfigOptions sets "off": most scenarios fix the
	// fixture's failing test, which a pre-run test would refuse (specs/hardening/SPEC.md §8);
	// the pre-run test scenarios set it back.
	testBaseline string
	// promptLimits is rendered verbatim under limits:, e.g. "  prompt:\n    max_diff_bytes: 65536\n".
	promptLimits string
	// timeout is the rendered execution.timeout value (e.g. "30s"). Defaults to "30s"
	// via defaultConfigOptions - tests only override it to exercise the run-wide
	// timeout (MVP.md §25) with a short deadline.
	timeout string
	// jevURL is decision.jev.base_url - the fake Jev server's URL.
	jevURL string
	// onFailure is decision.on_failure: "ask" or "fail".
	onFailure string
	// decisionModel overrides decision.model; empty keeps jev-1.13.0 (or "fake" for a CLI
	// decision provider).
	decisionModel string
	// extraYAML is appended to the rendered config verbatim (e.g. a security: block).
	extraYAML string
	// jira renders a jira: section when set (specs/jira/SPEC.md §7); nil leaves it out,
	// so every other scenario's config is unchanged.
	jira *jiraOptions

	// The quality phase's keys (specs/quality/SPEC.md §11). Each is rendered only when set, so
	// a config that does not use one is the config it was before.
	//
	// testSandbox is test.sandbox; "" renders none. verify needs a sandbox, so its scenarios put
	// a fake bwrap on PATH (installFakeBwrap) and set "bwrap".
	testSandbox string
	// checks is the top-level checks: list, run in order before test.command.
	checks []checkOption
	// verify is actions.implement.verify, and verifyMaxCalls (when above 0)
	// limits.verify.max_calls_per_step.
	verify         bool
	verifyMaxCalls int
	// byComplexity is actions.<action>.by_complexity as action -> complexity -> model.
	byComplexity map[string]map[string]string
	// fixMedium is review.fix_medium.
	fixMedium bool
	// reviewModels is actions.review.models: the extra reviewers.
	reviewModels []string
	// pathRules is policy.paths, the list items verbatim (four spaces of indent), e.g.
	// "    - match: fixture.go\n      require_stages: [testing]\n". Empty renders none.
	pathRules string
	// explore is policy.explore, the lines verbatim (two spaces of indent), e.g.
	// "  explore:\n    band: 0.05\n    rate: 0.5\n". Empty renders none.
	explore string
	// maxCostUSD is limits.max_cost_usd; 0 renders none.
	maxCostUSD float64
}

// pathRulesYAML is the policy.paths and policy.explore keys that follow policy.stages, or
// nothing.
func (o configOptions) pathRulesYAML() string {
	s := o.explore
	if o.pathRules != "" {
		s = "  paths:\n" + o.pathRules + s
	}
	return s
}

// checkOption is one entry of the checks: list.
type checkOption struct{ name, command string }

// yamlQuote renders s as a single-quoted YAML scalar, which needs no other escaping.
func yamlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// actionsYAML renders the actions: entries, each with the keys the options add to it.
func (o configOptions) actionsYAML() string {
	p := o.providers
	var b strings.Builder
	for _, a := range []struct{ name, provider string }{
		{"research", p.Research}, {"analyze", p.Analyze}, {"plan", p.Plan}, {"implement", p.Implement}, {"review", p.Review},
	} {
		fmt.Fprintf(&b, "  %s:\n    provider: %s\n    model: fake\n", a.name, a.provider)
		if levels := o.byComplexity[a.name]; len(levels) > 0 {
			b.WriteString("    by_complexity:\n")
			names := make([]string, 0, len(levels))
			for level := range levels {
				names = append(names, level)
			}
			sort.Strings(names)
			for _, level := range names {
				fmt.Fprintf(&b, "      %s: {model: %s}\n", level, levels[level])
			}
		}
		if a.name == "implement" && o.verify {
			b.WriteString("    verify: true\n")
		}
		if a.name == "review" && len(o.reviewModels) > 0 {
			fmt.Fprintf(&b, "    models: [%s]\n", strings.Join(o.reviewModels, ", "))
		}
	}
	return b.String()
}

// limitsYAML is what goes under limits: after stop_without_progress.
func (o configOptions) limitsYAML() string {
	s := o.promptLimits
	if o.maxCostUSD > 0 {
		s += fmt.Sprintf("  max_cost_usd: %v\n", o.maxCostUSD)
	}
	if o.verifyMaxCalls > 0 {
		s += fmt.Sprintf("  verify:\n    max_calls_per_step: %d\n", o.verifyMaxCalls)
	}
	return s
}

// tailYAML is the top-level checks: and review: sections, after the rest of the config.
func (o configOptions) tailYAML() string {
	var b strings.Builder
	if len(o.checks) > 0 {
		b.WriteString("\nchecks:\n")
		for _, c := range o.checks {
			fmt.Fprintf(&b, "  - name: %s\n    command: %s\n", c.name, yamlQuote(c.command))
		}
	}
	if o.fixMedium {
		b.WriteString("\nreview:\n  fix_medium: true\n")
	}
	return b.String()
}

// jiraOptions is the jira: section: a basic-auth instance at baseURL reading
// JIRA_EMAIL/JIRA_API_TOKEN, with fields as extra custom fields (id, label).
type jiraOptions struct {
	baseURL string
	fields  [][2]string
}

func (j *jiraOptions) yaml() string {
	if j == nil {
		return ""
	}
	s := fmt.Sprintf("\njira:\n  base_url: %s\n  auth: basic\n  email_env: JIRA_EMAIL\n  token_env: JIRA_API_TOKEN\n", j.baseURL)
	if len(j.fields) > 0 {
		s += "  fields:\n"
		for _, f := range j.fields {
			s += fmt.Sprintf("    - id: %s\n      label: %s\n", f[0], f[1])
		}
	}
	return s
}

func defaultConfigOptions() configOptions {
	return configOptions{
		providers:        defaultProviderSet(),
		maxSteps:         12,
		maxFixIterations: 2,
		testCommand:      "go test ./...",
		testBaseline:     "off",
		timeout:          "30s",
		jevURL:           "http://127.0.0.1:1", // tests using Jev replace it with their server's
		onFailure:        "fail",
	}
}

// writeConfig renders a version 2 orchestrator.yaml matching internal/config's schema
// and returns its absolute path. It deliberately does not import internal/config, to
// keep these as true black-box tests driven only through app.Run's public surface.
// envPassYAML renders test.env_pass, or nothing.
func envPassYAML(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return "  env_pass: [" + strings.Join(names, ", ") + "]\n"
}

func writeConfig(t *testing.T, dir string, opts configOptions) string {
	t.Helper()
	p := opts.providers
	decisionModel := "jev-1.13.0"
	if p.Decision != "jev" {
		decisionModel = "fake"
	}
	if opts.decisionModel != "" {
		decisionModel = opts.decisionModel
	}
	sandbox := opts.testSandbox
	if sandbox == "" {
		sandbox = "none"
	}
	content := fmt.Sprintf(`version: 2

decision:
  provider: %s
  model: %s
  timeout: 10s
  complexity:
    primary: choice
    shadow: true
  on_failure: %s
  jev:
    base_url: %s
    api_key_env: JEV_API_KEY
    max_retries: 1

translation:
  provider: %s
  model: fake
  timeout: 10s

policy:
  context_depth:
    project_threshold: 0.5
  complexity:
    cuts: {medium: 0.50, complex: 1.40}
  stages:
    thresholds: {research: 0.60, analysis: 0.60, development: 0.60}
    force:
      medium: [analysis, review]
      complex: [analysis, planning, review]
%s
context:
  max_files: 20
  max_bytes: 24576

actions:
%s
test:
  sandbox: %s
  baseline: %s
  command: %q
%s
limits:
  max_steps: %d
  max_fix_iterations: %d
  stop_without_progress: %t
%s
execution:
  timeout: %s
%s`, p.Decision, decisionModel, opts.onFailure, opts.jevURL, p.Translation,
		opts.pathRulesYAML(), opts.actionsYAML(),
		sandbox, opts.testBaseline, opts.testCommand, envPassYAML(opts.testEnvPass), opts.maxSteps, opts.maxFixIterations, opts.stopWithoutProgress, opts.limitsYAML(), opts.timeout, opts.extraYAML)
	content += opts.tailYAML()
	content += opts.jira.yaml()

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

const analyzeResponse = `{
  "summary": "Add() subtracts instead of adding.",
  "root_cause": "wrong operator",
  "affected_files": ["fixture.go"],
  "recommendation": "use + instead of -",
  "risks": []
}`

const implementResponse = `{
  "summary": "fixed Add to use +",
  "summary_short": "Fix Add to use +",
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

// resumedRun is the part of run.json the resume scenarios assert on.
type resumedRun struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	FailureReason string `json:"failure_reason"`
	Mode          string `json:"mode"`
	PreRunRef     string `json:"pre_run_ref"`
	HeadRef       string `json:"head_ref"`
	Workflow      *struct {
		Steps []struct {
			Action string `json:"action"`
		} `json:"steps"`
	} `json:"workflow"`
	ResumeBaseline json.RawMessage `json:"resume_baseline"`
	Resumes        []struct {
		FromStatus string `json:"from_status"`
		FromReason string `json:"from_reason"`
		Entry      string `json:"entry"`
		Why        string `json:"why"`
		Mode       string `json:"mode"`
	} `json:"resumes"`
	Executions []struct {
		Action  string `json:"action"`
		Attempt int    `json:"attempt"`
	} `json:"executions"`
}

func loadResumedRun(t *testing.T, repoDir string) resumedRun {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var run resumedRun
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

// resume runs `orchestrator --mode auto [extra...] resume <id>` in repoDir.
func resume(t *testing.T, repoDir, configPath, id string, extra ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runOrchestrator(t, repoDir, configPath, append(extra, "resume", id)...)
}

// runJSONPath is the path of the single run's run.json under repoDir.
func runJSONPath(t *testing.T, repoDir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(repoDir, ".orchestrator", "runs", "*", "run.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one run.json, got %v (err=%v)", matches, err)
	}
	return matches[0]
}

// editRunJSON rewrites the single run's run.json through edit, as a model's step or the
// operator could, and returns the original bytes so a test can put them back.
func editRunJSON(t *testing.T, repoDir string, edit func(run map[string]any)) (original []byte) {
	t.Helper()
	path := runJSONPath(t, repoDir)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var run map[string]any
	if err := json.Unmarshal(original, &run); err != nil {
		t.Fatal(err)
	}
	edit(run)
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return original
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
