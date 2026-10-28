package integration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 15 (specs/permissions/SPEC.md §7.2-7.3): a workflow that writes starts from a
// clean restore point, and an implement that changes a protected path stops the run
// before test executes anything.

// scriptReadOnly decides a read-only workflow: no asked stage clears its threshold and the
// simple level has no template, so investigation alone runs (the fallback of specs/clarify CL01).
func scriptReadOnly() jevScript {
	return jevScript{PProject: 0.9, Choice: [3]float64{0.8, 0.15, 0.05}}
}

func dirtyFixture(t *testing.T) string {
	t.Helper()
	repoDir := fakeexec.InitGitFixture(t)
	if err := os.WriteFile(filepath.Join(repoDir, "notes.txt"), []byte("work in progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoDir
}

func TestScenario15_DirtyTreeRefusesImplement(t *testing.T) {
	repoDir := dirtyFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())

	stdout, _, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "fix Add in fixture.go")
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
		t.Fatalf("expected a ConfigError (exit 2), got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "security.require_clean_worktree: false") {
		t.Errorf("the message must name both remedies:\n%v", err)
	}
	if got := invocations(t, invocationsLog); len(got) != 0 {
		t.Errorf("no step may run, got %v", got)
	}
	if strings.Contains(stdout, "Restore point") {
		t.Errorf("no restore point for a run that never starts:\n%s", stdout)
	}
}

func TestScenario15_DirtyTreeAllowsReadOnlyWorkflow(t *testing.T) {
	repoDir := dirtyFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, scriptReadOnly())

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "explain Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)
	if !strings.Contains(stderr, "uncommitted changes") {
		t.Errorf("expected the dirty-tree warning, stderr:\n%s", stderr)
	}
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"investigate"}) {
		t.Errorf("invocations = %v, want [investigate]", got)
	}
}

func TestScenario15_DirtyTreeAllowedByConfig(t *testing.T) {
	repoDir := dirtyFixture(t)
	invocationsLog, _ := decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j, func(o *configOptions) { o.security = "  require_clean_worktree: false\n" })

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)
	if got := invocations(t, invocationsLog); !equalStrings(got, []string{"investigate", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
	// On a dirty tree the restore point is a stash commit holding notes.txt, not HEAD.
	head, _ := gitOutput(repoDir, "rev-parse", "HEAD")
	ref := preRunRef(t, repoDir)
	if ref == "" || ref == head {
		t.Errorf("pre_run_ref = %q, want a stash commit (HEAD is %s)", ref, head)
	}
	if files, _ := gitOutput(repoDir, "show", "--name-only", "--format=", ref); !strings.Contains(files, "notes.txt") {
		t.Errorf("the restore point must hold the uncommitted notes.txt, has:\n%s", files)
	}
	assertRestorePointPinned(t, repoDir)
}

func TestScenario15_CleanRunRecordsRestorePoint(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "fix Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)
	head, _ := gitOutput(repoDir, "rev-parse", "HEAD")
	if ref := preRunRef(t, repoDir); ref != head {
		t.Errorf("pre_run_ref = %q, want HEAD %s", ref, head)
	}
	if !strings.Contains(stdout, "Restore point: "+head) {
		t.Errorf("expected the restore point in the output:\n%s", stdout)
	}
	assertRestorePointPinned(t, repoDir)
}

func TestScenario15_ProtectedPathStopsBeforeTest(t *testing.T) {
	cases := map[string]string{
		"agent settings": ".claude/settings.json",
		"git hook":       ".git/hooks/pre-commit",
		"config file":    "orchestrator.yaml",
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			repoDir := fakeexec.InitGitFixture(t)
			// The config lives in the worktree (committed), so it is protected too.
			configDir := repoDir
			if target != "orchestrator.yaml" {
				configDir = t.TempDir()
			}
			// Empty directories are invisible to git, so the tree stays clean.
			if err := os.MkdirAll(filepath.Join(repoDir, filepath.Dir(target)), 0o755); err != nil {
				t.Fatal(err)
			}
			invocationsLog, _ := decisionEnv(t, repoDir)
			t.Setenv("FAKE_FIX_TARGET_FILE", filepath.Join(repoDir, target))

			marker := filepath.Join(t.TempDir(), "test-ran")
			j := startJev(t, scriptBugfixMedium())
			opts := defaultConfigOptions()
			opts.jevURL = j.URL()
			opts.testCommand = "touch " + marker + " && go test ./..."
			cfg := writeConfig(t, configDir, opts)
			if target == "orchestrator.yaml" {
				commitAll(t, repoDir, "add config")
			}

			_, _, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go")
			var wfErr *apperr.WorkflowError
			if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
				t.Fatalf("expected a WorkflowError (exit 4), got %T: %v", err, err)
			}
			wantPath := target
			if strings.HasPrefix(target, ".git/") {
				wantPath = ".git (config or hooks)"
			}
			if !strings.Contains(err.Error(), wantPath) || !strings.Contains(err.Error(), "Restore point:") {
				t.Errorf("the message must name %q and the restore point:\n%v", wantPath, err)
			}
			if _, statErr := os.Stat(marker); statErr == nil {
				t.Error("test.command ran after a protected-path violation")
			}
			if got := invocations(t, invocationsLog); !equalStrings(got, []string{"investigate", "implement"}) {
				t.Errorf("invocations = %v, want [investigate implement]", got)
			}
		})
	}
}

// assertRestorePointPinned checks that refs/orchestrator/runs/<id> names the run's
// pre_run_ref, so git gc cannot prune it.
func assertRestorePointPinned(t *testing.T, repoDir string) {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var run struct {
		ID        string `json:"id"`
		PreRunRef string `json:"pre_run_ref"`
	}
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		t.Fatal(err)
	}
	pinned, err := gitOutput(repoDir, "rev-parse", "--verify", "refs/orchestrator/runs/"+run.ID)
	if err != nil || pinned != run.PreRunRef {
		t.Errorf("refs/orchestrator/runs/%s = %q (%v), want pre_run_ref %s", run.ID, pinned, err, run.PreRunRef)
	}
}

func preRunRef(t *testing.T, repoDir string) string {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var run struct {
		PreRunRef string `json:"pre_run_ref"`
	}
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		t.Fatal(err)
	}
	return run.PreRunRef
}
