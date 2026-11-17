package listener

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// The requirements comment of an Epic's plan (specs/listener-requirements/SPEC.md §3): what the plan
// understood from the Epic and its references, written on the Epic before it is split, and the
// questions that stop the plan. It is for the people on the Epic only — nothing parses it, the next
// plan reads it as discussion — so it holds the short summary and the questions, never the detailed
// requirements (those go to specify and to the children's descriptions). Like every comment of the
// listener it is a Go template; the model's text is escaped. Compose adds the Marker line above it.

// Requirements is the requirements call's answer as the comment needs it (the package cannot import
// internal/story).
type Requirements struct {
	Summary string
	// Decided are the defaults taken and the answers found in the code instead of asking.
	Decided   []string
	Questions []string
	// BackTo is the status the Epic is moved back to once the questions are answered.
	BackTo string
}

const (
	// maxSummaryBytes bounds the summary in the comment: the bound of the requirements answer.
	maxSummaryBytes = 1200
	// zeroWidthSpace keeps a line from being read as a heading or a quote.
	zeroWidthSpace = "​"
)

// wikiLeadRE is a line wiki markup would read as a heading or a quote.
var wikiLeadRE = regexp.MustCompile(`(?i)^(h[1-6]|bq)\.`)

// RequirementsComment is the body of the requirements comment (SPEC §3): the Epic and the names of its
// references, the summary, what was decided without asking (one bullet each) and, when there are
// questions, how to answer them and one numbered line each. The answer's bounds keep it far below a comment's limit.
func RequirementsComment(esc Escape, epic string, refs []string, a Requirements) []string {
	head := "How the plan understood " + keyText(esc, epic)
	if len(refs) > 0 {
		names := make([]string, len(refs))
		for i, r := range refs {
			names[i] = text(esc, r)
		}
		head += " (references: " + strings.Join(names, ", ") + ")"
	}
	body := []string{head + ":", summaryLine(esc, a.Summary)}
	if len(a.Decided) > 0 {
		body = append(body, "", "Decided without asking — if one is wrong, say so in a comment:")
		for _, d := range a.Decided {
			body = append(body, "* "+text(esc, d))
		}
	}
	if len(a.Questions) > 0 {
		back := strings.TrimSpace(a.BackTo)
		if back == "" {
			back = "the queue"
		}
		body = append(body, "", "Open questions — answer them in a comment and move the Epic back to "+esc(oneLine(back))+":")
		for _, q := range a.Questions {
			body = append(body, "# "+text(esc, q))
		}
	}
	return body
}

// summaryLine is the summary on one line, escaped, at most maxSummaryBytes before its escaping,
// never read as a heading, a quote or a numbered list.
func summaryLine(esc Escape, s string) string {
	return lead(esc(cutAt(strings.Join(strings.Fields(s), " "), maxSummaryBytes)))
}

// cutAt is s cut to at most n bytes at a character boundary, marked "…" when something was left out.
func cutAt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// lead keeps an escaped line from starting a heading, a quote or a numbered list.
func lead(s string) string {
	switch {
	case strings.HasPrefix(s, "#"):
		return `\` + s
	case wikiLeadRE.MatchString(s):
		return zeroWidthSpace + s
	}
	return s
}
