package logging

import (
	"fmt"
	"strings"

	"orchestrator/internal/domain"
)

// ResumeHeader opens `orchestrator resume`: the run, its directory, and the state the
// previous attempt left it in. A run found "running" is one whose process died.
func (r *ConsoleReporter) ResumeHeader(run *domain.Run, dir string) {
	fmt.Fprintf(r.Out, "Resuming run %s  (%s)\n", run.ID, dir)
	status := string(run.Status)
	if run.Status == domain.RunRunning {
		status = "interrupted"
	}
	if run.FailureReason != "" {
		fmt.Fprintf(r.Out, "Previous attempt: %s — %s\n", status, firstLine(run.FailureReason))
	} else {
		fmt.Fprintf(r.Out, "Previous attempt: %s\n", status)
	}
}

// NextStep names where a resume continues, why, and the workflow from there on as it
// would run forward; retries are not predicted.
func (r *ConsoleReporter) NextStep(entry domain.ActionType, why string, remaining []domain.ActionType) {
	fmt.Fprintf(r.Out, "Next: %s — %s\n", entry, why)
	names := make([]string, len(remaining))
	for i, a := range remaining {
		names[i] = string(a)
	}
	fmt.Fprintf(r.Out, "Remaining workflow: %s\n", strings.Join(names, " → "))
}

// AlreadyCompleted reports a resume of a run that has nothing left to do.
func (r *ConsoleReporter) AlreadyCompleted(runID string) {
	fmt.Fprintf(r.Out, "Run %s already completed. Nothing to resume.\n", runID)
}
