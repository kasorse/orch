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

func (d *Dispatcher) executeInvestigate(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	prompt := BuildInvestigatePrompt(InvestigatePromptInput{
		Task:        run.Task.Description,
		Research:    d.lastResearch,
		FinalAnswer: isFinalReadOnlyStep(run, domain.ActionInvestigate),
	})

	pm, source := d.route(run, domain.ActionInvestigate)
	result, ref, err := d.call(ctx, run, pm, source, domain.ActionInvestigate, prompt, provider.Extensions{})
	if err != nil {
		return d.failureOutcome(ref, result), err
	}

	artifact := parseInvestigation(result.Output)
	d.applyInvestigation(artifact)
	path := d.saveArtifact(run, domain.ActionInvestigate, artifact)

	return workflow.StepOutcome{
		Outcome:      workflow.OutcomeSuccess,
		Provider:     &ref,
		ArtifactPath: path,
		Artifact:     artifact,
		RawStdout:    result.Output,
	}, nil
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
