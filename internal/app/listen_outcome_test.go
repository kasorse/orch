package app

import (
	"fmt"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/listener"
)

// The listener reads a failed run's reason, the error that ended it as rendered, to decide its
// outcome on restart (listener.ExitFromRun). It must agree with the exit code and the failure kind
// the child itself would have printed for that error.
func TestExitFromRunAgreesWithTheChildsExitCode(t *testing.T) {
	errs := []error{
		&apperr.ProviderError{Provider: "claude", Action: "implement", Kind: apperr.FailureExit, ExitCode: 7},
		&apperr.ProviderError{Provider: "claude", Action: "implement", Kind: apperr.FailureTimeout},
		&apperr.ProviderError{Provider: "claude", Action: "implement", Kind: apperr.FailureRateLimit},
		&apperr.ProviderError{Provider: "claude", Action: "review", Kind: apperr.FailureAuth, Detail: "claude auth (api): rejected"},
		&apperr.ProviderError{Provider: "claude", Action: "implement", Kind: apperr.FailureCancelled},
		fmt.Errorf("implement: %w", &apperr.ProviderError{Provider: "codex", Kind: apperr.FailureAuth}),
		&apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionUnavailable, StatusCode: 503, Attempts: 3},
		&apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionAuth, StatusCode: 401},
		&apperr.DecisionError{Provider: "jev", Kind: apperr.DecisionCancelled},
	}
	for _, err := range errs {
		code, kind := listener.ExitFromRun(domain.RunFailed, err.Error(), false)
		if want := exitcode.FromError(err); code != want {
			t.Errorf("ExitFromRun(%q) = %d, want %d", err.Error(), code, want)
		}
		if want := failureKind(err); kind != want {
			t.Errorf("ExitFromRun(%q) kind = %q, want %q", err.Error(), kind, want)
		}
	}
}
