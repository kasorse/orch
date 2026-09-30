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
		{"decision unavailable", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionUnavailable}, ProviderExecution},
		{"decision timeout", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionTimeout}, ProviderExecution},
		{"decision bad request", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionBadRequest}, ProviderExecution},
		{"decision auth", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionAuth}, ConfigOrDependency},
		{"decision cancelled", &apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionCancelled}, UserCancellation},
		{"limit", &apperr.LimitExceededError{Reason: "too many steps"}, WorkflowOrLimit},
		{"workflow", &apperr.WorkflowError{Msg: "bad transition"}, WorkflowOrLimit},
		{"cancel", context.Canceled, UserCancellation},
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
