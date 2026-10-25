package action

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
	"orchestrator/internal/verify"
	"orchestrator/internal/workflow"
)

// prepareVerify writes the server's input into dir and returns the extension that starts it.
func (d *Dispatcher) prepareVerify(dir string, run *domain.Run, before GuardState) (provider.Extensions, error) {
	// MkdirTemp creates the directory 0700 already; the server's files are only for us.
	if err := os.Chmod(dir, 0o700); err != nil {
		return provider.Extensions{}, err
	}
	spec, err := d.verifySpec(run, before)
	if err != nil {
		return provider.Extensions{}, err
	}
	if err := verify.WriteSpec(dir, spec); err != nil {
		return provider.Extensions{}, err
	}
	exe, err := d.executable()
	if err != nil {
		return provider.Extensions{}, err
	}
	return verifyExtensions(dir, exe, len(spec.Checks) > 0), nil
}

// executeImplement always reports OutcomeSuccess if the provider process exits 0 -
// whether the implementation is actually correct is verified by the next test/review
// step, not by implement itself (MVP.md §31.4).
func (d *Dispatcher) executeImplement(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	in := ImplementPromptInput{
		Task:                   run.Task.Description,
		Clarification:          run.Task.Clarification.Render(),
		Plan:                   d.lastInvestigation,
		PreviousImplementation: d.lastImpl,
		LastTestResult:         d.pendingTestFailure,
		PreRunFailure:          d.preRunFailureForImplement(),
		PreRunTest:             d.preRunFailure,
		ReviewIssues:           d.pendingReviewIssues,
		UnmetCriteria:          d.pendingUnmetCriteria,
		MediumIssues:           d.pendingMediumIssues,
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

	// With verify on, the session gets the orch MCP server over a private directory that
	// holds its input and, afterwards, its record of the calls. The directory is removed on
	// every way out of this function.
	var builtin provider.Extensions
	verifyDir := ""
	if d.Config.Actions.Implement.Verify {
		dir, err := os.MkdirTemp("", "orch-verify-")
		if err != nil {
			return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "failed to create the verify directory", Err: err}
		}
		defer os.RemoveAll(dir)
		verifyDir = dir
		if builtin, err = d.prepareVerify(dir, run, before); err != nil {
			return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "failed to set up the verify tools", Err: err}
		}
		in.Verify, in.VerifyChecks = true, len(d.Config.Checks) > 0
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

	pm, source := d.route(run, domain.ActionImplement)
	result, ref, err := d.call(ctx, run, pm, source, domain.ActionImplement, prompt, builtin)
	// Read on every path: the calls are recorded in a step that failed as well.
	var verified verifyOutput
	if verifyDir != "" {
		verified = readVerifyDir(verifyDir)
	}
	if err != nil {
		failed := d.failureOutcome(ref, result)
		failed.VerifyCalls, failed.VerifyLogs = verified.calls, verified.logs
		return failed, err
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
		VerifyCalls:  verified.calls,
		VerifyLogs:   verified.logs,
	}
	if err != nil {
		return outcome, &apperr.WorkflowError{Msg: "failed to check protected paths after implement", Err: err}
	}
	if len(changed) > 0 {
		return outcome, &apperr.WorkflowError{Msg: protectedViolation(domain.ActionImplement, changed, run.PreRunRef)}
	}
	// A violation the server found may have been undone by the model since, so the guard
	// above can come back clean.
	if err := verifyFailure(verified, run.PreRunRef); err != nil {
		return outcome, err
	}
	// Only now, with the guard passed, may git look at what the call wrote.
	var netFiles []string
	netOK := false
	if treeBefore != "" {
		outcome.Diff, outcome.TreeAfter, netFiles, netOK = d.stepDiff(ctx, run, treeBefore, artifact)
		if outcome.TreeAfter != "" {
			outcome.TreeBefore = treeBefore
		}
	}
	if err := d.scanSecrets(ctx, run, domain.ActionImplement); err != nil {
		return outcome, err
	}
	err = checkPathRules(run, &outcome, netFiles, netOK)
	return outcome, err
}

// checkPathRules matches the rules the run started with against the files of its net diff and
// records the matches on outcome (specs/governance/SPEC.md §4.2). A match that needs a stage
// the workflow lacks fails the step. Rules that cannot be checked because git gave no file
// list fail it too: a rule that silently does not apply is the wrong answer.
func checkPathRules(run *domain.Run, outcome *workflow.StepOutcome, files []string, filesOK bool) error {
	rules := run.Options.PathRules
	if len(rules) == 0 {
		return nil
	}
	if !filesOK {
		return &apperr.WorkflowError{Msg: "policy.paths cannot be checked: git could not list the changed files"}
	}
	var wf domain.Workflow
	if run.Workflow != nil {
		wf = *run.Workflow
	}
	outcome.PathRules = domain.MatchPathRules(rules, files, wf)
	for _, m := range outcome.PathRules {
		if len(m.Missing) > 0 {
			return &apperr.WorkflowError{Msg: domain.PathRuleFailure(m)}
		}
	}
	return nil
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

// parseImplementation is lenient, matching parseInvestigation - see its comment. An object that
// does not decode (an unescaped quote in a model's summary) still gives the fields written
// after the break, so summary_short, the commit message (MVP.md §42.2), is not lost.
func parseImplementation(output string) domain.ImplementationArtifact {
	var artifact domain.ImplementationArtifact
	if obj := extractJSONObject(output); obj != "" {
		if err := json.Unmarshal([]byte(obj), &artifact); err == nil {
			return artifact
		}
	}
	artifact = domain.ImplementationArtifact{Summary: output}
	recoverField(output, "summary_short", &artifact.SummaryShort)
	recoverField(output, "changed_files", &artifact.ChangedFiles)
	recoverField(output, "tests_added", &artifact.TestsAdded)
	return artifact
}

// preRunFailureForImplement is the red pre-run test implement should see: only until the
// first test step has run, whose result then speaks for itself.
func (d *Dispatcher) preRunFailureForImplement() *domain.TestResult {
	if d.lastTest != nil {
		return nil
	}
	return d.preRunFailure
}

// CommitImplementation is the implementation the commit offered at the end of a run takes
// its message from (MVP.md §42): the first one with a summary_short, since a fix
// iteration's describes only the fix, else the last one; restored from the artifacts on
// resume, or nil when none ran.
func (d *Dispatcher) CommitImplementation() *domain.ImplementationArtifact {
	if d.commitImpl != nil {
		return d.commitImpl
	}
	return d.lastImpl
}
