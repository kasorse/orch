package action

import "testing"

func TestExtractJSONObject(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"bare", `{"a":1}`, `{"a":1}`},
		{"fenced after prose", "Done.\n```json\n{\"a\":1}\n```\n", `{"a":1}`},
		{"code braces in prose first", "Set `Actions: []string{\"implement\"}`.\n```json\n{\"a\":\"}\"}\n```", `{"a":"}"}`},
		{"brace inside a string", `{"a":"x { y"}`, `{"a":"x { y"}`},
		{"none", "no object {here", ""},
	}
	for _, c := range cases {
		if got := extractJSONObject(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// An unescaped quote in the summary breaks the object; summary_short and the lists after it
// are still read, and the whole output stays the summary (run 7afc3de2, ST02).
func TestParseImplementation_BrokenSummaryKeepsTheFieldsAfterIt(t *testing.T) {
	out := `{"summary":"the "providers must report cost" rule","summary_short":"Add story config",` +
		`"changed_files":["a.go"],"tests_added":["a_test.go"]}`
	got := parseImplementation(out)
	if got.Summary != out || got.SummaryShort != "Add story config" ||
		len(got.ChangedFiles) != 1 || got.ChangedFiles[0] != "a.go" || len(got.TestsAdded) != 1 {
		t.Errorf("parseImplementation = %+v", got)
	}
	if got := parseImplementation("no json at all"); got.SummaryShort != "" || got.Summary != "no json at all" {
		t.Errorf("plain text = %+v", got)
	}
	if got := parseImplementation(`{"summary":"x "y"", "summary_short": 7}`); got.SummaryShort != "" {
		t.Errorf("a non-string summary_short was taken: %+v", got)
	}
}
