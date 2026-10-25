package domain

import (
	"math"
	"strings"
	"testing"
)

func TestDecisionUsage_ClarifyCallsAreCounted(t *testing.T) {
	u := DecisionUsage{
		Translation:          &Usage{InputTokens: 1},
		Classification:       &Usage{InputTokens: 3},
		TranslationClarified: &Usage{InputTokens: 4},
		Clarify:              []Usage{{InputTokens: 5}},
		ClarityRecheck:       []Usage{{InputTokens: 7}, {InputTokens: 9}},
	}
	if (DecisionUsage{Clarify: []Usage{{}}}).Empty() || (DecisionUsage{TranslationClarified: &Usage{}}).Empty() {
		t.Error("the clarify call's and the second translation's usage make a DecisionUsage non-empty")
	}
	var sum int64
	for _, c := range u.Calls() {
		sum += int64(c.InputTokens)
	}
	// The second translation is a call of its own.
	if got := len(u.Calls()); got != 6 || sum != 29 {
		t.Errorf("Calls() = %d calls, %d input tokens; want 6 and 29", got, sum)
	}
	if got := u.ClarifyCalls(); len(got) != 3 {
		t.Errorf("ClarifyCalls() = %v, want the clarify call and the two re-checks", got)
	}
}

// A manual decision (run --stage) is charged for the clarity checks and the clarify step only.
func TestComputeOutcome_CountsTheClarifyStepOfAManualDecision(t *testing.T) {
	trace := &DecisionTrace{
		Source: DecisionSourceManual,
		Usage: &DecisionUsage{
			Clarify:        []Usage{{InputTokens: 5, CostUSD: 0.05}},
			ClarityRecheck: []Usage{{InputTokens: 1, CostUSD: 0.01}, {InputTokens: 1, CostUSD: 0.01}},
		},
	}
	out := ComputeOutcome(Run{Status: RunCompleted}, trace)
	if d := out.DecisionOverhead; d.Calls != 3 || math.Abs(d.CostUSD-0.07) > 1e-9 {
		t.Errorf("decision overhead = %+v, want 3 calls costing 0.07", d)
	}
}

func TestClarification_NeedsInputText(t *testing.T) {
	c := &Clarification{
		Threshold: 0.8, Checks: []float64{0.42, 0.55}, Status: ClarityUnclear,
		Rounds: []ClarifyRound{{
			Kind: ClarifyRoundClarify, Mode: ClarifyAuto,
			Points: []ClarifyPoint{
				{Unclear: "format", Question: "Which format?", Answer: "CSV", By: ClarifyByDocs},
				{Unclear: "locale", Question: "Which   locale is the date in?"},
			},
		}},
	}
	text := c.NeedsInputText()
	for _, want := range []string{"P(clear) 0.55 < 0.80", "Open points:", "  - Which locale is the date in?"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Which format?") {
		t.Errorf("an answered point is not open:\n%s", text)
	}

	c.Rounds[0].Points = c.Rounds[0].Points[:1]
	if text := c.NeedsInputText(); !strings.Contains(text, "No point is left open") {
		t.Errorf("without open points the text must still say why:\n%s", text)
	}
}
