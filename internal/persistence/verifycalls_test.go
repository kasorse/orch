package persistence

import (
	"strings"
	"testing"
	"time"

	"orchestrator/internal/domain"
)

func TestValidateRun_VerifyCalls(t *testing.T) {
	run := func(action domain.ActionType, calls ...domain.VerifyCall) *domain.Run {
		return &domain.Run{ID: "r", Status: domain.RunFailed, Executions: []domain.StepExecution{
			{Index: 0, Action: action, Status: domain.StepFailed, VerifyCalls: calls},
		}}
	}
	good := []domain.VerifyCall{
		{Tool: domain.VerifyToolRunTests, ExitCode: 1, Duration: 3 * time.Second, OutputCut: true},
		{Tool: domain.VerifyToolRunTests, Filter: "pkg/a.TestX", Success: true, Duration: time.Second},
		{Tool: domain.VerifyToolRunCheck, Check: "vet", Success: true},
		{Tool: domain.VerifyToolRunCheck, Check: "vet", ExitCode: -1, Duration: time.Minute},
		{Tool: domain.VerifyToolRunCheck, Refused: "invalid arguments: name is required"},
		{Tool: domain.VerifyToolRunCheck, Check: "vet", Refused: "limit reached"},
	}
	if err := validateRun(run(domain.ActionImplement, good...), "r"); err != nil {
		t.Fatalf("valid verify calls: %v", err)
	}

	many := make([]domain.VerifyCall, domain.MaxRecordedCalls+1)
	for i := range many {
		many[i] = domain.VerifyCall{Tool: domain.VerifyToolRunTests, Success: true}
	}
	if err := validateRun(run(domain.ActionImplement, many[:domain.MaxRecordedCalls]...), "r"); err != nil {
		t.Fatalf("%d entries: %v", domain.MaxRecordedCalls, err)
	}

	one := func(c domain.VerifyCall) []domain.VerifyCall { return []domain.VerifyCall{c} }
	for _, tc := range []struct {
		name string
		run  *domain.Run
		want string
	}{
		{"on a review step", run(domain.ActionReview, good[:1]...), "recorded on a review step"},
		{"too many", run(domain.ActionImplement, many...), "at most 100"},
		{"unknown tool", run(domain.ActionImplement, one(domain.VerifyCall{Tool: "rm"})...), `unknown tool "rm"`},
		{"check on run_tests", run(domain.ActionImplement, one(domain.VerifyCall{Tool: domain.VerifyToolRunTests, Check: "vet", Success: true})...), "check"},
		{"run_check without a check", run(domain.ActionImplement, one(domain.VerifyCall{Tool: domain.VerifyToolRunCheck, Success: true})...), "without a check"},
		{"bad check name", run(domain.ActionImplement, one(domain.VerifyCall{Tool: domain.VerifyToolRunCheck, Check: "../x", Success: true})...), "not a check name"},
		{"filter on run_check", run(domain.ActionImplement, one(domain.VerifyCall{Tool: domain.VerifyToolRunCheck, Check: "vet", Filter: "a", Success: true})...), "filter"},
		{"bad filter", run(domain.ActionImplement, one(domain.VerifyCall{Tool: domain.VerifyToolRunTests, Filter: "a b", Success: true})...), "filter"},
		{"negative duration", run(domain.ActionImplement, one(domain.VerifyCall{Tool: domain.VerifyToolRunTests, Success: true, Duration: -1})...), "duration"},
		{"success without exit 0", run(domain.ActionImplement, one(domain.VerifyCall{Tool: domain.VerifyToolRunTests, ExitCode: 2, Success: true})...), "inconsistent result"},
		{"multi-line refused", run(domain.ActionImplement, one(domain.VerifyCall{Tool: domain.VerifyToolRunTests, Refused: "a\nb"})...), "refused"},
		{"long refused", run(domain.ActionImplement, one(domain.VerifyCall{Tool: domain.VerifyToolRunTests, Refused: strings.Repeat("x", 301)})...), "refused"},
		{"refused with a result", run(domain.ActionImplement, one(domain.VerifyCall{Tool: domain.VerifyToolRunTests, Refused: "limit reached", ExitCode: 1})...), "refused call carries a result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRun(tc.run, "r")
			if err == nil || !strings.Contains(err.Error(), "verify_calls") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateRun() = %v, want a verify_calls error containing %q", err, tc.want)
			}
		})
	}

	o := domain.Outcome{Status: domain.RunFailed, VerifyCalls: -1}
	if err := validateRun(&domain.Run{ID: "r", Status: domain.RunFailed, Outcome: &o}, "r"); err == nil {
		t.Error("a negative outcome.verify_calls was accepted")
	}
}
