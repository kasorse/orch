package action

import (
	"context"
	"fmt"
	"strings"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/story"
)

// The brief call of the listener's plan: what a target service's own instructions (CLAUDE.md,
// AGENTS.md, README.md) say of its architecture and its business rules, so that the requirements and
// the tasks are written in the target's terms. Tool-less: the files are given in the prompt; the code is
// not read.

// TargetBrief is a target's brief as the requirements and specify prompts embed it.
type TargetBrief struct {
	Repo   string
	Answer story.BriefAnswer
}

// BriefResult is what the call gave, as far as it got.
type BriefResult struct {
	Answer story.BriefAnswer
	Prompt string
	Output string
	Usage  *domain.Usage
}

var briefRules = fmt.Sprintf(`"architecture" is how the service is built, in at most %d bytes: its parts, its data model and the concepts `+
	`it is organised around (for example countries, not sites), its main flows. "rules" lists at most %d business rules it enforces, `+
	`each one line of at most %d bytes. Leave out commands, tooling, code style, testing and workflow instructions. Write in English.`,
	story.MaxBriefArchitectureBytes, story.MaxBriefRules, story.MaxBriefRuleBytes)

// BuildBriefPrompt is the prompt of the brief of repo from its instruction files' text.
func BuildBriefPrompt(repo, instructions string) string {
	var b strings.Builder
	b.WriteString("ACTION: brief\n\n")
	b.WriteString("Below are the instruction files of the repository \"" + repo + "\" (CLAUDE.md, AGENTS.md, README.md). " +
		"Extract only its architecture and its business rules, for a later step that writes the requirements and the tasks " +
		"of a change to this repository without reading its code. You have no tools. Respond with a single JSON object, " +
		"no markdown fences, no prose before or after it, matching exactly this shape:\n\n")
	b.WriteString("{\n  \"architecture\": \"...\",\n  \"rules\": [\"...\"]\n}\n\n")
	b.WriteString(briefRules + "\n\n")
	b.WriteString(fence("instructions", "the instruction files of the repository "+repo, instructions))
	return b.String()
}

// Brief runs the brief call of repo with no tools and decodes the answer strictly.
func Brief(ctx context.Context, call PlanningCall, repo, instructions string) (BriefResult, error) {
	res := BriefResult{Prompt: BuildBriefPrompt(repo, instructions)}
	kind := planningKind{action: "brief", log: "brief-" + repo, timeout: "actions.investigate.timeout"}
	var err error
	res.Output, res.Usage, err = call.run(ctx, kind, provider.Request{Prompt: res.Prompt, Authority: domain.AuthorityNone})
	if err != nil {
		return res, err
	}
	obj := extractJSONObject(res.Output)
	if obj == "" {
		return res, &apperr.InvalidResponseError{Call: "brief", Repo: repo, Msg: "the answer holds no JSON object"}
	}
	res.Answer, err = story.ParseBrief([]byte(obj))
	if err != nil {
		return res, invalidResponse("brief", repo, err)
	}
	return res, nil
}

// renderBrief is a brief inside its fence.
func renderBrief(a story.BriefAnswer) string {
	var b strings.Builder
	b.WriteString("Architecture:\n" + strings.TrimSpace(a.Architecture) + "\n")
	items := make([]string, 0, len(a.Rules))
	for _, r := range a.Rules {
		items = append(items, oneLine(r))
	}
	writeNumberedList(&b, "Business rules", items)
	return b.String()
}

// briefRule tells the requirements and specify calls what the targets' briefs are for.
const briefRule = "The target repositories are described by briefs of their own instructions, given after the task: their " +
	"architecture and business rules. Write in the targets' terms: a concept of the references that a target does not " +
	"have (for example sites where the target has countries) is not carried over as it is; map it to the target's concept " +
	"and state the mapping, or ask when no mapping is clear."

// writeBriefs writes the targets' briefs, one fence each, in the order given; nothing when there is none.
func writeBriefs(b *strings.Builder, briefs []TargetBrief) {
	if len(briefs) > 0 {
		b.WriteString("\nTarget briefs, one per target repository, from its own instruction files:\n")
	}
	for i, t := range briefs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fence("brief", "the brief of the target repository "+t.Repo, renderBrief(t.Answer)))
	}
}
