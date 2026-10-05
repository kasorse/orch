package integration

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 24 (specs/hardening/SPEC.md §7): a claude CLI outside the verified range is
// refused before any call, unless security.allow_unverified_cli says otherwise; the version
// is recorded in run.json and in each resume entry.

const allowUnverified = "\nsecurity:\n  allow_unverified_cli: true\n"

type recordedCLI struct {
	ProviderCLI map[string]struct {
		Version  string `json:"version"`
		Verified bool   `json:"verified"`
	} `json:"provider_cli"`
	Resumes []struct {
		ProviderCLI map[string]struct {
			Version  string `json:"version"`
			Verified bool   `json:"verified"`
		} `json:"provider_cli"`
	} `json:"resumes"`
}

func loadRecordedCLI(t *testing.T, repoDir string) recordedCLI {
	t.Helper()
	raw, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json was not written")
	}
	var r recordedCLI
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestScenario24_UnverifiedCLIRefused(t *testing.T) {
	for _, command := range []string{"run", "validate"} {
		t.Run(command, func(t *testing.T) {
			repoDir := fakeexec.InitGitFixture(t)
			invocationsLog, _ := decisionEnv(t, repoDir)
			t.Setenv("FAKE_CLI_VERSION", "2.2.0 (Claude Code)")
			j := startJev(t, scriptBugfixMedium())

			args := []string{command}
			if command == "run" {
				args = append(args, "fix Add()")
			}
			_, _, err := runOrchestrator(t, repoDir, jevConfig(t, j), args...)

			var depErr *apperr.DependencyError
			if !errors.As(err, &depErr) || exitcode.FromError(err) != exitcode.ConfigOrDependency {
				t.Fatalf("expected a DependencyError (exit 2), got %T: %v", err, err)
			}
			for _, want := range []string{"claude 2.2.0", ">=2.1.283 <2.2.0", "security.allow_unverified_cli: true"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message lacks %q:\n%v", want, err)
				}
			}
			if got := invocations(t, invocationsLog); len(got) != 0 {
				t.Errorf("no provider call may precede the refusal, got %v", got)
			}
			if got := j.Bodies(); len(got) != 0 {
				t.Errorf("expected no Jev requests, got %d", len(got))
			}
		})
	}
}

func TestScenario24_OverrideRunsAndRecords(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	t.Setenv("FAKE_CLI_VERSION", "2.2.0 (Claude Code)")
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j, func(o *configOptions) { o.extraYAML = allowUnverified })

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add()")
	mustSucceed(t, stdout, stderr, err)

	if !strings.Contains(stdout, "claude 2.2.0 (Claude Code) is outside the verified range >=2.1.283 <2.2.0") {
		t.Errorf("header lacks the warning:\n%s", stdout)
	}
	got := loadRecordedCLI(t, repoDir).ProviderCLI["claude"]
	if got.Version != "2.2.0 (Claude Code)" || got.Verified {
		t.Errorf("provider_cli.claude = %+v", got)
	}
}

func TestScenario24_VerifiedVersionIsRecorded(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())

	stdout, stderr, err := runOrchestrator(t, repoDir, jevConfig(t, j), "run", "fix Add()")
	mustSucceed(t, stdout, stderr, err)
	if strings.Contains(stdout, "outside the verified range") {
		t.Errorf("unexpected warning:\n%s", stdout)
	}
	if got := loadRecordedCLI(t, repoDir).ProviderCLI["claude"]; got.Version != "2.1.289 (Claude Code)" || !got.Verified {
		t.Errorf("provider_cli.claude = %+v", got)
	}
}

func TestScenario24_ResumeRecordsItsVersion(t *testing.T) {
	repoDir := dirtyFixture(t)
	decisionEnv(t, repoDir)
	j := startJev(t, scriptBugfixMedium())
	cfg := jevConfig(t, j)

	if _, _, err := runOrchestrator(t, repoDir, cfg, "run", "fix Add in fixture.go"); err == nil {
		t.Fatal("expected the dirty-tree refusal")
	}
	refused := loadResumedRun(t, repoDir)
	gitRun(t, repoDir, "add", "notes.txt")
	gitRun(t, repoDir, "commit", "-qm", "notes")

	// The CLI was updated between the run and the resume.
	t.Setenv("FAKE_CLI_VERSION", "2.1.300 (Claude Code)")
	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "resume", refused.ID)
	mustSucceed(t, stdout, stderr, err)

	rec := loadRecordedCLI(t, repoDir)
	if got := rec.ProviderCLI["claude"]; got.Version != "2.1.289 (Claude Code)" {
		t.Errorf("run's provider_cli = %+v", got)
	}
	if len(rec.Resumes) != 1 || rec.Resumes[0].ProviderCLI["claude"].Version != "2.1.300 (Claude Code)" || !rec.Resumes[0].ProviderCLI["claude"].Verified {
		t.Errorf("resumes = %+v", rec.Resumes)
	}
}
