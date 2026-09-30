package integration

import (
	"strings"
	"testing"
)

// 19f (specs/jira/SPEC.md §11.3): resuming a Jira run never contacts Jira and needs no
// Jira variables; the persisted task is the task.
func TestScenario19f_JiraResume(t *testing.T) {
	r := newJiraRun(t)

	t.Setenv("FAKE_FAIL_ACTION", "implement")
	if _, _, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--jira", "PMC-1"); err == nil {
		t.Fatal("expected implement to fail the run")
	}
	t.Setenv("FAKE_FAIL_ACTION", "")
	failed := loadResumedRun(t, r.repoDir)
	if failed.Status != "failed" {
		t.Fatalf("run status = %s, want failed", failed.Status)
	}
	unsetenv(t, "JIRA_EMAIL")
	unsetenv(t, "JIRA_API_TOKEN")

	stdout, stderr, err := resume(t, r.repoDir, r.cfg, failed.ID)
	mustSucceed(t, stdout, stderr, err)
	if n := len(r.jira.Requests()); n != 1 {
		t.Errorf("Jira got %d requests over run and resume, want 1", n)
	}
	lines := strings.Split(stdout, "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[2], "Resuming run "+failed.ID) ||
		!strings.HasPrefix(lines[3], "Task from PMC-1, fetched ") || !strings.HasSuffix(lines[3], " (not re-fetched)") {
		t.Errorf("the resume header lacks the source line:\n%s", stdout)
	}
	if got := loadTaskSource(t, r.repoDir); got.Task.Description != pmc1Rendered(r.jira.URL(), "") {
		t.Errorf("the task changed on resume:\n%q", got.Task.Description)
	}
}
