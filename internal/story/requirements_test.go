package story

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func goodRequirements() map[string]any {
	return map[string]any{
		"summary":      "Digital categories are dictionaries per country.",
		"requirements": []any{"a category has a name and a country", "the dictionary's values come from product-master"},
		"questions":    []any{"which countries are in scope?"},
	}
}

func TestParseRequirements(t *testing.T) {
	a, err := ParseRequirements(marshal(t, goodRequirements()))
	if err != nil {
		t.Fatal(err)
	}
	if a.Summary == "" || len(a.Requirements) != 2 || a.Requirements[0] != "a category has a name and a country" ||
		len(a.Questions) != 1 || a.Questions[0] != "which countries are in scope?" {
		t.Errorf("answer = %+v", a)
	}
	for name, obj := range map[string]string{
		"no questions key":   `{"summary": "s", "requirements": ["r"]}`,
		"empty questions":    `{"summary": "s", "requirements": ["r"], "questions": []}`,
		"null questions":     `{"summary": "s", "requirements": ["r"], "questions": null}`,
		"surrounding spaces": " \n" + `{"summary": "s", "requirements": ["r"]}` + "\n",
	} {
		if _, err := ParseRequirements([]byte(obj)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseRequirements_Bounds(t *testing.T) {
	many := func(n int, item string) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = item
		}
		return out
	}
	cases := []struct {
		name   string
		change func(m map[string]any)
		field  string
	}{
		{"unknown key", func(m map[string]any) { m["verdict"] = "ok" }, "verdict"},
		{"summary missing", func(m map[string]any) { delete(m, "summary") }, "summary"},
		{"summary blank", func(m map[string]any) { m["summary"] = " \n" }, "summary"},
		{"summary at the limit", func(m map[string]any) { m["summary"] = strings.Repeat("s", MaxRequirementsSummaryBytes) }, ""},
		{"summary over the limit, cut", func(m map[string]any) { m["summary"] = strings.Repeat("s", MaxRequirementsSummaryBytes+1) }, ""},
		{"summary of the wrong type", func(m map[string]any) { m["summary"] = 3 }, "summary"},
		{"requirements missing", func(m map[string]any) { delete(m, "requirements") }, "requirements"},
		{"requirements empty", func(m map[string]any) { m["requirements"] = []any{} }, "requirements"},
		{"requirements of the wrong type", func(m map[string]any) { m["requirements"] = "one" }, "requirements"},
		{"requirements at the limit", func(m map[string]any) { m["requirements"] = many(MaxRequirements, "r") }, ""},
		{"too many requirements", func(m map[string]any) { m["requirements"] = many(MaxRequirements+1, "r") }, "requirements"},
		{"a blank requirement", func(m map[string]any) { m["requirements"] = []any{"r", "  "} }, "requirements[1]"},
		{"a requirement at the limit", func(m map[string]any) { m["requirements"] = []any{strings.Repeat("r", MaxRequirementBytes)} }, ""},
		{"a requirement over the limit, cut", func(m map[string]any) { m["requirements"] = []any{"r", strings.Repeat("r", MaxRequirementBytes+1)} }, ""},
		{"many questions", func(m map[string]any) { m["questions"] = many(50, "q") }, ""},
		{"a blank question", func(m map[string]any) { m["questions"] = []any{""} }, "questions[0]"},
		{"a question at the limit", func(m map[string]any) { m["questions"] = []any{strings.Repeat("q", MaxQuestionBytes)} }, ""},
		{"a question over the limit, cut", func(m map[string]any) { m["questions"] = []any{strings.Repeat("q", MaxQuestionBytes+1)} }, ""},
		{"questions of the wrong type", func(m map[string]any) { m["questions"] = "which?" }, "questions"},
		{"many assumptions", func(m map[string]any) { m["assumptions"] = many(50, "a") }, ""},
		{"a blank assumption", func(m map[string]any) { m["assumptions"] = []any{" "} }, "assumptions[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := goodRequirements()
			tc.change(m)
			_, err := ParseRequirements(marshal(t, m))
			if tc.field == "" {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				return
			}
			wantAnswerErr(t, err, tc.field)
		})
	}
}

