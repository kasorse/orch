package story

import (
	"strings"
	"testing"
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
		{"summary over the limit", func(m map[string]any) { m["summary"] = strings.Repeat("s", MaxRequirementsSummaryBytes+1) }, "summary"},
		{"summary of the wrong type", func(m map[string]any) { m["summary"] = 3 }, "summary"},
		{"requirements missing", func(m map[string]any) { delete(m, "requirements") }, "requirements"},
		{"requirements empty", func(m map[string]any) { m["requirements"] = []any{} }, "requirements"},
		{"requirements of the wrong type", func(m map[string]any) { m["requirements"] = "one" }, "requirements"},
		{"requirements at the limit", func(m map[string]any) { m["requirements"] = many(MaxRequirements, "r") }, ""},
		{"too many requirements", func(m map[string]any) { m["requirements"] = many(MaxRequirements+1, "r") }, "requirements"},
		{"a blank requirement", func(m map[string]any) { m["requirements"] = []any{"r", "  "} }, "requirements[1]"},
		{"a requirement at the limit", func(m map[string]any) { m["requirements"] = []any{strings.Repeat("r", MaxRequirementBytes)} }, ""},
		{"a requirement over the limit", func(m map[string]any) { m["requirements"] = []any{"r", strings.Repeat("r", MaxRequirementBytes+1)} }, "requirements[1]"},
		{"questions at the limit", func(m map[string]any) { m["questions"] = many(MaxQuestions, "q") }, ""},
		{"too many questions", func(m map[string]any) { m["questions"] = many(MaxQuestions+1, "q") }, "questions"},
		{"a blank question", func(m map[string]any) { m["questions"] = []any{""} }, "questions[0]"},
		{"a question at the limit", func(m map[string]any) { m["questions"] = []any{strings.Repeat("q", MaxQuestionBytes)} }, ""},
		{"a question over the limit", func(m map[string]any) { m["questions"] = []any{strings.Repeat("q", MaxQuestionBytes+1)} }, "questions[0]"},
		{"questions of the wrong type", func(m map[string]any) { m["questions"] = "which?" }, "questions"},
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
