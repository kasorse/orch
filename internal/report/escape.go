package report

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file is how report.md keeps untrusted text out of Markdown (specs/delivery/SPEC.md §5).
// The task, the ticket and every string a model wrote reach a merge request, where GitLab
// renders them: they may not become HTML, a link, an image, a mention, a reference, a quick
// action or a code fence.

// mdText escapes s so that it renders as the text it is. The characters Markdown or GitLab
// reads as markup get a backslash: raw HTML (< > &), link and image brackets, `, and the
// references that notify or link (@user, #123, !123, ~label, %milestone, the % only before a
// word character). The backslash itself is escaped, so that text cannot cancel an escape
// made here. A line that starts with / is indented by one space, since GitLab runs a quick
// action (/merge) only at the start of a line. CR and other control characters are dropped.
func mdText(s string) string {
	var b strings.Builder
	atLineStart := true
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '\n':
			b.WriteRune(r)
			atLineStart = true
			continue
		case r == '\t':
			r = ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r) && r != '‍':
			// Controls and format characters (a bidi override) hide what a line says.
			continue
		}
		if atLineStart && r == '/' {
			b.WriteByte(' ')
		}
		atLineStart = false
		switch r {
		case '\\', '<', '>', '&', '[', ']', '!', '`', '@', '#', '~':
			b.WriteByte('\\')
		case '%':
			if i+1 < len(runes) && isWordRune(runes[i+1]) {
				b.WriteByte('\\')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// mdCell is s as one Markdown table cell: one line, escaped, with no column separator.
func mdCell(s string) string {
	return strings.ReplaceAll(mdText(oneLine(s)), "|", `\|`)
}

// mdCode is s as an inline code span. A code span has no escapes, so a backtick in s, which
// would end it, becomes an apostrophe, and a | stays inside a table cell.
func mdCode(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(oneLine(s), "`", "'"), "|", `\|`)
	if s == "" {
		return noValue
	}
	return "`" + s + "`"
}

// cutRunes is s cut to at most max runes, ending in … when it was cut.
func cutRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}
