package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/app"
	"orchestrator/internal/config"
	"orchestrator/internal/testutil/fakeexec"
)

// The org config layer (specs/governance/SPEC.md §6, GV04). The tests name the org file
// through ORCHESTRATOR_ORG_CONFIG, which applies only while the /etc org file does not exist.
// TestMain points that location at a path that never exists, so the host's own
// /etc/orchestrator/org.yaml is never read; useEtcOrgConfig puts a file there for a test.

// absentEtcOrgConfig is the /etc org file location of every test that does not set its own.
var absentEtcOrgConfig string

// useEtcOrgConfig writes an org file with body, makes it the /etc one for the test and returns
// its path.
func useEtcOrgConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "etc-org.yaml")
	writeOrgConfig(t, path, body)
	restore := app.SetOrgConfigEtcPath(path)
	t.Cleanup(restore)
	return path
}

type orgRecord struct {
	OrgConfig *orgRef `json:"org_config"`
	Resumes   []struct {
		OrgConfig *orgRef `json:"org_config"`
	} `json:"resumes"`
}

type orgRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func loadOrgRecord(t *testing.T, repoDir string) orgRecord {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var r orgRecord
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// useOrgConfig writes an org file with body, points ORCHESTRATOR_ORG_CONFIG at it and returns
// its path.
func useOrgConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "org.yaml")
	writeOrgConfig(t, path, body)
	t.Setenv(config.OrgConfigEnv, path)
	return path
}

func writeOrgConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("version: 1\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestOrgConfig_ValidatePrintsTheSource(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	orgPath := useOrgConfig(t, "security:\n  protected_paths: [\"secrets/**\"]\n")

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "validate")
	mustSucceed(t, stdout, stderr, err)
	assertContains(t, "stdout", stdout, "org config: "+orgPath)
}

func TestOrgConfig_ARepositoryConflictIsAConfigError(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	orgPath := useOrgConfig(t, "security:\n  require_clean_worktree: true\n")

	cfg := jevConfig(t, j, func(o *configOptions) { o.security = "  require_clean_worktree: false\n" })
	_, _, err := runOrchestrator(t, repoDir, cfg, "validate")
	if err == nil {
		t.Fatal("expected the conflict")
	}
	assertContains(t, "error", err.Error(), "security.require_clean_worktree: false is weaker than "+orgPath, cfg)
}

func TestOrgConfig_RunAndResumeRecordTheirOwn(t *testing.T) {
	repoDir := dirtyFixture(t)
	decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j)
	orgPath := useOrgConfig(t, "security:\n  protected_paths: [\"secrets/**\"]\n")
	first := sha256Hex(t, orgPath)

	if _, _, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go"); err == nil {
		t.Fatal("expected the dirty-tree refusal")
	}
	refused := loadResumedRun(t, repoDir)
	gitRun(t, repoDir, "add", "notes.txt")
	gitRun(t, repoDir, "commit", "-qm", "notes")

	rec := loadOrgRecord(t, repoDir)
	if rec.OrgConfig == nil || rec.OrgConfig.Path != orgPath || rec.OrgConfig.SHA256 != first {
		t.Fatalf("run.org_config = %+v, want %s %s", rec.OrgConfig, orgPath, first)
	}

	// The org file changes between the run and the resume.
	writeOrgConfig(t, orgPath, "security:\n  protected_paths: [\"secrets/**\", \"infra/**\"]\n")
	second := sha256Hex(t, orgPath)
	if second == first {
		t.Fatal("the org file did not change")
	}
	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "resume", refused.ID)
	mustSucceed(t, stdout, stderr, err)
	assertContains(t, "stderr", stderr,
		"Warning: org config changed since the run started ("+first+" → "+second+"); this resume runs under the current one")

	rec = loadOrgRecord(t, repoDir)
	if rec.OrgConfig == nil || rec.OrgConfig.SHA256 != first {
		t.Errorf("the run's record must not change: %+v", rec.OrgConfig)
	}
	if len(rec.Resumes) != 1 || rec.Resumes[0].OrgConfig == nil || rec.Resumes[0].OrgConfig.SHA256 != second {
		t.Errorf("resumes = %+v, want one entry with %s", rec.Resumes, second)
	}
}

func TestOrgConfig_ResumeUnderTheSameFileIsSilent(t *testing.T) {
	repoDir := dirtyFixture(t)
	decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j)
	useOrgConfig(t, "security:\n  protected_paths: [\"secrets/**\"]\n")

	if _, _, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go"); err == nil {
		t.Fatal("expected the dirty-tree refusal")
	}
	refused := loadResumedRun(t, repoDir)
	gitRun(t, repoDir, "add", "notes.txt")
	gitRun(t, repoDir, "commit", "-qm", "notes")

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "resume", refused.ID)
	mustSucceed(t, stdout, stderr, err)
	if strings.Contains(stderr, "org config changed") {
		t.Errorf("unexpected warning:\n%s", stderr)
	}
}
