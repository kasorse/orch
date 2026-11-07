package action

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/workflow"
)

func (d *Dispatcher) executeInvestigate(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	in := InvestigatePromptInput{
		Task:          run.Task.Description,
		Clarification: run.Task.Clarification.Render(),
		Research:      d.lastResearch,
		FinalAnswer:   isFinalReadOnlyStep(run, domain.ActionInvestigate),
	}

	// With a gitlab section the session gets fetch_repository from the orch server: a task
	// that links another repository of the allowed groups (the contract it integrates) is
	// investigated with that code, cloned into .orchestrator/deps by Go after its own URL
	// check. A convenience like replace: when it cannot be set up the step warns and runs
	// without it.
	pm, source := d.route(run, domain.ActionInvestigate)
	var builtin provider.Extensions
	verifyDir := ""
	if d.Config.GitLab != nil && d.Guard != nil && d.supportsMCP(pm) {
		dir, err := os.MkdirTemp("", "orch-verify-")
		if err != nil {
			return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "failed to create the verify directory", Err: err}
		}
		defer os.RemoveAll(dir)
		ext, err := d.prepareFetch(ctx, dir)
		if err != nil {
			d.warn(domain.ActionInvestigate, "the fetch_repository tool is not available in this step: "+err.Error())
		} else {
			builtin, verifyDir, in.Fetch = ext, dir, d.Config.GitLab
		}
	}
	prompt := BuildInvestigatePrompt(in)

	result, ref, err := d.call(ctx, run, pm, source, domain.ActionInvestigate, prompt, builtin)
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

	artifact := parseInvestigation(result.Output)
	d.applyInvestigation(artifact)
	path := d.saveArtifact(run, domain.ActionInvestigate, artifact)

	outcome := workflow.StepOutcome{
		Outcome:      workflow.OutcomeSuccess,
		Provider:     &ref,
		ArtifactPath: path,
		Artifact:     artifact,
		RawStdout:    result.Output,
		VerifyCalls:  verified.calls,
		VerifyLogs:   verified.logs,
	}
	// A violation the server found before a clone fails the step like implement's.
	if err := verifyFailure(verified, run.PreRunRef); err != nil {
		return outcome, err
	}
	return outcome, nil
}

// prepareFetch snapshots the guard and writes the fetch-only spec into dir.
func (d *Dispatcher) prepareFetch(ctx context.Context, dir string) (provider.Extensions, error) {
	before, err := d.Guard.Snapshot(ctx)
	if err != nil {
		return provider.Extensions{}, fmt.Errorf("failed to snapshot protected paths: %w", err)
	}
	spec, err := d.fetchSpec(before)
	if err != nil {
		return provider.Extensions{}, err
	}
	return d.prepareServer(dir, spec)
}

// The most acceptance criteria an investigation keeps, and the longest one in bytes
// (specs/quality/SPEC.md §4.2).
const (
	maxAcceptanceCriteria = 8
	maxCriterionBytes     = 300
)

// parseInvestigation is lenient: correctness of the plan itself is judged by the downstream
// test/review steps, not by strict schema validation here, so a malformed or prose response is
// not a hard failure - it falls back to the raw text as the summary. The criteria are capped
// here, once, so the artifact a resume reads back holds what the prompts used.
func parseInvestigation(output string) domain.InvestigationArtifact {
	var artifact domain.InvestigationArtifact
	if obj := extractJSONObject(output); obj != "" {
		if err := json.Unmarshal([]byte(obj), &artifact); err == nil {
			artifact.AcceptanceCriteria = capCriteria(artifact.AcceptanceCriteria)
			return artifact
		}
	}
	return domain.InvestigationArtifact{Summary: output}
}

// capCriteria keeps the first maxAcceptanceCriteria non-blank entries, trimmed and cut to
// maxCriterionBytes without splitting a character. Nothing left gives nil.
func capCriteria(in []string) []string {
	var out []string
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		out = append(out, cutRunes(c, maxCriterionBytes))
		if len(out) == maxAcceptanceCriteria {
			break
		}
	}
	return out
}

// cutRunes returns s cut to at most n bytes, on a rune boundary.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
