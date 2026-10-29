package jira

import (
	"fmt"
	"strings"
)

// BuildJQL is the listener's query (specs/jira-listener/SPEC.md §3.1), built here and never
// taken from the config: the tickets of projects that carry label, are in one of statuses and
// were reported by one of reporters, most urgent and oldest first. Every value is written in
// double quotes.
//
// A value holding a double quote, a backslash or a control character panics: config validation
// refuses those first, and a quote that escaped its string would let a value change the query.
func BuildJQL(projects []string, label string, statuses, reporters []string) string {
	return fmt.Sprintf("project in (%s) AND labels = %s AND status in (%s) AND reporter in (%s) ORDER BY priority DESC, created ASC",
		jqlList(projects), jqlString(label), jqlList(statuses), jqlList(reporters))
}

func jqlList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = jqlString(v)
	}
	return strings.Join(quoted, ", ")
}

func jqlString(v string) string {
	if strings.ContainsFunc(v, func(r rune) bool { return r == '"' || r == '\\' || r < ' ' || r == 0x7f }) {
		panic(fmt.Sprintf("jira.BuildJQL: %q holds a quote, a backslash or a control character; config validation must refuse it", v))
	}
	return `"` + v + `"`
}
