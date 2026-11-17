package listener

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// The requirements comment of an Epic's plan (specs/listener-requirements/SPEC.md §3): what the plan
// understood from the Epic and its references, written on the Epic before it is split, and the
// questions that stop the plan. Like every comment of the listener it is a Go template; the model's
// text is escaped. Compose adds the Marker line above it, so the next plan of the Epic reads the
// discussion that follows it.

// Requirements is the requirements call's answer as the comment needs it (the package cannot import
// internal/story).
type Requirements struct {
	Summary      string
	Requirements []string
	Questions    []string
	// BackTo is the status the Epic is moved back to once the questions are answered.
	BackTo string
}

const (
	// maxSummaryBytes bounds the summary in the comment: the bound of the requirements answer.
	maxSummaryBytes = 4000
	// requirementsRoom is what the comment's body may take: Compose's limit, less its marker line and a
	// margin, so that Compose never has to cut it (and the questions, last, are never cut).
	requirementsRoom = MaxCommentBytes - 256
	// cutItem stands for the requirements left out of a comment that would be too long.
	cutItem = "* …"
	// zeroWidthSpace keeps a line from being read as a heading or a quote.
	zeroWidthSpace = "​"
)

// wikiLeadRE is a line wiki markup would read as a heading or a quote.
var wikiLeadRE = regexp.MustCompile(`(?i)^(h[1-6]|bq)\.`)

// RequirementsComment is the body of the requirements comment (SPEC §3): the Epic and the names of its
// references, the summary, one bullet per requirement and, when there are questions, how to answer
// them and one numbered line each. When the whole would be too long for a comment the summary and the
// requirements are cut (the requirements from the end, marked "* …"); the questions are always whole.
func RequirementsComment(esc Escape, epic string, refs []string, a Requirements) []string {
	head := "Requirements of " + keyText(esc, epic) + ", from the Epic"
	if len(refs) > 0 {
		names := make([]string, len(refs))
		for i, r := range refs {
			names[i] = text(esc, r)
		}
		head += " and its references (" + strings.Join(names, ", ") + ")"
	}
	head += ":"

	var questions []string
	if len(a.Questions) > 0 {
		back := strings.TrimSpace(a.BackTo)
		if back == "" {
			back = "the queue"
		}
		questions = append(questions, "", "Open questions — answer them in a comment and move the Epic back to "+esc(oneLine(back))+":")
		for _, q := range a.Questions {
			questions = append(questions, "# "+text(esc, q))
		}
	}

	size := func(lines []string) int { return len(strings.Join(lines, "\n")) }
	room := requirementsRoom - size(questions)
	// The summary leaves room for the head and for the mark of a cut.
	body := []string{head, summaryLine(esc, a.Summary, room-len(head)-len(cutItem)-2)}
	for i, r := range a.Requirements {
		line := "* " + text(esc, r)
		rest := 0
		if i < len(a.Requirements)-1 {
			rest = len(cutItem) + 1
		}
		if size(body)+1+len(line)+rest > room {
			body = append(body, cutItem)
			break
		}
		body = append(body, line)
	}
	return append(body, questions...)
}

// summaryLine is the summary on one line, escaped, at most maxSummaryBytes before its escaping and
// limit after it, never read as a heading, a quote or a numbered list.
func summaryLine(esc Escape, s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	keep := min(len(s), maxSummaryBytes)
	for {
		line := lead(esc(cutAt(s, keep)))
		if len(line) <= limit || keep == 0 {
			return line
		}
		keep -= max(len(line)-limit, 1)
		keep = max(keep, 0)
	}
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
