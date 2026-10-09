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
	in := ImplementPromptInput{
		Task:                   run.Task.Description,
		Analysis:               d.lastAnalysis,
		Plan:                   d.lastPlan,
		PreviousImplementation: d.lastImpl,
		LastTestResult:         d.pendingTestFailure,
		PreRunFailure:          d.preRunFailureForImplement(),
		ReviewIssues:           d.pendingReviewIssues,
		UnmetCriteria:          d.pendingUnmetCriteria,
		MaxTestOutput:          d.Config.Limits.Prompt.TestOutputBytes(),
	}

	// Fail closed: without a guard nothing would stop a write that turns into execution
	// (a hook, CI, .git/config) from reaching the test step.
	if d.Guard == nil {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "implement requires a guard over the repository, and none is configured"}
	}
	before, err := d.Guard.Snapshot(ctx)
	if err != nil {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "failed to snapshot protected paths before implement", Err: err}
	}
	// The working tree as a git tree, to count what the step changes. Best effort: without
	// it the step records no diff.
	treeBefore, _ := d.Repo.WorktreeTree(ctx)

	// A retry also sees what the run has changed so far. Not fatal: without the stat the
	// prompt is the one it was before.
	if d.lastImpl != nil && run.PreRunRef != "" {
		const noStat = "could not compute the run's diff stat for the retry prompt"
		if treeBefore == "" {
			d.warn(domain.ActionImplement, noStat+": the working tree could not be read")
		} else if files, err := d.Repo.DiffNumstat(ctx, run.PreRunRef, treeBefore, d.runsExclude()); err != nil {
			d.warn(domain.ActionImplement, noStat+": "+err.Error())
		} else {
			in.RunDiffStat = renderDiffStat(files, d.Config.Limits.Prompt.DiffBytes())
		}
	}
	prompt := BuildImplementPrompt(in)
	if TestOutputCut(in) {
		d.stepTruncated = append(d.stepTruncated, domain.PromptTestOutput)
	}

	result, ref, err := d.call(ctx, run, d.Config.Actions.Implement.ProviderModel, domain.ActionImplement, prompt)
	if err != nil {
		return d.failureOutcome(ref, result), err
	}

	artifact := parseImplementation(result.Output)
	d.applyImplementation(artifact)

	// Checked before the outcome is accepted, so a violation stops the run before test
	// executes anything the call wrote.
	changed, err := d.Guard.Changed(ctx, before)

	// Saved only after the guard has looked: nothing may be written to the run directory
	// while the guard is open, since the run directory's resume inputs are among the
	// paths it protects (specs/resume/SPEC.md §9.3).
	path := d.saveArtifact(run, domain.ActionImplement, artifact)
	outcome := workflow.StepOutcome{
		Outcome:      workflow.OutcomeSuccess,
		Provider:     &ref,
		ArtifactPath: path,
		Artifact:     artifact,
		RawStdout:    result.Output,
	}
	if err != nil {
		return outcome, &apperr.WorkflowError{Msg: "failed to check protected paths after implement", Err: err}
	}
	if len(changed) > 0 {
		return outcome, &apperr.WorkflowError{Msg: protectedViolation(domain.ActionImplement, changed, run.PreRunRef)}
	}
	// Only now, with the guard passed, may git look at what the call wrote.
	if treeBefore != "" {
		outcome.Diff, outcome.TreeAfter = d.stepDiff(ctx, run, treeBefore, artifact)
		if outcome.TreeAfter != "" {
			outcome.TreeBefore = treeBefore
		}
	}
	if err := d.scanSecrets(ctx, run, domain.ActionImplement); err != nil {
		return outcome, err
	}
	return outcome, nil
}

// protectedViolation explains why the run stopped. The changes stay on disk for
// inspection.
func protectedViolation(step domain.ActionType, changed []string, restoreRef string) string {
	return violationMessage(fmt.Sprintf("%s changed protected paths; the run stops before anything executes them:\n", step), changed, restoreRef)
}

func violationMessage(heading string, changed []string, restoreRef string) string {
	var b strings.Builder
	b.WriteString(heading)
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

// preRunFailureForImplement is the red pre-run test implement should see: only until the
// first test step has run, whose result then speaks for itself.
func (d *Dispatcher) preRunFailureForImplement() *domain.TestResult {
	if d.lastTest != nil {
		return nil
	}
	return d.preRunFailure
}

// LastImplementation is the artifact of the last implement step, restored from its
// artifact on resume, or nil when none ran. The commit offered at the end of a run takes
// its message from it (MVP.md §42).
func (d *Dispatcher) LastImplementation() *domain.ImplementationArtifact {
	return d.lastImpl
}
