package decision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"orchestrator/internal/domain"
)

// The questions every decision provider answers (TASKS.md §2). They are shared so
// that answers from different providers stay comparable (T22), and each adapter only
// renders them in its own format. The model reads them literally: any edit to a text
// here is a reason to recalibrate the policy thresholds.

// Question IDs. `request` in the texts below refers to the state field of that name.
const (
	QuestionContextDepth    = "context_depth"
	QuestionComplexity      = "complexity"
	QuestionComplexityScore = "complexity_score"
	QuestionScopeStory      = "scope_story"
	stageQuestionPrefix     = "stage_"
)

// StageQuestionID is the ID of the yes/no question for stage.
func StageQuestionID(stage domain.Stage) string { return stageQuestionPrefix + string(stage) }

// Option is one answer of a choice question.
type Option struct {
	Value       string
	Description string
}

// ChoiceQuestion asks for a probability per option.
type ChoiceQuestion struct {
	ID           string
	Instructions string
	Options      []Option
}

// ScoreQuestion asks for a probability per ordered level. Level i is
// domain.ComplexityOrder()[i].
type ScoreQuestion struct {
	ID           string
	Instructions string
	Levels       []string
}

// YesNoQuestion asks for the probability that the answer is yes.
type YesNoQuestion struct {
	ID           string
	Stage        domain.Stage
	Instructions string
	True         string
	False        string
}

// ContextDepthQuestion is the pre-check.
func ContextDepthQuestion() ChoiceQuestion {
	return ChoiceQuestion{
		ID:           QuestionContextDepth,
		Instructions: "To estimate the complexity and required workflow stages of `request`, is it necessary to inspect the current code repository?",
		Options: []Option{
			{string(domain.ContextDepthNone), "The request can be classified from its text alone: general knowledge, concepts, comparisons; it does not refer to this repository's code, files or behaviour."},
			{string(domain.ContextDepthProject), "The request refers to code, components, files, bugs, tests or behaviour of the current repository, so its complexity depends on that code."},
		},
	}
}

// complexityCriteria is the single source of the complexity criteria (MVP.md §14), in
// domain.ComplexityOrder. Both complexity questions are built from it so their texts
// cannot drift apart - otherwise comparing choice and score (TASKS.md В1) is moot.
var complexityCriteria = []Option{
	{string(domain.ComplexitySimple), "Small local change, few components, known implementation path, no architecture change, low regression risk."},
	{string(domain.ComplexityMedium), "Several files/components, requires understanding existing code, introduces a dependency or behaviour, moderate regression risk."},
	{string(domain.ComplexityComplex), "Cross-component or architectural change, uncertain strategy, significant data-flow change, high regression risk."},
}

// ComplexityChoiceQuestion asks for a distribution over simple/medium/complex.
func ComplexityChoiceQuestion() ChoiceQuestion {
	return ChoiceQuestion{
		ID:           QuestionComplexity,
		Instructions: "How complex is it to fulfil `request` in this project?",
		Options:      append([]Option(nil), complexityCriteria...),
	}
}

// ComplexityScoreQuestion asks for the same criteria as ordered levels, rendered as
// "Simple: small local change, ...".
func ComplexityScoreQuestion() ScoreQuestion {
	levels := make([]string, len(complexityCriteria))
	for i, c := range complexityCriteria {
		levels[i] = capitalize(c.Value) + ": " + lowerFirst(c.Description)
	}
	return ScoreQuestion{
		ID:           QuestionComplexityScore,
		Instructions: "Rate how complex it is to fulfil `request` in this project.",
		Levels:       levels,
	}
}

var stageQuestions = map[domain.Stage]YesNoQuestion{
	domain.StageResearch: {
		Instructions: "Does fulfilling `request` require researching external knowledge (approaches, libraries, alternatives) before working?",
		True:         "The request needs information that is not in the repository: comparing approaches, choosing or evaluating libraries or tools, or looking up external standards or APIs.",
		False:        "Everything needed is in the request, the repository, or common engineering knowledge.",
	},
	domain.StageDevelopment: {
		Instructions: "Does fulfilling `request` require changing code in the repository?",
		True:         "Files in the repository must be created, modified or deleted.",
		False:        "The request is answered or fulfilled without modifying repository files.",
	},
}

// StageQuestions returns one yes/no question per asked stage (domain.AskedStages), in stage
// order. Planning, testing and review are not asked: their answers repeated the task's size
// (specs/decision-shape/SPEC.md §1.1).
func StageQuestions() []YesNoQuestion {
	order := domain.AskedStages()
	out := make([]YesNoQuestion, 0, len(order))
	for _, st := range order {
		q := stageQuestions[st]
		q.ID = StageQuestionID(st)
		q.Stage = st
		out = append(out, q)
	}
	return out
}

// ScopeQuestion is the shadow question of specs/story/SPEC.md §13: how story-like the task
// looks. Its answer is recorded and decides nothing. It is not a stage question: Stage is
// empty, and StageQuestions does not return it.
func ScopeQuestion() YesNoQuestion {
	return YesNoQuestion{
		ID:           QuestionScopeStory,
		Instructions: "Does fulfilling `request` need several changes that are each worth reviewing and merging on their own - in different services or repositories, or as steps that each leave the system working?",
		True:         "The work splits into several independent changes, each of which could be reviewed and merged by itself: it touches more than one service or repository, or it is a sequence of steps after each of which the system still works.",
		False:        "The work is one change that is reviewed and merged as a whole, in one service or repository.",
	}
}

// QuestionsHash is the lowercase hex sha256 of every shared question as asked, in a fixed
// order. decision.json records it, and calibration groups by it: an answer to a reworded
// question is on another scale (specs/decision-shape/SPEC.md §5.3). It is one constant for
// every provider-decided run, scope question included even where a run does not ask it
// (a story task, specs/story/SPEC.md §11.2): the stage answers of such a run come from the
// same questions, and the hash names the question set, not what one run skipped.
func QuestionsHash() string {
	data, err := json.Marshal(struct {
		ContextDepth     ChoiceQuestion
		ComplexityChoice ChoiceQuestion
		ComplexityScore  ScoreQuestion
		Stages           []YesNoQuestion
		Scope            YesNoQuestion
	}{ContextDepthQuestion(), ComplexityChoiceQuestion(), ComplexityScoreQuestion(), StageQuestions(), ScopeQuestion()})
	if err != nil {
		panic(err) // plain strings and slices always encode
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
