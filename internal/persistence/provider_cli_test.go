package persistence

import (
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
