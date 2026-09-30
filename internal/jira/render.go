package jira

import (
	"fmt"
	"strings"
	"unicode"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// Render turns a validated ticket and the operator's notes into the task text
// (SPEC.md §9). The same input always gives the same bytes: values are verbatim except
// that \r\n becomes \n, trailing whitespace is removed from every line and blank lines at
// both ends of each section are dropped. Wiki markup is not converted. Empty sections are
// left out, and the notes come last so the operator's own words are the final thing each
// stage reads. A text longer than maxBytes is refused, never truncated.
func Render(t domain.Ticket, notes string, maxBytes int) (string, error) {
	header := []string{clean(t.Key + ": " + t.Summary)}
	if typ := clean(t.Type); typ != "" {
		header = append(header, "Type: "+typ)
	}
	if t.Parent != nil {
		parent := "Parent: " + t.Parent.Key
		if summary := clean(t.Parent.Summary); summary != "" {
			parent += " — " + summary
		}
		header = append(header, clean(parent))
	}
	header = append(header, "Source: "+t.URL)

	var b strings.Builder
	b.WriteString(strings.Join(header, "\n"))
	section := func(label, body string) {
		if body = clean(body); body != "" {
			fmt.Fprintf(&b, "\n\n%s:\n%s", label, body)
		}
	}
	section("Description", t.Description)
	for _, f := range t.Fields {
		section(f.Label, f.Value)
	}
	section("Operator notes", strings.TrimSpace(notes))
	b.WriteString("\n")

	out := b.String()
	if len(out) > maxBytes {
		return "", apperr.NewConfigError(fmt.Sprintf(
			"%s renders to %d bytes, more than jira.max_bytes (%d); raise the limit or trim the ticket",
			t.Key, len(out), maxBytes), nil)
	}
	return out, nil
}

// clean normalises line endings, strips each line's trailing whitespace and drops blank
// lines at both ends.
func clean(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
