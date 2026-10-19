package app

import (
	"fmt"
	"testing"
	"time"

	"orchestrator/internal/apperr"
)

func TestLimitWait(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	for name, tc := range map[string]struct {
		resets time.Time
		want   time.Duration
	}{
		"reported":       {now.Add(2 * time.Hour), 2*time.Hour + resetMargin},
		"already passed": {now.Add(-time.Hour), resetMargin},
		"not reported":   {time.Time{}, 5 * time.Minute},
	} {
		if got := limitWait(tc.resets, now, 5*time.Minute); got != tc.want {
			t.Errorf("%s: limitWait = %s, want %s", name, got, tc.want)
		}
	}
}

func TestIsRateLimit(t *testing.T) {
	limit := fmt.Errorf("step: %w", &apperr.ProviderError{Kind: apperr.FailureRateLimit})
	if !isRateLimit(limit, time.Minute) {
		t.Error("a wrapped usage-limit failure must wait")
	}
	if isRateLimit(limit, 0) {
		t.Error("execution.rate_limit_wait: 0 must not wait")
	}
	if isRateLimit(&apperr.ProviderError{Kind: apperr.FailureExit}, time.Minute) {
		t.Error("another provider failure must not wait")
	}
}
