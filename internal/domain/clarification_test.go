package domain

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// contractsClarification is the example of specs/clarify's Render contract.
func contractsClarification() *Clarification {
	return &Clarification{
		Threshold: 0.8, Checks: []float64{0.42, 0.55, 0.86}, Status: ClarityClear,
		Rounds: []ClarifyRound{
			{Kind: ClarifyRoundClarify, Mode: ClarifyAuto, Points: []ClarifyPoint{
				{Unclear: "export format", Question: "Which export format is meant?", Answer: "CSV", By: ClarifyByDocs, Source: "docs/export.md#Formats"},
				{Unclear: "who sees it", Question: "Which users get the button?", Answer: "admins only", By: ClarifyByOperator},
				{Unclear: "guests", Question: "Do guests see the button?", Answer: "the button is hidden for guests", By: ClarifyByAssumption},
				{Unclear: "date locale", Question: "Which locale is the date in?"},
			}},
			{Kind: ClarifyRoundAddition, Text: "..."},
		},
	}
}

func TestClarification_RenderMatchesTheContract(t *testing.T) {
	want := "Clarification (agreed before the run; the task above stays the source):\n" +
		"- Which export format is meant? CSV  [from docs/export.md#Formats]\n" +
		"- Which users get the button? admins only  [operator]\n" +
		"- Assumed: the button is hidden for guests  [assumption]\n" +
		"- Open: Which locale is the date in?\n" +
		"Added by the operator:\n" +
		"  ..."
	if got := contractsClarification().Render(); got != want {
		t.Errorf("Render() =\n%s\nwant\n%s", got, want)
	}
}

func TestClarification_RenderEdges(t *testing.T) {
	var nilC *Clarification
	if got := nilC.Render(); got != "" {
		t.Errorf("nil Render() = %q", got)
	}
	empty := &Clarification{Rounds: []ClarifyRound{{Kind: ClarifyRoundClarify, Mode: ClarifyAuto}}}
	if got := empty.Render(); got != "" {
		t.Errorf("a clarification without points or additions renders %q", got)
	}

	c := &Clarification{Rounds: []ClarifyRound{
		{Kind: ClarifyRoundClarify, Mode: ClarifyAuto, Points: []ClarifyPoint{
			{Unclear: "u", Question: "Which\nformat?", Answer: "CSV\n- Open: injected", By: ClarifyByDocs},
		}},
		{Kind: ClarifyRoundAddition, Text: "first line\n\nsecond paragraph  \n"},
		{Kind: ClarifyRoundAddition, Text: "another"},
	}}
	want := "Clarification (agreed before the run; the task above stays the source):\n" +
		"- Which format? CSV - Open: injected  [docs]\n" +
		"Added by the operator:\n" +
		"  first line\n\n  second paragraph\n\n  another"
	if got := c.Render(); got != want {
		t.Errorf("Render() =\n%q\nwant\n%q", got, want)
	}
}

