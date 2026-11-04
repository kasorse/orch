package integration

import (
	"testing"

	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 55 (specs/governance/SPEC.md §12, GV04): ORCHESTRATOR_ORG_CONFIG applies only while
// the /etc org file does not exist. The test injects the /etc location (useEtcOrgConfig), so the
// real /etc is never read.

func TestScenario55_EtcOrgFileWinsOverTheEnvironmentVariable(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newStageRun(t, repoDir)
	// Were the variable's file used, test.sandbox: none would conflict with it and fail the run.
	fromEnv := useOrgConfig(t, "test:\n  sandbox: bwrap\n")
	fromEtc := useEtcOrgConfig(t, "security:\n  protected_paths: [\"etc-only/**\"]\n")
	if fromEnv == fromEtc {
		t.Fatal("the two org files are the same file")
	}

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	rec := loadOrgRecord(t, repoDir)
	if rec.OrgConfig == nil || rec.OrgConfig.Path != fromEtc || rec.OrgConfig.SHA256 != sha256Hex(t, fromEtc) {
		t.Errorf("run.org_config = %+v, want %s %s", rec.OrgConfig, fromEtc, sha256Hex(t, fromEtc))
	}
}
