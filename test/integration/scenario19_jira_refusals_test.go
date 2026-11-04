package integration

import (
	"errors"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
)

// Scenario 19, refusals (specs/jira/SPEC.md §14, J2): a ticket that cannot be read ends
// the command with exit 2 before any run exists and before any provider or Jev call.

// assertRefusedBeforeRun checks the common outcome: exit 2, no run directory, no AI
// call, no decision request.
func assertRefusedBeforeRun(t *testing.T, r jiraRun, err error) {
	t.Helper()
	if got := exitcode.FromError(err); got != exitcode.ConfigOrDependency {
		t.Errorf("exit code = %d, want %d (%v)", got, exitcode.ConfigOrDependency, err)
	}
	assertNoRuns(t, r.repoDir)
	if got := invocations(t, r.invocationsLog); len(got) != 0 {
		t.Errorf("expected no provider call, got %v", got)
	}
	if n := len(r.jev.Bodies()); n != 0 {
		t.Errorf("expected no Jev request, got %d", n)
	}
}

// 19c: an issue that does not exist (or is not visible) is exit 2, after one request.
func TestScenario19c_JiraNotFound(t *testing.T) {
	r := newJiraRun(t)

	_, _, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--jira", "PMC-404")
	var jerr *apperr.JiraError
	if !errors.As(err, &jerr) || jerr.Kind != apperr.JiraNotFound {
		t.Fatalf("expected a not_found JiraError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "PMC-404 does not exist, or the account behind JIRA_EMAIL cannot see it") {
		t.Errorf("message = %v", err)
	}
	assertRefusedBeforeRun(t, r, err)
	if n := len(r.jira.Requests()); n != 1 {
		t.Errorf("Jira got %d requests, want 1", n)
	}
}

// 19d: a missing token stops the fail-fast chain, before any request.
func TestScenario19d_JiraMissingToken(t *testing.T) {
	r := newJiraRun(t)
	unsetenv(t, "JIRA_API_TOKEN")

	_, _, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--jira", "PMC-1")
	var depErr *apperr.DependencyError
	if !errors.As(err, &depErr) || !strings.Contains(err.Error(), "jira.token_env names JIRA_API_TOKEN, which is not set") {
		t.Fatalf("expected the missing-token DependencyError, got %T: %v", err, err)
	}
	assertRefusedBeforeRun(t, r, err)
	if n := len(r.jira.Requests()); n != 0 {
		t.Errorf("Jira got %d requests, want 0", n)
	}
}

// 19e: a URL on another host is refused before any request; the request would only
// ever go to jira.base_url anyway.
func TestScenario19e_JiraForeignHost(t *testing.T) {
	r := newJiraRun(t)

	_, _, err := runOrchestrator(t, r.repoDir, r.cfg, "run", "--jira", "https://evil.example/browse/PMC-1")
	var cfgErr *apperr.ConfigError
	if !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), `--jira URL host "evil.example" does not match jira.base_url`) {
		t.Fatalf("expected the host-mismatch ConfigError, got %T: %v", err, err)
	}
	assertRefusedBeforeRun(t, r, err)
	if n := len(r.jira.Requests()); n != 0 {
		t.Errorf("Jira got %d requests, want 0", n)
	}
}