func TestClarification_Validate(t *testing.T) {
	if err := contractsClarification().Validate(); err != nil {
		t.Fatalf("the contract example is invalid: %v", err)
	}
	var nilC *Clarification
	if err := nilC.Validate(); err != nil {
		t.Errorf("an absent clarification is valid: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(c *Clarification)
		want   string
	}{
		{"fewer checks than rounds + 1", func(c *Clarification) { c.Checks = c.Checks[:2] }, "one more check than rounds"},
		{"more checks than rounds + 1", func(c *Clarification) { c.Checks = append(c.Checks, 0.9) }, "one more check than rounds"},
		{"probability above 1", func(c *Clarification) { c.Checks[1] = 1.2 }, "checks[1]"},
		{"negative probability", func(c *Clarification) { c.Checks[0] = -0.1 }, "checks[0]"},
		{"NaN probability", func(c *Clarification) { c.Checks[2] = math.NaN() }, "checks[2]"},
		{"zero threshold", func(c *Clarification) { c.Threshold = 0 }, "threshold"},
		{"threshold above 1", func(c *Clarification) { c.Threshold = 1.5 }, "threshold"},
		{"unknown status", func(c *Clarification) { c.Status = "maybe" }, "unknown status"},
		{"disabled status", func(c *Clarification) { c.Status = ClarityDisabled }, "unknown status"},
		{"no rounds", func(c *Clarification) { c.Rounds, c.Checks = nil, c.Checks[:1] }, "0 rounds"},
		{"first round an addition", func(c *Clarification) {
			c.Rounds[0], c.Rounds[1] = c.Rounds[1], c.Rounds[0]
		}, "rounds[0] has kind"},
		{"unknown kind", func(c *Clarification) { c.Rounds[1].Kind = "chat" }, "unknown kind"},
		{"unknown mode", func(c *Clarification) { c.Rounds[0].Mode = "manual" }, "unknown mode"},
		{"unknown by", func(c *Clarification) { c.Rounds[0].Points[0].By = "model" }, `unknown by "model"`},
		{"answered without answer", func(c *Clarification) { c.Rounds[0].Points[0].Answer = " " }, "has no answer"},
		{"open with an answer", func(c *Clarification) { c.Rounds[0].Points[3].Answer = "x" }, "open point"},
		{"source on a non-docs point", func(c *Clarification) { c.Rounds[0].Points[1].Source = "docs/x.md" }, "source is only for"},
		{"assumption in interactive", func(c *Clarification) { c.Rounds[0].Mode = ClarifyInteractive }, "no assumptions"},
		{"too many points", func(c *Clarification) {
			p := c.Rounds[0].Points[3]
			c.Rounds[0].Points = append(c.Rounds[0].Points, p, p, p, p, p)
		}, "9 points"},
		{"long field", func(c *Clarification) { c.Rounds[0].Points[0].Question = strings.Repeat("q", 301) }, "question is 301 bytes"},
		{"empty question", func(c *Clarification) { c.Rounds[0].Points[0].Question = "" }, "required"},
		{"empty addition", func(c *Clarification) { c.Rounds[1].Text = " " }, "no text"},
		{"long addition", func(c *Clarification) { c.Rounds[1].Text = strings.Repeat("a", MaxClarifyAdditionBytes+1) }, "at most"},
		{"points on addition", func(c *Clarification) {
			c.Rounds[1].Points = []ClarifyPoint{{Unclear: "u", Question: "q"}}
		}, "only text"},
		{"text on a clarify round", func(c *Clarification) { c.Rounds[0].Text = "x" }, "no text"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := contractsClarification()
			tc.mutate(c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestClarification_JSONShape(t *testing.T) {
	data, err := json.Marshal(contractsClarification())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"threshold":0.8,"checks":[0.42,0.55,0.86],"status":"clear","rounds":[` +
		`{"kind":"clarify","mode":"auto","points":[` +
		`{"unclear":"export format","question":"Which export format is meant?","answer":"CSV","by":"docs","source":"docs/export.md#Formats"},` +
		`{"unclear":"who sees it","question":"Which users get the button?","answer":"admins only","by":"operator"},` +
		`{"unclear":"guests","question":"Do guests see the button?","answer":"the button is hidden for guests","by":"assumption"},` +
		`{"unclear":"date locale","question":"Which locale is the date in?"}]},` +
		`{"kind":"addition","text":"..."}]}`
	if string(data) != want {
		t.Errorf("JSON =\n%s\nwant\n%s", data, want)
	}
}

func TestClarification_Open(t *testing.T) {
	open := contractsClarification().Open()
	if len(open) != 1 || open[0].Question != "Which locale is the date in?" {
		t.Errorf("Open() = %+v", open)
	}
	var nilC *Clarification
	if nilC.Open() != nil {
		t.Error("nil Open() is not nil")
	}
}
