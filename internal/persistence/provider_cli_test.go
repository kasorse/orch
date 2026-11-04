package persistence

import (
	"slices"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

func TestValidateProviderCLI(t *testing.T) {
	ok := map[string]domain.ProviderCLI{"claude": {Version: "2.1.289 (Claude Code)", Verified: true},
		"codex": {Verified: true, Transport: domain.TransportAPI, Endpoint: "ai-gateway.example.internal:8443"}}
	if err := validateProviderCLI(ok); err != nil {
		t.Errorf("valid entry: %v", err)
	}
	api := func(endpoint string) domain.ProviderCLI {
		return domain.ProviderCLI{Verified: true, Transport: domain.TransportAPI, Endpoint: endpoint}
	}
	for name, m := range map[string]map[string]domain.ProviderCLI{
		"unknown provider":      {"gpt": {Version: "1"}},
		"too long":              {"claude": {Version: strings.Repeat("9", 65)}},
		"unprintable":           {"claude": {Version: "2.1.289\x1b[31m"}},
		"unknown transport":     {"claude": {Transport: "grpc"}},
		"endpoint without api":  {"claude": {Version: "2.1.289", Endpoint: "api.anthropic.com"}},
		"api with a version":    {"claude": {Version: "2.1.289", Transport: domain.TransportAPI, Endpoint: "api.anthropic.com"}},
		"api without endpoint":  {"claude": api("")},
		"endpoint with a path":  {"claude": api("gw.example/v1")},
		"endpoint with a query": {"claude": api("gw.example?key=x")},
		"endpoint with a user":  {"claude": api("u@gw.example")},
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

// Checks and failed_check (specs/quality/SPEC.md §5.3), in the pre-run test and in a test
// step. A result with neither, as every run written before checks existed, is valid.
func TestValidateRun_Checks(t *testing.T) {
	pass := func(name string) domain.CheckResult { return domain.CheckResult{Name: name, Success: true} }
	fail := func(name string) domain.CheckResult { return domain.CheckResult{Name: name, ExitCode: 1} }
	withPreRun := func(r domain.TestResult) *domain.Run {
		return &domain.Run{ID: "r", Status: domain.RunFailed, PreRunTest: &r}
	}
	withStep := func(r domain.TestResult) *domain.Run {
		return &domain.Run{ID: "r", Status: domain.RunFailed, Executions: []domain.StepExecution{
			{Index: 0, Action: domain.ActionTest, Status: domain.StepCompleted, TestResult: &r}}}
	}

	valid := map[string]domain.TestResult{
		"no checks (an old result)": {ExitCode: 1},
		"green checks, green tests": {Success: true, Checks: []domain.CheckResult{pass("build"), pass("vet")}},
		"green checks, red tests":   {ExitCode: 1, Checks: []domain.CheckResult{pass("build")}},
		"the last check failed":     {ExitCode: 1, FailedCheck: "vet", Checks: []domain.CheckResult{pass("build"), fail("vet")}},
		"the only check failed":     {ExitCode: 1, FailedCheck: "b", Checks: []domain.CheckResult{fail("b")}},
	}
	for name, r := range valid {
		if err := validateRun(withPreRun(r), "r"); err != nil {
			t.Errorf("pre_run_test, %s: %v", name, err)
		}
		if err := validateRun(withStep(r), "r"); err != nil {
			t.Errorf("test step, %s: %v", name, err)
		}
	}

	var tooMany []domain.CheckResult
	for i := 0; i <= domain.MaxChecks; i++ {
		tooMany = append(tooMany, pass(string(rune('a'+i))))
	}
	invalid := map[string]domain.TestResult{
		"failed_check without checks":      {ExitCode: 1, FailedCheck: "vet"},
		"failed_check names a passed one":  {ExitCode: 1, FailedCheck: "build", Checks: []domain.CheckResult{pass("build"), fail("vet")}},
		"failed_check not the last":        {ExitCode: 1, FailedCheck: "build", Checks: []domain.CheckResult{fail("build"), pass("vet")}},
		"failed_check names no entry":      {ExitCode: 1, FailedCheck: "lint", Checks: []domain.CheckResult{fail("vet")}},
		"a failed check without the name":  {ExitCode: 1, Checks: []domain.CheckResult{fail("vet")}},
		"a pass after a failed check":      {ExitCode: 1, FailedCheck: "vet", Checks: []domain.CheckResult{fail("vet"), pass("lint")}},
		"failed_check on a green result":   {Success: true, FailedCheck: "vet", Checks: []domain.CheckResult{fail("vet")}},
		"bad failed_check name":            {ExitCode: 1, FailedCheck: "../x", Checks: []domain.CheckResult{fail("../x")}},
		"bad check name":                   {ExitCode: 1, Checks: []domain.CheckResult{pass("A b")}},
		"empty check name":                 {ExitCode: 1, Checks: []domain.CheckResult{pass("")}},
		"duplicate check name":             {ExitCode: 1, Checks: []domain.CheckResult{pass("vet"), pass("vet")}},
		"success with a failing exit code": {ExitCode: 1, Checks: []domain.CheckResult{{Name: "vet", ExitCode: 2, Success: true}}},
		"negative check duration":          {ExitCode: 1, Checks: []domain.CheckResult{{Name: "vet", Success: true, Duration: -1}}},
		"more than 16 checks":              {ExitCode: 1, Checks: tooMany},
	}
	for name, r := range invalid {
		if err := validateRun(withPreRun(r), "r"); err == nil || !strings.Contains(err.Error(), "pre_run_test") {
			t.Errorf("pre_run_test, %s: got %v", name, err)
		}
		if err := validateRun(withStep(r), "r"); err == nil || !strings.Contains(err.Error(), "test_result") {
			t.Errorf("test step, %s: got %v", name, err)
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
