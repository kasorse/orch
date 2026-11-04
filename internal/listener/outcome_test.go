package listener

import (
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

func TestDecideIsTheTableOfSpec8(t *testing.T) {
	tests := []struct {
		name string
		in   RunOutcome
		want Decision
	}{
		{"delivered", RunOutcome{ExitCode: 0, MRCreated: true}, Decision{Comment: CommentMergeRequest, Transition: ResultDone, Counted: true}},
		{"exit 0 without a merge request", RunOutcome{ExitCode: 0}, Decision{Comment: CommentFailure, Transition: ResultFailed, Counted: true}},
		{"needs input", RunOutcome{ExitCode: 4, FailureReason: domain.FailureTaskNeedsInput}, Decision{Comment: CommentNeedsInput, Transition: ResultBlocked, Counted: true}},
		{"red tests", RunOutcome{ExitCode: 4, FailureReason: "maximum fix iterations exceeded"}, Decision{Comment: CommentFailure, Transition: ResultFailed, Counted: true}},
		{"exit 6", RunOutcome{ExitCode: 6}, Decision{Comment: CommentFailure, Transition: ResultFailed, Counted: true}},
		{"auth", RunOutcome{ExitCode: 2, FailureKind: "auth"}, Decision{Transition: ResultRetry, Stop: true}},
		{"config", RunOutcome{ExitCode: 2}, Decision{Comment: CommentError, Transition: ResultFailed, Counted: true}},
		{"error", RunOutcome{ExitCode: 1}, Decision{Comment: CommentError, Transition: ResultFailed, Counted: true}},
		{"provider", RunOutcome{ExitCode: 3}, Decision{Transition: ResultRetry, Backoff: true}},
		{"cancelled", RunOutcome{ExitCode: 5}, Decision{Transition: ResultNone}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.in); got != tc.want {
				t.Errorf("Decide(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestExitFromRun(t *testing.T) {
	const provider = "provider execution failed\n\nprovider: claude\naction: implement\nmodel: m\n"
	tests := []struct {
		status   domain.RunStatus
		reason   string
		mr       bool
		want     int
		wantKind string
	}{
		{domain.RunCompleted, "", true, ExitOK, ""},
		{domain.RunCompleted, "", false, ExitNeeds, ""},
		{domain.RunStopped, "execution cancelled", false, ExitCancelled, ""},
		{domain.RunStopped, "maximum fix iterations exceeded", false, ExitWorkflow, ""},
		{domain.RunStopped, domain.FailureTaskNeedsInput, false, ExitWorkflow, ""},
		{domain.RunFailed, domain.FailureTaskNeedsInput, false, ExitWorkflow, ""},
		{domain.RunFailed, "boom", false, ExitError, ""},
		{domain.RunFailed, provider + "failure: exit (the CLI ran and exited non-zero)\nexit code: 7\n", false, ExitProvider, ""},
		{domain.RunFailed, "implement: " + provider + "failure: auth (the CLI did not use the run's credential)\n", false, ExitConfig, FailureKindAuth},
		{domain.RunFailed, provider + "failure: cancelled (interrupted)\n", false, ExitCancelled, ""},
		{domain.RunFailed, "decision provider jev failed: unavailable (the service is overloaded or failing), HTTP 503", false, ExitProvider, ""},
		{domain.RunFailed, "decision provider jev failed: auth (the API key is missing, invalid, or not permitted), HTTP 401", false, ExitConfig, FailureKindAuth},
		{domain.RunFailed, "decision provider jev failed: cancelled (interrupted)", false, ExitCancelled, ""},
	}
	for _, tc := range tests {
		got, kind := ExitFromRun(tc.status, tc.reason, tc.mr)
		if got != tc.want || kind != tc.wantKind {
			t.Errorf("ExitFromRun(%s, %q, %v) = %d, %q, want %d, %q", tc.status, tc.reason, tc.mr, got, kind, tc.want, tc.wantKind)
		}
	}
}

// A provider failure found on restart is still a retry with no attempt, and a credential still stops.
func TestARunFoundFinishedOnRestartIsDecidedAsItsChildWouldHaveBeen(t *testing.T) {
	code, kind := ExitFromRun(domain.RunFailed, "provider execution failed\n\nfailure: rate_limit (the account's usage limit was reached)\n", false)
	if d := Decide(RunOutcome{ExitCode: code, FailureKind: kind}); d.Transition != ResultRetry || d.Counted || !d.Backoff {
		t.Errorf("provider failure: %+v, want retry with no attempt", d)
	}
	code, kind = ExitFromRun(domain.RunFailed, "provider execution failed\n\nfailure: auth (x)\n", false)
	if d := Decide(RunOutcome{ExitCode: code, FailureKind: kind}); d.Transition != ResultRetry || d.Counted || !d.Stop {
		t.Errorf("auth: %+v, want retry, no attempt, and a stop", d)
	}
}

func TestCutOffResumesEveryEndButAVerdictWhileStopping(t *testing.T) {
	for code := ExitOK; code <= ExitNeeds; code++ {
		if CutOff(code, false) {
			t.Errorf("CutOff(%d, false) = true: nothing is cut off while the listener runs", code)
		}
		want := code != ExitOK && code != ExitWorkflow && code != ExitNeeds
		if got := CutOff(code, true); got != want {
			t.Errorf("CutOff(%d, true) = %v, want %v", code, got, want)
		}
	}
}
