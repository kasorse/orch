package action

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
	"orchestrator/internal/workflow"
)

func (d *Dispatcher) executePlan(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	prompt := BuildPlanPrompt(PlanPromptInput{
		Task:        run.Task.Description,
		Research:    d.lastResearch,
		Analysis:    d.lastAnalysis,
		FinalAnswer: isFinalReadOnlyStep(run, domain.ActionPlan),
	})

	result, ref, err := d.call(ctx, run, d.Config.Actions.Plan.ProviderModel, domain.ActionPlan, prompt, provider.Extensions{})
	if err != nil {
		return d.failureOutcome(ref, result), err
	}

	artifact := parsePlan(result.Output)
	d.applyPlan(artifact)
	path := d.saveArtifact(run, domain.ActionPlan, artifact)

	return workflow.StepOutcome{
		Outcome:      workflow.OutcomeSuccess,
		Provider:     &ref,
		ArtifactPath: path,
		Artifact:     artifact,
		RawStdout:    result.Output,
	}, nil
}

// The most acceptance criteria a plan keeps, and the longest one in bytes
// (specs/quality/SPEC.md §4.2).
const (
	maxAcceptanceCriteria = 8
	maxCriterionBytes     = 300
)

// parsePlan is lenient, matching parseAnalysis - see its comment. The criteria are capped
// here, once, so the artifact a resume reads back holds what the prompts used.
func parsePlan(output string) domain.PlanArtifact {
	var artifact domain.PlanArtifact
	if obj := extractJSONObject(output); obj != "" {
		if err := json.Unmarshal([]byte(obj), &artifact); err == nil {
			artifact.AcceptanceCriteria = capCriteria(artifact.AcceptanceCriteria)
			return artifact
		}
	}
	return domain.PlanArtifact{Summary: output}
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
