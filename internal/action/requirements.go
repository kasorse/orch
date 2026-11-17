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

// The requirements call of the listener's plan (specs/listener-requirements/SPEC.md §2): what an
// Epic asks for, understood from its text and its references' surveys before it is split. Like
// select and specify it is outside every workflow, with no tools; Go validates what it answers
// (story.ParseRequirements).

// RequirementsInput is what the requirements call is asked from.
type RequirementsInput struct {
	// Request is the Epic's rendered text, with its discussion.
	Request string
	// References are the surveys of the reference repositories; none is allowed.
	References []SurveyInput
}

// RequirementsResult is what the call gave, as far as it got: the prompt, the raw answer (also
// when it is refused, for the plan's artifacts) and the usage (nil when the CLI reported none).
type RequirementsResult struct {
	Answer story.RequirementsAnswer
	Prompt string
	Output string
	Usage  *domain.Usage
}

// requirementsRules state the bounds story.ParseRequirements enforces.
var requirementsRules = fmt.Sprintf(`"summary" is what the task asks for, in at most %d bytes. `+
	`"requirements" lists from 1 to %d requirements, each one statement of at most %d bytes. `+
	`"questions" lists every question, each of at most %d bytes; leave it empty when nothing is left unclear. `+
	`The summary and the questions are posted as they are in a comment on the ticket, for the people who own it, and are read by nobody else: `+
	`write them as a colleague would, short and in plain words. The summary is two to four sentences on what is built and where, `+
	`with no list, no field or code names and no detail the requirements already hold. Each question asks one decision `+
	`in one or two sentences, naming the options briefly when there are some. `+
	`The requirements are the detail: they are not shown on the ticket but go to the developers. `+
	`The summary, the requirements and the questions are written in the language of the task.`,
	story.MaxRequirementsSummaryBytes, story.MaxRequirements, story.MaxRequirementBytes, story.MaxQuestionBytes)

// BuildRequirementsPrompt is the prompt of the requirements call: what to state, the shape of the
// answer, the task, and the references' surveys, each in its own fence, in the order of the
// repositories' names.
func BuildRequirementsPrompt(in RequirementsInput) string {
	var b strings.Builder
	b.WriteString("ACTION: requirements\n\n")
	b.WriteString("Understand the following software engineering task and state its requirements. You have no tools: what you " +
		"know is the task and, when there are any, the surveys of the reference repositories, given after it. " +
		"State what must be built - data, dictionaries and where their values come from, rules, behaviour per country, UI - " +
		"as requirements a developer can implement without reading the references. What the references show is behaviour " +
		"to reproduce: name it as such. " +
		"Ask a question only about what neither the task nor the references settle and that changes what is built; " +
		"ask nothing about code layout, naming or tests, which the runs that implement the task decide. " +
		"Respond with a single JSON object, no markdown fences, no prose before or after it, matching exactly this shape:\n\n")
	b.WriteString("{\n  \"summary\": \"...\",\n  \"requirements\": [\"...\"],\n  \"questions\": [\"...\"]\n}\n\n")
	b.WriteString(requirementsRules + "\n\n")
	b.WriteString("Task:\n" + taskBlock(in.Request))
	writeReferenceSurveys(&b, in.References)
	return b.String()
}

// Requirements runs the requirements call with no tools, no working directory and no deny list
// (there is nothing it could read), and decodes the answer strictly. An answer that holds no JSON
// object or breaks a rule is an *apperr.InvalidResponseError naming the field; the raw answer is
// in the result either way.
func Requirements(ctx context.Context, call PlanningCall, in RequirementsInput) (RequirementsResult, error) {
	res := RequirementsResult{Prompt: BuildRequirementsPrompt(in)}
	kind := planningKind{action: "requirements", log: "requirements", timeout: "actions.investigate.timeout"}
	var err error
	res.Output, res.Usage, err = call.run(ctx, kind, provider.Request{Prompt: res.Prompt, Authority: domain.AuthorityNone})
	if err != nil {
		return res, err
	}
	obj := extractJSONObject(res.Output)
	if obj == "" {
		return res, &apperr.InvalidResponseError{Call: "requirements", Msg: "the answer holds no JSON object"}
	}
	res.Answer, err = story.ParseRequirements([]byte(obj))
	if err != nil {
		return res, invalidResponse("requirements", "", err)
	}
	return res, nil
}
