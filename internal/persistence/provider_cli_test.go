package persistence

import (
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func TestValidateProviderCLI(t *testing.T) {
	ok := map[string]domain.ProviderCLI{"claude": {Version: "2.1.289 (Claude Code)", Verified: true}}
	if err := validateProviderCLI(ok); err != nil {
		t.Errorf("valid entry: %v", err)
	}
	for name, m := range map[string]map[string]domain.ProviderCLI{
		"unknown provider": {"gpt": {Version: "1"}},
		"too long":         {"claude": {Version: strings.Repeat("9", 65)}},
		"unprintable":      {"claude": {Version: "2.1.289\x1b[31m"}},
	} {
		if err := validateProviderCLI(m); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestValidateRun_PreRunTest(t *testing.T) {
	run := func(r *domain.TestResult) *domain.Run {
		return &domain.Run{ID: "r", Status: domain.RunFailed, PreRunTest: r}
	}
	if err := validateRun(run(&domain.TestResult{ExitCode: 1}), "r"); err != nil {
		t.Errorf("a red pre-run test is valid: %v", err)
	}
	if err := validateRun(run(&domain.TestResult{ExitCode: 0, Success: true}), "r"); err != nil {
		t.Errorf("a green pre-run test is valid: %v", err)
	}
	for name, r := range map[string]*domain.TestResult{
		"success with a failing exit": {ExitCode: 2, Success: true},
		"failure with exit 0":         {ExitCode: 0},
		"negative duration":           {ExitCode: 1, Duration: -1},
	} {
		if err := validateRun(run(r), "r"); err == nil || !strings.Contains(err.Error(), "pre_run_test") {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestValidateRun_SecretScan(t *testing.T) {
	fp := strings.Repeat("ab", 32)
	ok := &domain.Run{ID: "r", Status: domain.RunRunning, SecretScanBaseline: []string{fp},
		Executions:     []domain.StepExecution{{Index: 0, Action: domain.ActionImplement, Status: domain.StepCompleted}},
		SecretFindings: []domain.SecretFinding{{StepIndex: 1, File: "pkg/cfg.go", Line: 3, Rule: "aws-access-key-id", Fingerprint: fp}}}
	if err := validateRun(ok, "r"); err != nil {
		t.Fatalf("valid run: %v", err)
	}
	for name, edit := range map[string]func(r *domain.Run){
		"baseline not hex":  func(r *domain.Run) { r.SecretScanBaseline = []string{"zz"} },
		"unknown rule":      func(r *domain.Run) { r.SecretFindings[0].Rule = "magic" },
		"absolute file":     func(r *domain.Run) { r.SecretFindings[0].File = "/etc/passwd" },
		"escaping file":     func(r *domain.Run) { r.SecretFindings[0].File = "../x" },
		"unclean file":      func(r *domain.Run) { r.SecretFindings[0].File = "a/../b" },
		"line zero":         func(r *domain.Run) { r.SecretFindings[0].Line = 0 },
		"step out of range": func(r *domain.Run) { r.SecretFindings[0].StepIndex = 5 },
		"bad fingerprint":   func(r *domain.Run) { r.SecretFindings[0].Fingerprint = "x" },
	} {
		r := *ok
		r.SecretScanBaseline = slices.Clone(ok.SecretScanBaseline)
		r.SecretFindings = slices.Clone(ok.SecretFindings)
		edit(&r)
		if err := validateRun(&r, "r"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
