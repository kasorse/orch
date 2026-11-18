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

// The brief call of the listener's plan: what a target service is, as far as the task needs it, so
// that the requirements and the tasks are written in the target's terms. It starts from the target's own
// instructions (CLAUDE.md, AGENTS.md, README.md), given in the prompt, and reads the target's code, read
// only, where they do not say what the task needs.

// TargetBrief is a target's brief as the requirements and specify prompts embed it.
type TargetBrief struct {
	Repo   string
	Answer story.BriefAnswer
}

// BriefInput is what a target's brief is asked from.
type BriefInput struct {
	Repo string
	// Instructions is the text of the target's instruction files; "" when it has none.
	Instructions string
	// Request is the Epic's rendered text, with its discussion.
	Request string
	// Dir is the target's checkout; ExtraDeny and RunsDir make its deny list, as a survey's.
	Dir       string
	ExtraDeny []string
	RunsDir   string
}

// BriefResult is what the call gave, as far as it got.
type BriefResult struct {
	Answer story.BriefAnswer
	Prompt string
	Output string
	Usage  *domain.Usage
}

var briefRules = fmt.Sprintf(`"architecture" is how the service is built, in at most %d bytes: its parts, its data model and the concepts `+
	`it is organised around (for example countries, not sites), its main flows, and where the task's data and flows live in it `+
	`(packages, tables, endpoints, screens), so that a later step can tell how many places the change touches. "rules" lists `+
	`at most %d business rules it enforces that bear on the task, each one line of at most %d bytes. Leave out commands, tooling, `+
	`code style, testing and workflow instructions. Write in English.`,
	story.MaxBriefArchitectureBytes, story.MaxBriefRules, story.MaxBriefRuleBytes)

// BuildBriefPrompt is the prompt of the brief of in.Repo: its instructions, the rules and the task.
func BuildBriefPrompt(in BriefInput) string {
	var b strings.Builder
	b.WriteString("ACTION: brief\n\n")
	b.WriteString("The working directory is the checkout of the repository \"" + in.Repo + "\", which the software engineering " +
		"task given below changes. Describe its architecture and its business rules as far as the task needs them, for a later " +
		"step that writes the requirements and the tasks of the change without reading the code. ")
	if in.Instructions != "" {
		b.WriteString("Start from its instruction files (CLAUDE.md, AGENTS.md, README.md), given below; read the code only " +
			"where they do not say what the task needs: the data model, the concepts and the flows the task touches. ")
	} else {
		b.WriteString("It has no instruction file (CLAUDE.md, AGENTS.md, README.md): read the code for the data model, the " +
			"concepts and the flows the task touches. ")
	}
	b.WriteString("Do not design the change. " + readOnlyRule + " Respond with a single JSON object, no markdown fences, " +
		"no prose before or after it, matching exactly this shape:\n\n")
	b.WriteString("{\n  \"architecture\": \"...\",\n  \"rules\": [\"...\"]\n}\n\n")
	b.WriteString(briefRules + "\n\n")
	if in.Instructions != "" {
		b.WriteString(fence("instructions", "the instruction files of the repository "+in.Repo, in.Instructions) + "\n")
	}
	b.WriteString("Task:\n" + taskBlock(in.Request))
	return b.String()
}

// Brief runs the brief call in the target's checkout with read authority and the deny list, and decodes
// the answer strictly.
func Brief(ctx context.Context, call PlanningCall, in BriefInput) (BriefResult, error) {
	res := BriefResult{Prompt: BuildBriefPrompt(in)}
	if in.Dir == "" {
		return res, &apperr.WorkflowError{Msg: "brief: no working directory"}
	}
	kind := planningKind{action: "brief", log: "brief-" + in.Repo, timeout: "actions.investigate.timeout"}
	var err error
	res.Output, res.Usage, err = call.run(ctx, kind, provider.Request{
		Prompt: res.Prompt, WorkingDir: in.Dir, Authority: domain.AuthorityRead,
		DenyRead: denyList(in.ExtraDeny, in.RunsDir),
	})
	if err != nil {
		return res, err
	}
	obj := extractJSONObject(res.Output)
	if obj == "" {
		return res, &apperr.InvalidResponseError{Call: "brief", Repo: in.Repo, Msg: "the answer holds no JSON object"}
	}
	res.Answer, err = story.ParseBrief([]byte(obj))
	if err != nil {
		return res, invalidResponse("brief", in.Repo, err)
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
