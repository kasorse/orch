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
