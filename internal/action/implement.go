package action

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// executeImplement always reports OutcomeSuccess if the provider process exits 0 -
// whether the implementation is actually correct is verified by the next test/review
// step, not by implement itself (MVP.md §31.4).
func (d *Dispatcher) executeImplement(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	prompt := BuildImplementPrompt(ImplementPromptInput{
		Task:                   run.Task.Description,
		Analysis:               d.lastAnalysis,
		Plan:                   d.lastPlan,
		PreviousImplementation: d.lastImpl,
		LastTestResult:         d.pendingTestFailure,
		ReviewIssues:           d.pendingReviewIssues,
	})

	// Fail closed: without a guard nothing would stop a write that turns into execution
	// (a hook, CI, .git/config) from reaching the test step.
	if d.Guard == nil {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "implement requires a guard over the repository, and none is configured"}
	}
	before, err := d.Guard.Snapshot(ctx)
	if err != nil {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "failed to snapshot protected paths before implement", Err: err}
	}

	result, ref, err := d.call(ctx, run, d.Config.Actions.Implement.ProviderModel, domain.ActionImplement, prompt)
	if err != nil {
		return d.failureOutcome(ref, result), err
	}

	artifact := parseImplementation(result.Output)
	d.lastImpl = &artifact
	// Consumed - do not resend stale retry context to a later, unrelated implement call.
	d.pendingTestFailure = nil
	d.pendingReviewIssues = nil

	path := d.saveArtifact(run, domain.ActionImplement, artifact)
	outcome := workflow.StepOutcome{
		Outcome:      workflow.OutcomeSuccess,
		Provider:     &ref,
		ArtifactPath: path,
		RawStdout:    result.Output,
	}

	// Checked before the outcome is accepted, so a violation stops the run before test
	// executes anything the call wrote.
	changed, err := d.Guard.Changed(ctx, before)
	if err != nil {
		return outcome, &apperr.WorkflowError{Msg: "failed to check protected paths after implement", Err: err}
	}
	if len(changed) > 0 {
		return outcome, &apperr.WorkflowError{Msg: protectedViolation(domain.ActionImplement, changed, run.PreRunRef)}
	}
	return outcome, nil
}

// protectedViolation explains why the run stopped. The changes stay on disk for
// inspection.
func protectedViolation(step domain.ActionType, changed []string, restoreRef string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s changed protected paths; the run stops before anything executes them:\n", step)
	for _, p := range changed {
		fmt.Fprintf(&b, "  %s\n", p)
	}
	if slices.Contains(changed, gitViolation) {
		b.WriteString("\nDo not run git in this repository until you have inspected .git/config and the hooks " +
			"directory by hand: git status itself would run a planted core.fsmonitor.\n")
	}
	b.WriteString("\nThe changes are left in the working tree for inspection.")
	if restoreRef != "" {
		fmt.Fprintf(&b, " Restore point: %s", restoreRef)
	}
	b.WriteString("\nThese files make code run later (hooks, CI, agent settings, the orchestrator config); " +
		"if the change is intended, make it yourself or remove the path from security.protected_paths.")
	return b.String()
}

// parseImplementation is lenient, matching parseAnalysis - see its comment.
func parseImplementation(output string) domain.ImplementationArtifact {
	var artifact domain.ImplementationArtifact
	if obj := extractJSONObject(output); obj != "" {
		if err := json.Unmarshal([]byte(obj), &artifact); err == nil {
			return artifact
		}
	}
	return domain.ImplementationArtifact{Summary: output}
}
