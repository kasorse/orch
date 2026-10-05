package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 28 (specs/hardening/SPEC.md §5): what review and a retried implement are shown
// of the repository - new files with their content, lock files as a stat line, the diff
// within its budget, a failing test's output within its budget.

func readPrompt(t *testing.T, promptsDir, action string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(promptsDir, action+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type reviewDiffRecord struct {
	Diff *struct {
		Bytes      int  `json:"bytes"`
		Files      int  `json:"files"`
		Shown      int  `json:"shown"`
		Summarized int  `json:"summarized"`
		Withheld   int  `json:"withheld"`
		Truncated  bool `json:"truncated"`
	} `json:"diff"`
}

func readReviewArtifact(t *testing.T, repoDir string) reviewDiffRecord {
	t.Helper()
	raw, ok := runFile(t, repoDir, filepath.Join("artifacts", "000-review.json"))
	if !ok {
		t.Fatal("no review artifact")
	}
	var rec reviewDiffRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Diff == nil {
		t.Fatal("the review artifact records no diff summary")
	}
	return rec
}

// 28a-c: a review of hand-made changes sees a new file's content, a lock file as a stat
// line next to its full manifest, and nothing of a deny-listed file.
func TestScenario28_ReviewSeesTheRealChangeSet(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	gitRun(t, repoDir, "config", "user.email", "t@example.com")
	gitRun(t, repoDir, "config", "user.name", "T")
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(repoDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.sum", "example.com/a v1.0.0 h1:AAAA\n")
	gitRun(t, repoDir, "add", "go.sum")
	gitRun(t, repoDir, "commit", "-qm", "sum")

	write("internal/newpkg/new.go", "package newpkg\n\n// Needle is new.\nfunc Needle() int { return 42 }\n")
	write("go.sum", "example.com/a v1.0.0 h1:AAAA\n"+strings.Repeat("example.com/b v1.0.0 h1:LOCKLINE\n", 2000))
	write("go.mod", "module fixture\n\nrequire example.com/b v1.0.0\n")
	write("config/secrets.yaml", "db_password: hunter2-never-in-a-prompt\n")

	r := newStageRun(t, repoDir)
	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "review", "review my change")
	mustSucceed(t, stdout, stderr, err)

	prompt := readPrompt(t, r.promptsDir, "review")
	for _, want := range []string{"+func Needle() int { return 42 }", "go.sum", "summarized", "+require example.com/b v1.0.0", "config/secrets.yaml", "withheld"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the review prompt lacks %q", want)
		}
	}
	for _, never := range []string{"LOCKLINE", "hunter2-never-in-a-prompt"} {
		if strings.Contains(prompt, never) {
			t.Errorf("the review prompt contains %q", never)
		}
	}
	d := readReviewArtifact(t, repoDir).Diff
	if d.Summarized != 1 || d.Withheld != 1 || d.Shown < 2 || d.Truncated {
		t.Errorf("diff summary = %+v", *d)
	}
}

// 28d: a change larger than limits.prompt.max_diff_bytes is cut to the budget and says so.
func TestScenario28_ReviewDiffBudget(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	big := strings.Repeat("a long line that the run added to the data file\n", 9000) // ~430 KiB
	if err := os.WriteFile(filepath.Join(repoDir, "data.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newStageRun(t, repoDir, func(o *configOptions) {
		o.promptLimits = "  prompt:\n    max_diff_bytes: 65536\n"
	})
	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "review", "review the data")
	mustSucceed(t, stdout, stderr, err)

	prompt := readPrompt(t, r.promptsDir, "review")
	if len(prompt) > 80<<10 {
		t.Errorf("review prompt is %d bytes for a 64 KiB diff budget", len(prompt))
	}
	if !strings.Contains(prompt, "data.txt") || !strings.Contains(prompt, "not shown: diff budget") {
		t.Error("the prompt does not list the file it left out")
	}
	if d := readReviewArtifact(t, repoDir).Diff; !d.Truncated {
		t.Errorf("diff summary = %+v, want truncated", *d)
	}
}

// 28e: a failing test's large output reaches the retried implement within its budget, with
// the failure line kept and the cut marked.
func TestScenario28_BoundedTestOutputInRetry(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newStageRun(t, repoDir, func(o *configOptions) {
		o.testCommand = `awk 'BEGIN { for (i = 0; i < 30000; i++) print "noise line " i; ` +
			`print "--- FAIL: TestNeedle (0.00s)"; for (i = 0; i < 30000; i++) print "noise line " i; exit 1 }'`
		o.maxFixIterations = 1
	})
	_, _, err := runOrchestrator(t, repoDir, r.cfg, "run", "--stage", "development", "--stage", "testing", "make TestNeedle pass")
	if err == nil {
		t.Fatal("the test command always fails; the run must end in failure")
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "implement"}) {
		t.Fatalf("invocations = %v, want an implement and its retry", got)
	}
	prompt := readPrompt(t, r.promptsDir, "implement") // the retry's prompt
	if !strings.Contains(prompt, "--- FAIL: TestNeedle (0.00s)") {
		t.Error("the failure line was cut from the retry prompt")
	}
	if !strings.Contains(prompt, "omitted ...]") || !strings.Contains(prompt, "The test output above was shortened") {
		t.Error("the retry prompt does not mark the cut")
	}
	if len(prompt) > 48<<10 {
		t.Errorf("retry prompt is %d bytes for a 32 KiB test output budget", len(prompt))
	}
	// The log keeps the whole output.
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(runJSONPath(t, repoDir)), "logs", "*-test.stdout.log"))
	if len(matches) == 0 {
		t.Fatal("no test log")
	}
	data, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(data), "--- FAIL: TestNeedle") || !strings.Contains(string(data), "noise line 29999\n") ||
		strings.Count(string(data), "\n") < 60001 {
		t.Errorf("the test log (%d bytes) does not hold the whole output", len(data))
	}
}
