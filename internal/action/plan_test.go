package action

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParsePlan_CapsTheCriteria(t *testing.T) {
	// 12 criteria: blanks, one of 1 KB of 2-byte runes, and more than the cap after it.
	long := strings.Repeat("é", 512)
	in := []string{"first", "", "  ", long, "third", "fourth", "fifth", "sixth", "seventh", "eighth", "ninth", "tenth"}
	raw, err := json.Marshal(map[string]any{"summary": "s", "steps": []string{"a"}, "acceptance_criteria": in})
	if err != nil {
		t.Fatal(err)
	}

	plan := parsePlan(string(raw))
	if got, want := len(plan.AcceptanceCriteria), maxAcceptanceCriteria; got != want {
		t.Fatalf("kept %d criteria, want %d: %q", got, want, plan.AcceptanceCriteria)
	}
	if plan.AcceptanceCriteria[0] != "first" || plan.AcceptanceCriteria[2] != "third" || plan.AcceptanceCriteria[7] != "eighth" {
		t.Errorf("criteria are not the first non-blank ones in order: %q", plan.AcceptanceCriteria)
	}
	cut := plan.AcceptanceCriteria[1]
	if len(cut) > maxCriterionBytes || !utf8.ValidString(cut) || !strings.HasPrefix(long, cut) {
		t.Errorf("the long criterion = %d bytes, valid UTF-8 %v; want a prefix of at most %d bytes on a rune boundary",
			len(cut), utf8.ValidString(cut), maxCriterionBytes)
	}
	for i, c := range plan.AcceptanceCriteria {
		if len(c) > maxCriterionBytes || !utf8.ValidString(c) {
			t.Errorf("criterion %d is %d bytes, valid UTF-8 %v", i, len(c), utf8.ValidString(c))
		}
	}
}

func TestParsePlan_CriteriaAreOptional(t *testing.T) {
	plan := parsePlan(`{"summary":"s","steps":["a"],"affected_files":[],"risks":[]}`)
	if plan.AcceptanceCriteria != nil {
		t.Errorf("criteria = %q, want none", plan.AcceptanceCriteria)
	}
	if blank := parsePlan(`{"summary":"s","acceptance_criteria":["", " "]}`); blank.AcceptanceCriteria != nil {
		t.Errorf("blank criteria = %q, want none", blank.AcceptanceCriteria)
	}
	if fallback := parsePlan("not json"); fallback.Summary != "not json" || fallback.AcceptanceCriteria != nil {
		t.Errorf("fallback = %+v", fallback)
	}
}

func TestCutRunes(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abc", 3, "abc"},
		{"abc", 2, "ab"},
		{"aé", 2, "a"}, // é is two bytes: cutting inside it steps back
		{"aé", 3, "aé"},
		{"日本", 4, "日"},
		{"日本", 0, ""},
	}
	for _, tc := range cases {
		if got := cutRunes(tc.in, tc.n); got != tc.want {
			t.Errorf("cutRunes(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}
