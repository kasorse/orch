package integration

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 27 (specs/hardening/SPEC.md §9): what implement or the test command adds is
// scanned for secrets. The key is assembled at run time: no token-shaped literal in the
// repository.
var scenarioKey = "AK" + "IA" + "Z7Q2R5T8W1Y4B6C9"

var implementTestReview = []string{"run", "--stage", "development", "--stage", "testing", "--stage", "review"}

// leakyFix makes the fake implement write the fixed fixture plus a hard-coded key.
func leakyFix(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixed.go")
	src := fakeexec.FixedFixtureSource + "\nconst deployKeyID = \"" + scenarioKey + "\"\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_FIX_CONTENT_FILE", path)
}

// runDirContains reports whether any file under the run directory holds needle.
func runDirContains(t *testing.T, repoDir, needle string) bool {
	t.Helper()
	found := false
	root := filepath.Dir(runJSONPath(t, repoDir))
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if data, _ := os.ReadFile(p); strings.Contains(string(data), needle) {
				found = true
			}
		}
		return nil
	})
	return found
}

func secretScanYAML(mode string, extra string) func(*configOptions) {
	return func(o *configOptions) {
		o.extraYAML = "\nsecurity:\n  secret_scan:\n    mode: " + mode + "\n" + extra
	}
}

// 27a: mode stop (the default) - the run stops after implement, before review sees it.
func TestScenario27_StopAfterImplement(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newStageRun(t, repoDir)
	leakyFix(t)

	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("expected a WorkflowError (exit 4), got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "fixture.go:") || !strings.Contains(err.Error(), "(aws-access-key-id)") {
		t.Errorf("the message does not say where:\n%v", err)
	}
	if strings.Contains(err.Error(), scenarioKey) {
		t.Error("the message contains the value")
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations = %v, want implement only", got)
	}
	if runDirContains(t, repoDir, scenarioKey) {
		t.Error("the value is in the run directory")
	}
}

// 27b: mode warn - the run goes on, the finding is recorded, review never sees the value.
func TestScenario27_WarnRecordsAndRedacts(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newStageRun(t, repoDir, secretScanYAML("warn", ""))
	leakyFix(t)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	raw, _ := runFile(t, repoDir, "run.json")
	var rec struct {
		SecretFindings []struct {
			StepIndex int    `json:"step_index"`
			File      string `json:"file"`
			Rule      string `json:"rule"`
		} `json:"secret_findings"`
	}
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.SecretFindings) != 1 || rec.SecretFindings[0].File != "fixture.go" || rec.SecretFindings[0].Rule != "aws-access-key-id" || rec.SecretFindings[0].StepIndex != 0 {
		t.Errorf("secret_findings = %+v", rec.SecretFindings)
	}
	prompt, err := os.ReadFile(filepath.Join(r.promptsDir, "review.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(prompt), scenarioKey) || !strings.Contains(string(prompt), "[REDACTED:aws-access-key-id]") {
		t.Error("the review prompt was not redacted")
	}
	if !strings.Contains(stdout, "implement added what looks like a secret: fixture.go:") {
		t.Errorf("the console does not warn:\n%s", stdout)
	}
	if runDirContains(t, repoDir, scenarioKey) {
		t.Error("the value is in the run directory")
	}
}

// 27c: a test command that writes a key is caught after the test step.
func TestScenario27_TestStepWritesAKey(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newStageRun(t, repoDir, func(o *configOptions) {
		// The key is split in the command, so the config file itself holds none.
		o.testCommand = "printf '%s%s\\n' 'AK' 'IAZ7Q2R5T8W1Y4B6C9' > leaked.txt && go test ./..."
	})
	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	if err == nil || !strings.Contains(err.Error(), "test added what looks like a secret") || !strings.Contains(err.Error(), "leaked.txt:1 (aws-access-key-id)") {
		t.Fatalf("expected the test step to be stopped, got %v", err)
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement"}) {
		t.Errorf("invocations = %v, review must not run", got)
	}
}

// 27d: a secret already in the tree when the run starts is not the run's doing.
func TestScenario27_DirtyStartBaseline(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	if err := os.WriteFile(filepath.Join(repoDir, "notes.txt"), []byte("old key "+scenarioKey+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newStageRun(t, repoDir, secretScanYAML("stop", "  require_clean_worktree: false\n"))
	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)
	raw, _ := runFile(t, repoDir, "run.json")
	if !strings.Contains(raw, `"secret_scan_baseline"`) {
		t.Error("run.json has no secret_scan_baseline")
	}
	prompt, _ := os.ReadFile(filepath.Join(r.promptsDir, "review.txt"))
	if strings.Contains(string(prompt), scenarioKey) {
		t.Error("a baseline secret reached the review prompt")
	}
}

// 27e: mode off is announced and nothing is scanned.
func TestScenario27_Off(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newStageRun(t, repoDir, secretScanYAML("off", ""))
	leakyFix(t)
	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)
	if !strings.Contains(stdout, "Secret scan: off") {
		t.Errorf("the run does not announce mode off:\n%s", stdout)
	}
}
