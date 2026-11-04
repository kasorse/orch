package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/story"
)

// TestFixCauseHint: the last red test or failed review of the run is what led to its stop, with
// where its output is (TODO.md S8).
func TestFixCauseHint(t *testing.T) {
	store := persistence.NewStore("/src/billing/.orchestrator/runs")
	const id = "0b6f3c1e-1111-4222-8333-444455556666"
	implement := domain.StepExecution{Index: 0, Action: domain.ActionImplement, Status: domain.StepCompleted}
	failed := domain.StepExecution{Index: 2, Action: domain.ActionImplement, Status: domain.StepFailed}
	red := func(r domain.TestResult) domain.StepExecution {
		return domain.StepExecution{Index: 1, Action: domain.ActionTest, Status: domain.StepCompleted, TestResult: &r}
	}
	review := domain.StepExecution{Index: 3, Action: domain.ActionReview, Status: domain.StepCompleted, ArtifactPath: "artifacts/003-review.json",
		ReviewResult: &domain.ReviewResult{
			Issues:   []domain.ReviewIssue{{Severity: domain.SeverityHigh}, {Severity: domain.SeverityLow}},
			Criteria: []domain.CriterionVerdict{{Index: 1, Met: false}},
		}}
	runDir := store.RunDir(id)
	cases := []struct {
		name  string
		execs []domain.StepExecution
		want  string
	}{
		{"failing tests", []domain.StepExecution{implement, red(domain.TestResult{ExitCode: 1,
			FailedTests: []string{"TestA", "TestB", "TestC", "TestD", "TestE", "TestF", "TestG"}}), failed},
			"Led to it: step 1 (test) was red, 7 failing test(s): TestA, TestB, TestC, TestD, TestE and 2 more; its output: " +
				filepath.Join(runDir, "logs", "001-test.stdout.log")},
		{"a check", []domain.StepExecution{implement, red(domain.TestResult{ExitCode: 1, FailedCheck: "gofmt"}), failed},
			"Led to it: step 1 (test) was red, the check gofmt failed (exit code 1); its output: " +
				filepath.Join(runDir, "logs", "001-test.gofmt.stdout.log")},
		{"no names", []domain.StepExecution{implement, red(domain.TestResult{ExitCode: 2})},
			"Led to it: step 1 (test) was red, exit code 2; its output: " + filepath.Join(runDir, "logs", "001-test.stdout.log")},
		{"the later review", []domain.StepExecution{implement, red(domain.TestResult{ExitCode: 1}), review},
			"Led to it: step 3 (review) failed, 1 critical or high issue(s), 1 unmet criteria; the review: " +
				filepath.Join(runDir, "artifacts", "003-review.json")},
		{"neither", []domain.StepExecution{implement, failed}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(fixCauseHint(&domain.Run{ID: id, Executions: tc.execs}, store), "\n")
			if got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestHeldByStop: the ready tasks a stop kept from starting, without the tasks that stopped it and
// those that wait for them.
func TestHeldByStop(t *testing.T) {
	r := &storyRunner{loc: storyLocation{slug: "refunds"}}
	r.spec.tasks = []story.Task{
		{ID: "RF01", Repo: "billing"},
		{ID: "RF02", Repo: "gateway"},
		{ID: "RF03", Repo: "billing", DependsOn: []string{"RF01"}},
		{ID: "RF04", Repo: "gateway"},
	}
	r.byID = map[string]story.Task{}
	for _, task := range r.spec.tasks {
		r.byID[task.ID] = task
	}
	r.rec.Tasks = map[string]persistence.StoryTask{
		"RF01": {Status: story.TaskFailed, RunID: "x"},
		"RF04": {Status: story.TaskDone},
	}
	if got := strings.Join(r.heldByStop([]string{"RF01"}), ","); got != "RF02" {
		t.Errorf("heldByStop = %s, want RF02", got)
	}
}

// TestNoteUnreadable: an unreadable run of this story is warned about at once, the others are
// counted once per runs directory, and a directory seen twice counts once.
func TestNoteUnreadable(t *testing.T) {
	runs := filepath.Join(t.TempDir(), "billing", ".orchestrator", "runs")
	write := func(name, content string) persistence.UnreadableDir {
		dir := filepath.Join(runs, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return persistence.UnreadableDir{Dir: dir, Err: os.ErrInvalid}
	}
	ours := write("a", `{"task":{"source":{"kind":"story","story":"refunds","task":"RF01"}}}`)
	other := write("b", `{"task":{"source":{"kind":"story","story":"another"}}}`)
	plain := write("c", `{"workflow":{"steps":[{"action":"analyze"}]}}`)

	var stderr bytes.Buffer
	r := &storyRunner{loc: storyLocation{slug: "refunds"}, stderr: &stderr}
	bad := []persistence.UnreadableDir{ours, other, plain}
	r.noteUnreadable(runs, bad)
	r.noteUnreadable(runs, bad)
	r.reportUnreadable()

	got := stderr.String()
	if n := strings.Count(got, "Warning: "+ours.Dir+" is a run of this story"); n != 1 {
		t.Errorf("the story's run is warned about %d times, want once:\n%s", n, got)
	}
	if !strings.Contains(got, "Warning: 2 run(s) in "+runs+" cannot be read by this version") ||
		!strings.Contains(got, "`orchestrator runs clean --unreadable` in "+filepath.Dir(filepath.Dir(runs))) {
		t.Errorf("no count line for the other two:\n%s", got)
	}
	if strings.Count(got, "\n") != 2 {
		t.Errorf("want two lines:\n%s", got)
	}
}
