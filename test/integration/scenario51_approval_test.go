package integration

import (
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 51 (specs/governance/SPEC.md §12, GV03): a rule with approval: manual and
// auto_commit: true. The run completes but commits nothing, says why, and records
// outcome.approval_required.

func TestScenario51_ManualApprovalStopsAutoCommit(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) {
		o.pathRules = "    - match: migrations/**\n      approval: manual\n"
		o.extraYAML = "auto_commit: true\n"
	})
	touchOnImplement(t, repoDir, "migrations/x.sql", 0)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "add migrations/x.sql")...)
	mustSucceed(t, stdout, stderr, err)

	assertContains(t, "stdout", stdout,
		"Not committing automatically: policy.paths requires manual approval: migrations/** (1 file); commit what you want yourself.")
	assertNotContains(t, "stdout", stdout, "Committed ")
	if count, _ := gitOutput(repoDir, "rev-list", "--count", "HEAD"); strings.TrimSpace(count) != "1" {
		t.Errorf("commit count = %s, want 1", count)
	}
	if status, _ := gitOutput(repoDir, "status", "--porcelain"); !strings.Contains(status, "migrations/") {
		t.Errorf("the change is gone from the working tree: %q", status)
	}

	raw := readRunJSON(t, repoDir)
	if _, ok := raw["commit"]; ok {
		t.Errorf("run.json records a commit: %v", raw["commit"])
	}
	outcome, ok := raw["outcome"].(map[string]any)
	if raw["status"] != "completed" || !ok || outcome["approval_required"] != true {
		t.Errorf("status %v, outcome %v, want completed with approval_required", raw["status"], raw["outcome"])
	}
}
