package exitcode

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"orchestrator/internal/apperr"
)

func TestFromError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, OK},
		{"generic", errors.New("boom"), GenericError},
		{"config", apperr.NewConfigError("bad config", nil), ConfigOrDependency},
		{"dependency", apperr.NewDependencyError("missing dep", nil), ConfigOrDependency},
		{"authority", &apperr.AuthorityError{Provider: "codex", Authority: "read"}, ConfigOrDependency},
		{"authority wrapped", fmt.Errorf("validation: %w", errors.Join(&apperr.AuthorityError{Provider: "codex"})), ConfigOrDependency},
		{"provider", &apperr.ProviderError{Provider: "claude"}, ProviderExecution},
		{"provider exit", &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureExit}, ProviderExecution},
		{"provider timeout", &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureTimeout}, ProviderExecution},
		{"provider spawn", &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureSpawn}, ProviderExecution},
		{"provider cancelled", &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureCancelled}, UserCancellation},
		{"provider auth", &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureAuth}, ConfigOrDependency},
		{"provider auth wrapped", fmt.Errorf("step: %w", &apperr.ProviderError{Provider: "claude", Kind: apperr.FailureAuth}), ConfigOrDependency},
		{"decision unavailable", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionUnavailable}, ProviderExecution},
		{"decision timeout", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionTimeout}, ProviderExecution},
		{"decision bad request", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionBadRequest}, ProviderExecution},
		{"decision auth", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionAuth}, ConfigOrDependency},
		{"decision cancelled", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionCancelled}, UserCancellation},
		{"invalid planning answer", &apperr.InvalidResponseError{Call: "specify", Field: "tasks[1].repo"}, ProviderExecution},
		{"invalid planning answer wrapped", fmt.Errorf("story plan: %w", &apperr.InvalidResponseError{Call: "survey"}), ProviderExecution},
		{"jira auth", &apperr.JiraError{Kind: apperr.JiraAuth}, ConfigOrDependency},
		{"jira not found", &apperr.JiraError{Kind: apperr.JiraNotFound}, ConfigOrDependency},
		{"jira redirect", &apperr.JiraError{Kind: apperr.JiraRedirect}, ConfigOrDependency},
		{"jira timeout", &apperr.JiraError{Kind: apperr.JiraTimeout}, ProviderExecution},
		{"jira connection", &apperr.JiraError{Kind: apperr.JiraConnection}, ProviderExecution},
		{"jira rate limited", &apperr.JiraError{Kind: apperr.JiraRateLimited}, ProviderExecution},
		{"jira unavailable", &apperr.JiraError{Kind: apperr.JiraUnavailable}, ProviderExecution},
		{"jira invalid response", &apperr.JiraError{Kind: apperr.JiraInvalidResponse}, ProviderExecution},
		{"jira rejected", &apperr.JiraError{Kind: apperr.JiraRejected}, ConfigOrDependency},
		{"jira cancelled", &apperr.JiraError{Kind: apperr.JiraCancelled}, UserCancellation},
		{"jira wrapped", fmt.Errorf("fetch: %w", &apperr.JiraError{Kind: apperr.JiraAuth}), ConfigOrDependency},
		{"limit", &apperr.LimitExceededError{Reason: "too many steps"}, WorkflowOrLimit},
		{"workflow", &apperr.WorkflowError{Msg: "bad transition"}, WorkflowOrLimit},
		{"cancel", context.Canceled, UserCancellation},
		{"needs a person", &apperr.NeedsPersonError{Msg: "verdict fail"}, NeedsPerson},
		{"needs a person wrapped", fmt.Errorf("review: %w", &apperr.NeedsPersonError{Msg: "verdict fail"}), NeedsPerson},
		{"run not found", &apperr.RunStateError{Kind: apperr.RunNotFound}, GenericError},
		{"run ambiguous", &apperr.RunStateError{Kind: apperr.RunAmbiguous}, GenericError},
		{"run in use", &apperr.RunStateError{Kind: apperr.RunInUse}, GenericError},
		{"run not resumable", &apperr.RunStateError{Kind: apperr.RunNotResumable}, GenericError},
		{"run corrupt", &apperr.RunStateError{Kind: apperr.RunCorrupt}, GenericError},
		{"run corrupt wrapping workflow", &apperr.RunStateError{Kind: apperr.RunCorrupt, Err: &apperr.WorkflowError{Msg: "x"}}, GenericError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FromError(tc.err); got != tc.want {
				t.Errorf("FromError(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
