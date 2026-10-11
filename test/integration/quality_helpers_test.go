package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Helpers shared by scenarios 33 and up (specs/quality/SPEC.md §17): the fake providers of
// the stage-select scenarios plus the per-call prompts and argv, and a view of run.json that
// reads the quality phase's fields.

// qualityRun is a stageRun whose providers also record each call's argv.
type qualityRun struct {
	stageRun
	// responses is FAKE_RESPONSES_DIR: write implement_<N>.json / review_<N>.json here.
	responses string
	// argsDir is FAKE_ARGS_DIR: <action>.args holds the last call's flags.
	argsDir string
}

// newQualityRun is a manual run's setup (newStageRun: the decision layer stays unused).
func newQualityRun(t *testing.T, repoDir string, mutate ...func(*configOptions)) qualityRun {
	t.Helper()
	return withQuality(t, newStageRun(t, repoDir, mutate...))
}

// newQualityPreRunRun is newQualityRun with the pre-run test on (test.baseline: require_green).
func newQualityPreRunRun(t *testing.T, repoDir string, mutate ...func(*configOptions)) qualityRun {
	t.Helper()
	return withQuality(t, newPreRunTestRun(t, repoDir, mutate...))
}

func withQuality(t *testing.T, r stageRun) qualityRun {
	t.Helper()
	argsDir := t.TempDir()
	t.Setenv("FAKE_ARGS_DIR", argsDir)
	return qualityRun{stageRun: r, responses: os.Getenv("FAKE_RESPONSES_DIR"), argsDir: argsDir}
}

// prompt is the prompt of the n-th (1-based) call of action; the fake saves one per call.
func (r qualityRun) prompt(t *testing.T, action string, n int) string {
	t.Helper()
	return readFile(t, filepath.Join(r.promptsDir, action+"_"+strconv.Itoa(n)+".txt"))
}

// args is the argv of the last call of action, one entry per line of <action>.args.
func (r qualityRun) args(t *testing.T, action string) []string {
	t.Helper()
	return strings.Split(strings.TrimSuffix(readFile(t, filepath.Join(r.argsDir, action+".args")), "\n"), "\n")
}

// argAfter is the argument after flag in args, or "" when flag is absent.
func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// runDirFile reads a file of the single run directory (a log, for instance).
func runDirFile(t *testing.T, repoDir, rel string) string {
	t.Helper()
	return readFile(t, filepath.Join(filepath.Dir(runJSONPath(t, repoDir)), rel))
}

// assertContains reports each of wants missing from s, as a test error.
func assertContains(t *testing.T, what, s string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(s, want) {
			t.Errorf("%s lacks %q:\n%s", what, want, s)
		}
	}
}

// assertNotContains reports each of unwanted found in s, as a test error.
func assertNotContains(t *testing.T, what, s string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(s, u) {
			t.Errorf("%s has %q:\n%s", what, u, s)
		}
	}
}

// fixOnCall makes the fake implement fix the fixture from its n-th call on (1 is the default).
func fixOnCall(t *testing.T, n int) {
	t.Helper()
	t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", strconv.Itoa(n))
}

// neverFix makes the fake implement leave the fixture as it was.
func neverFix(t *testing.T) { fixOnCall(t, 999) }

// appendOnEveryImplement makes each fake implement call add a line to the fixture's source,
// so the working tree differs between attempts while the code stays as buggy or as fixed as it
// was.
func appendOnEveryImplement(t *testing.T, repoDir string) {
	t.Helper()
	t.Setenv("FAKE_IMPLEMENT_APPEND_FILE", filepath.Join(repoDir, "fixture.go"))
}

// qlRun is the part of run.json the quality scenarios assert on.
type qlRun struct {
	ID            string        `json:"id"`
	Status        string        `json:"status"`
	FailureReason string        `json:"failure_reason"`
	FixIterations int           `json:"fix_iterations"`
	Options       qlOptions     `json:"options"`
	PreRunTest    *qlTestResult `json:"pre_run_test"`
	Executions    []qlExecution `json:"executions"`
	Resumes       []qlResume    `json:"resumes"`
	Outcome       *qlOutcome    `json:"outcome"`
}

