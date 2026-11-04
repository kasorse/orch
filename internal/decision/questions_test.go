package decision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

func TestContextDepthQuestion_CoversEveryDepth(t *testing.T) {
	q := ContextDepthQuestion()
	depths := domain.ContextDepths()
	if len(q.Options) != len(depths) {
		t.Fatalf("got %d options, want %d", len(q.Options), len(depths))
	}
	for i, d := range depths {
		if q.Options[i].Value != string(d) || q.Options[i].Description == "" {
			t.Errorf("option %d = %+v, want %q with a description", i, q.Options[i], d)
		}
	}
}

func TestComplexityQuestions_FollowComplexityOrder(t *testing.T) {
	choice := ComplexityChoiceQuestion()
	score := ComplexityScoreQuestion()
	order := domain.ComplexityOrder()
	if len(choice.Options) != len(order) || len(score.Levels) != len(order) {
		t.Fatalf("choice has %d options, score %d levels, want %d", len(choice.Options), len(score.Levels), len(order))
	}
	for i, c := range order {
		if choice.Options[i].Value != string(c) {
			t.Errorf("choice option %d = %q, want %q", i, choice.Options[i].Value, c)
		}
		// Same criterion text in both questions, only the level label is prepended.
		want := strings.ToUpper(string(c)[:1]) + string(c)[1:] + ": " +
			strings.ToLower(choice.Options[i].Description[:1]) + choice.Options[i].Description[1:]
		if score.Levels[i] != want {
			t.Errorf("score level %d = %q, want %q", i, score.Levels[i], want)
		}
	}
}

func TestComplexityChoiceQuestion_ReturnsCopy(t *testing.T) {
	q := ComplexityChoiceQuestion()
	q.Options[0].Description = "mutated"
	if ComplexityChoiceQuestion().Options[0].Description == "mutated" {
		t.Error("callers must not be able to change the shared criteria")
	}
}

func TestStageQuestions_OnePerAskedStageInOrder(t *testing.T) {
	qs := StageQuestions()
	order := domain.AskedStages()
	if len(qs) != len(order) {
		t.Fatalf("got %d stage questions, want %d", len(qs), len(order))
	}
	for i, st := range order {
		q := qs[i]
		if q.Stage != st || q.ID != "stage_"+string(st) {
			t.Errorf("question %d = %s/%s, want stage %s", i, q.ID, q.Stage, st)
		}
		if q.Instructions == "" || q.True == "" || q.False == "" {
			t.Errorf("stage %s question is incomplete: %+v", st, q)
		}
		if !strings.Contains(q.Instructions, "`request`") {
			t.Errorf("stage %s instructions must refer to the `request` state field", st)
		}
	}
}

// analysis is no longer a stage (specs/clarify): research and development are the only questions.
func TestStageQuestions_AskOnlyResearchAndDevelopment(t *testing.T) {
	var got []domain.Stage
	for _, q := range StageQuestions() {
		got = append(got, q.Stage)
	}
	if len(got) != 2 || got[0] != domain.StageResearch || got[1] != domain.StageDevelopment {
		t.Errorf("StageQuestions asks %v, want research and development", got)
	}
	if _, ok := stageQuestions[domain.Stage("analysis")]; ok {
		t.Error("the analysis question is still defined")
	}
}

func TestScopeQuestion_IsNotAStageQuestion(t *testing.T) {
	q := ScopeQuestion()
	if q.ID != "scope_story" || q.Stage != "" {
		t.Errorf("ScopeQuestion = %s/%q, want scope_story with no stage", q.ID, q.Stage)
	}
	if q.Instructions == "" || q.True == "" || q.False == "" {
		t.Errorf("scope question is incomplete: %+v", q)
	}
	if !strings.Contains(q.Instructions, "`request`") {
		t.Error("scope instructions must refer to the `request` state field")
	}
	for _, sq := range StageQuestions() {
		if sq.ID == q.ID {
			t.Errorf("StageQuestions returns the scope question %q", sq.ID)
		}
	}
}

func TestClarityQuestion_IsNotAStageQuestion(t *testing.T) {
	q := ClarityQuestion()
	if q.ID != "clarity" || q.Stage != "" {
		t.Errorf("ClarityQuestion = %s/%q, want clarity with no stage", q.ID, q.Stage)
	}
	if q.Instructions == "" || q.True == "" || q.False == "" {
		t.Errorf("clarity question is incomplete: %+v", q)
	}
	if !strings.Contains(q.Instructions, "`request`") {
		t.Error("clarity instructions must refer to the `request` state field")
	}
	for _, sq := range StageQuestions() {
		if sq.ID == q.ID {
			t.Errorf("StageQuestions returns the clarity question %q", sq.ID)
		}
	}
}

func TestQuestionsHash_CoversTheClarityQuestion(t *testing.T) {
	h := QuestionsHash()
	saved := clarityQuestion
	t.Cleanup(func() { clarityQuestion = saved })
	for name, change := range map[string]func(*YesNoQuestion){
		"instructions": func(q *YesNoQuestion) { q.Instructions += " " },
		"yes":          func(q *YesNoQuestion) { q.True += " " },
		"no":           func(q *YesNoQuestion) { q.False += " " },
	} {
		clarityQuestion = saved
		change(&clarityQuestion)
		if QuestionsHash() == h {
			t.Errorf("changing the clarity question's %s text did not change the hash", name)
		}
	}
}

func TestQuestionsHash_CoversTheScopeQuestion(t *testing.T) {
	// The hash as it was before the scope question joined it: the shared questions alone.
	data, err := json.Marshal(struct {
		ContextDepth     ChoiceQuestion
		ComplexityChoice ChoiceQuestion
		ComplexityScore  ScoreQuestion
		Stages           []YesNoQuestion
	}{ContextDepthQuestion(), ComplexityChoiceQuestion(), ComplexityScoreQuestion(), StageQuestions()})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if QuestionsHash() == hex.EncodeToString(sum[:]) {
		t.Error("the scope question is not part of QuestionsHash")
	}
}

func TestQuestionsHash(t *testing.T) {
	h := QuestionsHash()
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(h) {
		t.Fatalf("QuestionsHash = %q, want 64 lowercase hex characters", h)
	}
	if again := QuestionsHash(); again != h {
		t.Errorf("QuestionsHash is not stable: %q then %q", h, again)
	}
	saved := stageQuestions[domain.StageResearch]
	t.Cleanup(func() { stageQuestions[domain.StageResearch] = saved })
	reworded := saved
	reworded.Instructions += " "
	stageQuestions[domain.StageResearch] = reworded
	if QuestionsHash() == h {
		t.Error("rewording a stage question did not change the hash")
	}
}
