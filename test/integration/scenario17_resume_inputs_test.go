package integration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 17 (specs/resume/SPEC.md §9): a run records what a later resume is checked
// against, and a step cannot rewrite what a resume reads back.

func TestScenario17_RunRecordsResumeBaseline(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)

	raw, _ := runFile(t, repoDir, "run.json")
	var run struct {
		HeadRef        string `json:"head_ref"`
		ResumeBaseline struct {
			Patterns       []string `json:"patterns"`
			GitFingerprint string   `json:"git_fingerprint"`
		} `json:"resume_baseline"`
	}
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		t.Fatal(err)
	}
	head, _ := gitOutput(repoDir, "rev-parse", "HEAD")
	if run.HeadRef != strings.TrimSpace(head) {
		t.Errorf("head_ref = %q, want HEAD %s", run.HeadRef, head)
	}
	if run.ResumeBaseline.GitFingerprint == "" || !slices.Contains(run.ResumeBaseline.Patterns, ".claude/**") {
		t.Errorf("resume_baseline = %+v", run.ResumeBaseline)
	}
	// The run directory changes with every step, so it is never part of the baseline.
	for _, p := range run.ResumeBaseline.Patterns {
		if strings.Contains(p, ".orchestrator") {
			t.Errorf("the baseline protects the runs directory: %q", p)
		}
	}
}

// oldRun creates an earlier run's directory in a repository that ignores .orchestrator/,
// so the tree stays clean, and returns its path relative to the repository.
func oldRun(t *testing.T, repoDir string) string {
	t.Helper()
	gi := filepath.Join(repoDir, ".gitignore")
	existing, _ := os.ReadFile(gi)
	if err := os.WriteFile(gi, append(existing, []byte("\n.orchestrator/\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, repoDir, "ignore runs")

	rel := ".orchestrator/runs/3f2a9c1e-0000-4000-8000-000000000001"
	for name, content := range map[string]string{
		"run.json":                `{"id":"3f2a9c1e-0000-4000-8000-000000000001"}`,
		"logs/001-implement.log":  "log\n",
		"artifacts/000-plan.json": `{"summary":"plan"}`,
	} {
		p := filepath.Join(repoDir, rel, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return rel
}

func TestScenario17_ImplementCannotRewriteResumeInputs(t *testing.T) {
	for _, name := range []string{"run.json", "artifacts/000-plan.json"} {
		t.Run(name, func(t *testing.T) {
			repoDir := fakeexec.InitGitFixture(t)
			rel := oldRun(t, repoDir)
			invocationsLog, _ := decisionEnv(t, repoDir)
			target := rel + "/" + name
			t.Setenv("FAKE_FIX_TARGET_FILE", filepath.Join(repoDir, target))

			marker := filepath.Join(t.TempDir(), "test-ran")
			j := startJev(t, scriptBugfixMedium())
			opts := defaultConfigOptions()
			opts.jevURL = j.URL()
			opts.testCommand = "touch " + marker + " && go test ./..."
			cfg := writeConfig(t, t.TempDir(), opts)

			_, _, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
			var wfErr *apperr.WorkflowError
			if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
				t.Fatalf("expected a WorkflowError (exit 4), got %T: %v", err, err)
			}
			if !strings.Contains(err.Error(), target) {
				t.Errorf("the message must name %s:\n%v", target, err)
			}
			if _, statErr := os.Stat(marker); statErr == nil {
				t.Error("test.command ran after the run directory was rewritten")
			}
			if got := invocations(t, invocationsLog); !equalStrings(got, []string{"plan", "implement"}) {
				t.Errorf("invocations = %v, want [plan implement]", got)
			}
		})
	}
}

// Logs are output, never read back: writing one does not stop the run. The fake
// implement writes the log instead of fixing fixture.go, so the tests keep failing and
// the run ends at the fix-iteration limit - after the guard let it through to test and
// back to implement every time.
func TestScenario17_ImplementMayTouchLogs(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	rel := oldRun(t, repoDir)
	invocationsLog, _ := decisionEnv(t, repoDir)
	t.Setenv("FAKE_FIX_TARGET_FILE", filepath.Join(repoDir, rel, "logs/001-implement.log"))
	j := startJev(t, scriptBugfixMedium())

	_, _, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "fix Add in fixture.go")
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected the fix-iteration limit, got %T: %v", err, err)
	}
	implements := 0
	for _, a := range invocations(t, invocationsLog) {
		if a == "implement" {
			implements++
		}
	}
	if implements < 2 {
		t.Errorf("implement ran %d times; the guard must not stop a log write", implements)
	}
}
