package report

import (
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

// A run that stopped with "task needs input" lists the open points and the last P(clear) against
// the threshold in summary.md; a run that was never unclear has no such section.
func TestRunSummary_ListsWhatANeedsInputRunStillLacks(t *testing.T) {
	run := domain.Run{ID: "run-1", Status: domain.RunStopped, FailureReason: domain.FailureTaskNeedsInput}
	run.Task.Description = "add an export"
	run.Task.Clarification = &domain.Clarification{
		Threshold: 0.8, Checks: []float64{0.42, 0.55}, Status: domain.ClarityUnclear,
		Rounds: []domain.ClarifyRound{{
			Kind: domain.ClarifyRoundClarify, Mode: domain.ClarifyAuto,
			Points: []domain.ClarifyPoint{
				{Unclear: "format", Question: "Which format?", Answer: "CSV", By: domain.ClarifyByDocs, Source: "docs/export.md#Formats"},
				{Unclear: "locale", Question: "Which locale is the date in?"},
			},
		}},
	}

	got := RunSummary(run, nil)
	for _, want := range []string{
		"## Clarification",
		"The task was unclear: P(clear) 0.42 → 0.55 against the threshold 0.80.",
		"needs input",
		"> - Which format? CSV  [from docs/export.md#Formats]",
		"> - Open: Which locale is the date in?",
		"| Status | stopped: task needs input |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q:\n%s", want, got)
		}
	}

	run.Task.Clarification = nil
	if got := RunSummary(run, nil); strings.Contains(got, "## Clarification") {
		t.Errorf("a run without a clarification has no such section:\n%s", got)
	}
}
