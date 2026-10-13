package decision

import (
	"regexp"
	"strings"
	"testing"

	"orchestrator/internal/domain"
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

func TestStageQuestions_OnePerStageInOrder(t *testing.T) {
	qs := StageQuestions()
	order := domain.StageOrder()
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

func TestQuestionsHash(t *testing.T) {
	h := QuestionsHash()
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(h) {
		t.Fatalf("QuestionsHash = %q, want 64 lowercase hex characters", h)
	}
	if again := QuestionsHash(); again != h {
		t.Errorf("QuestionsHash is not stable: %q then %q", h, again)
	}
	saved := stageQuestions[domain.StageReview]
	t.Cleanup(func() { stageQuestions[domain.StageReview] = saved })
	reworded := saved
	reworded.Instructions += " "
	stageQuestions[domain.StageReview] = reworded
	if QuestionsHash() == h {
		t.Error("rewording a stage question did not change the hash")
	}
}