type qlOptions struct {
	ReviewFixMedium bool `json:"review_fix_medium"`
}

type qlResume struct {
	FromStatus string `json:"from_status"`
	FromReason string `json:"from_reason"`
	Entry      string `json:"entry"`
}

type qlOutcome struct {
	FixIterations int `json:"fix_iterations"`
	VerifyCalls   int `json:"verify_calls"`
}

type qlExecution struct {
	Index        int             `json:"index"`
	Action       string          `json:"action"`
	Status       string          `json:"status"`
	Error        string          `json:"error"`
	Provider     *qlProvider     `json:"provider"`
	TestResult   *qlTestResult   `json:"test_result"`
	ReviewResult *qlReviewResult `json:"review_result"`
	TreeAfter    string          `json:"tree_after"`
	VerifyCalls  []qlVerifyCall  `json:"verify_calls"`
	Call         *qlCall         `json:"call"`
}

type qlProvider struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	ModelSource string `json:"model_source"`
}

type qlCall struct {
	ExtraCalls []qlExtraCall `json:"extra_calls"`
}

type qlExtraCall struct {
	Model string `json:"model"`
}

type qlTestResult struct {
	Command     string    `json:"command"`
	ExitCode    int       `json:"exit_code"`
	Success     bool      `json:"success"`
	Checks      []qlCheck `json:"checks"`
	FailedCheck string    `json:"failed_check"`
	FailedTests []string  `json:"failed_tests"`
	Format      string    `json:"report_format"`
}

type qlCheck struct {
	Name     string `json:"name"`
	ExitCode int    `json:"exit_code"`
	Success  bool   `json:"success"`
}

type qlVerifyCall struct {
	Tool     string `json:"tool"`
	Check    string `json:"check"`
	Filter   string `json:"filter"`
	ExitCode int    `json:"exit_code"`
	Success  bool   `json:"success"`
	Refused  string `json:"refused"`
}

type qlReviewResult struct {
	Status    string        `json:"status"`
	Issues    []qlIssue     `json:"issues"`
	Criteria  []qlCriterion `json:"criteria"`
	Reviewers []qlReviewer  `json:"reviewers"`
}

type qlIssue struct {
	Severity    string `json:"severity"`
	File        string `json:"file"`
	Description string `json:"description"`
}

type qlCriterion struct {
	Index     int    `json:"index"`
	Criterion string `json:"criterion"`
	Met       bool   `json:"met"`
	Evidence  string `json:"evidence"`
}

type qlReviewer struct {
	Model  string         `json:"model"`
	Result qlReviewResult `json:"result"`
}

func loadQL(t *testing.T, repoDir string) qlRun {
	t.Helper()
	var run qlRun
	if err := json.Unmarshal([]byte(readFile(t, runJSONPath(t, repoDir))), &run); err != nil {
		t.Fatal(err)
	}
	return run
}

// actions are the actions of the executions, in order.
func (r qlRun) actions() []string {
	var got []string
	for _, e := range r.Executions {
		got = append(got, e.Action)
	}
	return got
}

// ofAction are the executions of one action, in order.
func (r qlRun) ofAction(action string) []qlExecution {
	var got []qlExecution
	for _, e := range r.Executions {
		if e.Action == action {
			got = append(got, e)
		}
	}
	return got
}

// reviewJSON is a review answer. criteriaJSON is the "criteria" array of a review whose plan
// had acceptance criteria, or "" for a plan without any.
func reviewJSON(status, issuesJSON, criteriaJSON string) string {
	s := `{"status": "` + status + `", "summary": "reviewed", "issues": ` + issuesJSON
	if criteriaJSON != "" {
		s += `, "criteria": ` + criteriaJSON
	}
	return s + "}"
}

// reviewMediumResponse passes with one medium issue: review.fix_medium sends it back once.
var reviewMediumResponse = reviewJSON("pass",
	`[{"severity": "medium", "file": "fixture.go", "description": "no test covers the negative case"}]`, "")