func TestParseRequirements_NotAnObject(t *testing.T) {
	for name, obj := range map[string]string{
		"empty":         "",
		"an array":      `["r"]`,
		"broken":        `{"summary": `,
		"trailing data": `{"summary": "s", "requirements": ["r"]} {}`,
	} {
		_, err := ParseRequirements([]byte(obj))
		wantAnswerErr(t, err, "")
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestParseLookup(t *testing.T) {
	a, err := ParseLookup([]byte(`{"answers":[{"question":2,"answer":"Core publishes on save.","evidence":"sku-master/publish.go"}]}`), 2)
	if err != nil || len(a.Answers) != 1 || a.Answers[0].Question != 2 {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	if a, err := ParseLookup([]byte(`{"answers":[]}`), 1); err != nil || len(a.Answers) != 0 {
		t.Errorf("no answer: %+v, %v", a, err)
	}
	item := func(q int, answer, evidence string) string {
		return fmt.Sprintf(`{"question":%d,"answer":%q,"evidence":%q}`, q, answer, evidence)
	}
	for name, tc := range map[string]struct{ obj, field string }{
		"unknown key":       {`{"answers":[],"open":[1]}`, "open"},
		"question 0":        {`{"answers":[` + item(0, "a", "e") + `]}`, "answers[0].question"},
		"question too high": {`{"answers":[` + item(3, "a", "e") + `]}`, "answers[0].question"},
		"question twice":    {`{"answers":[` + item(1, "a", "e") + `,` + item(1, "b", "e") + `]}`, "answers[1].question"},
		"blank answer":      {`{"answers":[` + item(1, " ", "e") + `]}`, "answers[0].answer"},
		"no evidence":       {`{"answers":[` + item(1, "a", "") + `]}`, "answers[0].evidence"},
	} {
		_, err := ParseLookup([]byte(tc.obj), 2)
		var aerr *AnswerError
		if !errors.As(err, &aerr) || aerr.Field != tc.field {
			t.Errorf("%s: err = %v, want a field error on %s", name, err, tc.field)
		}
	}
}

// A text over its bound is cut at a character, marked, and kept within the bound; the call is not refused.
func TestParse_OverlongTextsAreCut(t *testing.T) {
	long := strings.Repeat("ж", 400) // 800 bytes
	a, err := ParseRequirements(marshal(t, map[string]any{"summary": long, "requirements": []any{long}, "questions": []any{long}, "assumptions": []any{long}}))
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]struct {
		s     string
		bound int
	}{
		"summary": {a.Summary, 800}, "requirement": {a.Requirements[0], MaxRequirementBytes},
		"question": {a.Questions[0], MaxQuestionBytes}, "assumption": {a.Assumptions[0], MaxAssumptionBytes},
	} {
		if got.bound >= len(long) {
			continue
		}
		if len(got.s) > got.bound || !strings.HasSuffix(got.s, "…") || !utf8.ValidString(got.s) {
			t.Errorf("%s: %d bytes (bound %d), %q…", name, len(got.s), got.bound, got.s[len(got.s)-6:])
		}
	}
	l, err := ParseLookup([]byte(`{"answers":[{"question":1,"answer":"`+strings.Repeat("a", 700)+`","evidence":"`+strings.Repeat("e", 400)+`"}]}`), 1)
	if err != nil || len(l.Answers[0].Answer) > MaxLookupAnswerBytes || len(l.Answers[0].Evidence) > MaxLookupEvidenceBytes ||
		!strings.HasSuffix(l.Answers[0].Answer, "…") {
		t.Errorf("lookup = %+v, %v", l, err)
	}
}
