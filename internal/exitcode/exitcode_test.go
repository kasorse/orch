package exitcode

import (
	"context"
	"errors"
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
		{"provider", &apperr.ProviderError{Provider: "claude"}, ProviderExecution},
		{"limit", &apperr.LimitExceededError{Reason: "too many steps"}, WorkflowOrLimit},
		{"workflow", &apperr.WorkflowError{Msg: "bad transition"}, WorkflowOrLimit},
		{"cancel", context.Canceled, UserCancellation},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FromError(tc.err); got != tc.want {
				t.Errorf("FromError(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
