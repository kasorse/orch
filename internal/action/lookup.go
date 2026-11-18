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

// The lookup call of the listener's plan: before the requirements' questions are put to people, a
// read-only step looks for their answers in the code of the Epic's repositories, the references and
// the targets. What the code settles becomes an assumption written on the Epic; the rest is asked.

// LookupRepo is one repository the lookup may read: its directory's name under the working directory.
type LookupRepo struct {
	Name string
	// Reference is a repository the task reads but does not change.
	Reference bool
}

// LookupInput is what the lookup call is asked from.
type LookupInput struct {
	// Request is the Epic's rendered text, with its discussion.
	Request string
	// Summary is what the requirements call understood; Questions are what it asks, numbered from 1.
	Summary   string
	Questions []string
	// Repos are the checkouts under Dir, by directory name.
	Repos []LookupRepo
	// Dir holds the checkouts; ExtraDeny and RunsDir make its deny list, as a survey's.
	Dir       string
	ExtraDeny []string
	RunsDir   string
}

// LookupResult is what the call gave, as far as it got.
type LookupResult struct {
	Answer story.LookupAnswer
	Prompt string
	Output string
	Usage  *domain.Usage
}

var lookupRules = fmt.Sprintf(`"question" is the number of the question answered. "answer" is what was found, as one or two plain sentences that read `+
	`without the question (not "Yes" or "Only the old service", but "The list is read from the old service's `+
	`list endpoint"), at most %d bytes, written for the people who own the ticket, with no file path in it. "evidence" is where the code shows it (repository/path, a function or a table), at most %d bytes. `+
	`Leave a question out rather than guess; "answers" may be empty. Write in the language of the task.`,
	story.MaxLookupAnswerBytes, story.MaxLookupEvidenceBytes)

// BuildLookupPrompt is the prompt of the lookup call: the repositories, the questions, the rules, the
// task and what was understood of it.
func BuildLookupPrompt(in LookupInput) string {
	var b strings.Builder
	b.WriteString("ACTION: lookup\n\n")
	b.WriteString("The questions below arose while stating the requirements of the software engineering task given after them. " +
		"Before they are put to people, look for their answers in the code. The working directory holds the checkouts of " +
		"these repositories, one directory each; read only these:\n")
	for _, r := range in.Repos {
		role := "the task changes it"
		if r.Reference {
			role = "reference: the task reads it, never changes it"
		}
		b.WriteString("- " + r.Name + " (" + role + ")\n")
	}
	b.WriteString("\nAnswer a question when the code settles it: how an existing service or flow behaves, where data comes " +
		"from or goes to, what is already implemented. A question put as a choice is settled too when the code shows that only " +
		"one option exists or makes sense (say, data that is always published has no draft to choose from): answer with that " +
		"fact. Leave out a question that stays a decision about what to build, its scope or a product choice once the code " +
		"is read: that is for people. ")
	b.WriteString(readOnlyRule + " Respond with a single JSON object, no markdown fences, no prose before or after it, " +
		"matching exactly this shape:\n\n")
	b.WriteString("{\n  \"answers\": [{\"question\": 1, \"answer\": \"...\", \"evidence\": \"...\"}]\n}\n\n")
	b.WriteString(lookupRules + "\n\nQuestions:\n")
	for i, q := range in.Questions {
		fmt.Fprintf(&b, "%d. %s\n", i+1, oneLine(q))
	}
	b.WriteString("\nWhat was understood of the task:\n" + oneLine(in.Summary) + "\n\n")
	b.WriteString("Task:\n" + taskBlock(in.Request))
	return b.String()
}

// Lookup runs the lookup call in Dir with read authority and the deny list, and decodes the answer
// strictly. An answer that holds no JSON object or breaks a rule is an *apperr.InvalidResponseError.
func Lookup(ctx context.Context, call PlanningCall, in LookupInput) (LookupResult, error) {
	res := LookupResult{Prompt: BuildLookupPrompt(in)}
	if in.Dir == "" {
		return res, &apperr.WorkflowError{Msg: "lookup: no working directory"}
	}
	kind := planningKind{action: "lookup", log: "lookup", timeout: "actions.investigate.timeout"}
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
		return res, &apperr.InvalidResponseError{Call: "lookup", Msg: "the answer holds no JSON object"}
	}
	res.Answer, err = story.ParseLookup([]byte(obj), len(in.Questions))
	if err != nil {
		return res, invalidResponse("lookup", "", err)
	}
	return res, nil
}
