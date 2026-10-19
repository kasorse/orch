package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// resetMargin is added to the reported reset time, so a clock a little behind the provider's
// does not retry into the same limit.
const resetMargin = time.Minute

// isRateLimit reports whether err is a step the account's usage limit refused, with waiting on
// (execution.rate_limit_wait above 0).
func isRateLimit(err error, fallback time.Duration) bool {
	var pe *apperr.ProviderError
	return fallback > 0 && errors.As(err, &pe) && pe.Kind == apperr.FailureRateLimit
}

// limitWait is how long to sleep before retrying a step the usage limit refused: until the
// reset the CLI reported plus resetMargin, or fallback when it reported none.
func limitWait(resetsAt, now time.Time, fallback time.Duration) time.Duration {
	if resetsAt.IsZero() {
		return fallback
	}
	return max(resetsAt.Sub(now), 0) + resetMargin
}

// waitForLimit sleeps until the usage limit that refused the step resets (true), or until ctx
// ends - Ctrl+C - (false).
func waitForLimit(ctx context.Context, s *session, stepErr error) bool {
	var pe *apperr.ProviderError
	if !errors.As(stepErr, &pe) {
		return false
	}
	wait := limitWait(pe.ResetsAt, time.Now(), s.cfg.Execution.RateLimitWait)
	step := domain.ActionType(pe.Action)
	s.reporter.Warning(step, fmt.Sprintf("%s usage limit reached; retrying the step at %s. "+
		"Ctrl+C stops the run; orchestrator resume %s continues it later.",
		pe.Provider, time.Now().Add(wait).Format("2006-01-02 15:04"), s.run.ID))
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
