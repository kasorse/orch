package jira

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/kasorse/orch/internal/domain"
)

// zeroWidth is a zero-width space. It sits after a literal backslash, so that backslash can
// never escape the character after it (our own escape, or the second half of a line break), and
// before text that would otherwise start a heading or a quote.
const zeroWidth = "​"

// maxListDepth is how deep a nested list goes; a deeper item stays on the last level.
const maxListDepth = 5

var (
	wikiHeadingRE = regexp.MustCompile(`^(#{1,6})[ \t]+(.*)$`)
	wikiListRE    = regexp.MustCompile(`^([ \t]*)([-*+]|\d+[.)])[ \t]+(.*)$`)
	wikiRuleRE    = regexp.MustCompile(`^ {0,3}(-{3,}|\*{3,}|_{3,})[ \t]*$`)
	wikiQuoteRE   = regexp.MustCompile(`^ {0,3}>[ \t]?(.*)$`)
	wikiLeadRE    = regexp.MustCompile(`(?i)^(h[1-6]|bq)\.`)
	wikiLangRE    = regexp.MustCompile(`^[A-Za-z0-9+#_-]{1,20}$`)
)

// WikiTask is what the description of a child issue is made of.
type WikiTask struct {
	// Body is the task's own text as the task file has it (story.Task.Body): Markdown with
	// the Goal, Acceptance and Notes sections. It is written by a model or an operator and
	// never trusted to be wiki markup.
	Body string
	// Repo and SpecPath name where the work is: the service and the task file.
	Repo, SpecPath string
	// DependencyKeys are the Jira keys of the tasks this one depends on, those that have one.
	DependencyKeys []string

	// The fields below are the listener's (specs/listener-story/SPEC.md §8.2). With Epic set the
	// description starts with the story's lines and ends with the task's own Notes, instead of the
	// one-line trailer above, which Repo, SpecPath and DependencyKeys then do not feed.

	// Epic and EpicSummary are the Epic the plan was made for: "Story: <Epic> — <summary>".
	Epic, EpicSummary string
	// Service and ServicePath are the service the task changes and its project path.
	Service, ServicePath string
	// References are the URLs of the reference repositories, which the child's text links so that its
	// investigation can fetch them.
	References []string
	// Needs are the tasks of other services this one needs, which no link chains.
	Needs []WikiNeed
}

// WikiNeed is a task of another service that a task needs: its Jira key and its service.
type WikiNeed struct{ Key, Service string }

// Wiki renders the task as Jira wiki markup. The Markdown structure is kept - headings, bullet
// and numbered lists with their nesting, code blocks, inline code, bold - and every other
// character that wiki syntax would read as markup (macros, links, user mentions, tables,
// images, emphasis) is escaped, so text cannot start anything Jira would act on. Code is
// carried verbatim. It ends with a line naming the repository, the spec file and the
// dependencies (specs/story/SPEC.md §12.2). The same input always gives the same bytes.
func Wiki(t WikiTask) string {
	out := markdown(t.Body)
	if t.Epic != "" {
		return listenerWiki(t, out)
	}
	deps := "—"
	if len(t.DependencyKeys) > 0 {
		deps = strings.Join(t.DependencyKeys, ", ")
	}
	trailer := "Repository: " + escape(t.Repo) + " · Spec: " + escape(t.SpecPath) + " · Depends on: " + escape(deps)
	if len(out) == 0 {
		return trailer + "\n"
	}
	return strings.Join(out, "\n") + "\n\n----\n" + trailer + "\n"
}

// markdown renders Markdown text as the lines of wiki markup (see Wiki), without a trailing blank line.
func markdown(src string) []string {
	type level struct {
		indent int
		marker byte
	}
	var (
		out   []string
		stack []level
		code  bool
	)
	blankLine := func() {
		stack = nil
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}

	for _, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)

		if code {
			if strings.HasPrefix(trimmed, "```") {
				out = append(out, "{code}")
				code = false
				continue
			}
			// A "{code" in the block would end it early and let what follows be markup.
			out = append(out, strings.ReplaceAll(line, "{code", "{ code"))
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			blankLineIfText(&out)
			stack = nil
			lang, _, _ := strings.Cut(strings.TrimSpace(strings.TrimLeft(trimmed, "`")), " ")
			if wikiLangRE.MatchString(lang) {
				out = append(out, "{code:"+lang+"}")
			} else {
				out = append(out, "{code}")
			}
			code = true
			continue
		}

		switch {
		case trimmed == "":
			blankLine()
		case wikiRuleRE.MatchString(line):
			stack = nil
			out = append(out, "----")
		case wikiHeadingRE.MatchString(line):
			stack = nil
			h := wikiHeadingRE.FindStringSubmatch(line)
			out = append(out, "h"+string(rune('0'+len(h[1])))+". "+inline(h[2]))
		case wikiListRE.MatchString(line):
			l := wikiListRE.FindStringSubmatch(line)
			indent := indentWidth(l[1])
			marker := byte('*')
			if l[2][0] >= '0' && l[2][0] <= '9' {
				marker = '#'
			}
			for len(stack) > 0 && stack[len(stack)-1].indent > indent {
				stack = stack[:len(stack)-1]
			}
			switch {
			case len(stack) == 0 || (stack[len(stack)-1].indent < indent && len(stack) < maxListDepth):
				stack = append(stack, level{indent, marker})
			default:
				stack[len(stack)-1].marker = marker
			}
			markers := make([]byte, len(stack))
			for i, lv := range stack {
				markers[i] = lv.marker
			}
			out = append(out, string(markers)+" "+inline(l[3]))
		case wikiQuoteRE.MatchString(line):
			stack = nil
			out = append(out, "bq. "+inline(wikiQuoteRE.FindStringSubmatch(line)[1]))
		default:
			stack = nil
			p := inline(strings.TrimLeft(line, " \t"))
			if wikiLeadRE.MatchString(p) {
				p = zeroWidth + p
			}
			if strings.HasPrefix(p, "#") {
				p = `\` + p
			}
			out = append(out, p)
		}
	}
	if code {
		out = append(out, "{code}")
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// safeURLRE is a URL that can stand in a wiki link as it is: no character wiki markup reads.
var safeURLRE = regexp.MustCompile(`^https?://[A-Za-z0-9._~:/-]+$`)

// plainKey is an issue key as it is, so that Jira links it, or any other text escaped.
func plainKey(s string) string {
	if domain.ValidTicketKey(s) {
		return s
	}
	return escape(s)
}

// listenerWiki is the description of a child the listener creates (specs/listener-story/SPEC.md
// §8.2): the task's body, then the story and the service, the references, the tasks it needs, and the
// plan's approach and contracts, cut so that the whole description is at most t.MaxBytes.
func listenerWiki(t WikiTask, body []string) string {
	// The story's lines come first, one each, so that nothing follows the task's own Notes.
	story := "Story: " + plainKey(t.Epic)
	if strings.TrimSpace(t.EpicSummary) != "" {
		story += " — " + escape(t.EpicSummary)
	}
	story += " · Service: " + escape(t.Service)
	if t.ServicePath != "" {
		story += " (" + escape(t.ServicePath) + ")"
	}
	lines := []string{story}
	if len(t.References) > 0 {
		refs := make([]string, len(t.References))
		for i, u := range t.References {
			if safeURLRE.MatchString(u) {
				refs[i] = "[" + u + "]"
			} else {
				refs[i] = escape(u)
			}
		}
		lines = append(lines, "References (read only): "+strings.Join(refs, ", "))
	}
	if len(t.Needs) > 0 {
		needs := make([]string, len(t.Needs))
		for i, n := range t.Needs {
			needs[i] = plainKey(n.Key) + " (" + escape(n.Service) + ")"
		}
		lines = append(lines, "Needs, not linked: "+strings.Join(needs, ", "))
	}
	if len(body) > 0 {
		lines = append(append(lines, "----"), body...)
	}
	return strings.Join(lines, "\n") + "\n"
}

// blankLineIfText separates a block from the text above it.
func blankLineIfText(out *[]string) {
	if n := len(*out); n > 0 && (*out)[n-1] != "" {
		*out = append(*out, "")
	}
}

// indentWidth is the width of a list item's indent, a tab counting as four.
func indentWidth(s string) int {
	w := 0
	for _, c := range s {
		if c == '\t' {
			w += 4
		} else {
			w++
		}
	}
	return w
}

// inline renders one line of Markdown text: `code` becomes {{code}}, **bold** becomes *bold*,
// and everything else is escaped.
func inline(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch {
		case s[i] == '`':
			n := 1
			for i+n < len(s) && s[i+n] == '`' {
				n++
			}
			if end := closingTicks(s[i+n:], n); end >= 0 {
				content := s[i+n : i+n+end]
				if len(content) > 1 && content[0] == ' ' && content[len(content)-1] == ' ' && strings.TrimSpace(content) != "" {
					content = content[1 : len(content)-1]
				}
				if content != "" {
					b.WriteString("{{" + escape(content) + "}}")
					i += n + end + n
					continue
				}
			}
			b.WriteString(s[i : i+n])
			i += n
			continue
		case strings.HasPrefix(s[i:], "**"):
			if j := strings.Index(s[i+2:], "**"); j > 0 {
				if inner := s[i+2 : i+2+j]; strings.TrimSpace(inner) == inner {
					b.WriteString("*" + inline(inner) + "*")
					i += j + 4
					continue
				}
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteString(escapeRune(r))
		i += size
	}
	return b.String()
}

// closingTicks is the offset in s of the next run of exactly n backticks, or -1.
func closingTicks(s string, n int) int {
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		run := 1
		for i+run < len(s) && s[i+run] == '`' {
			run++
		}
		if run == n {
			return i
		}
		i += run
	}
	return -1
}

// Escape writes s so that wiki syntax reads it as plain text: the escaping every text a model
// wrote goes through before it is part of a comment (specs/delivery/SPEC.md §39.12).
func Escape(s string) string { return escape(s) }

// escape writes s so that wiki syntax reads it as plain text.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		b.WriteString(escapeRune(r))
	}
	return b.String()
}

// escapeRune is r as plain text: the characters wiki markup uses - macros {}, links and
// mentions [], emphasis * _ + - ^ ~ ?, tables |, images ! - get a backslash.
func escapeRune(r rune) string {
	switch r {
	case '\\':
		return `\` + zeroWidth
	case '{', '}', '[', ']', '*', '_', '+', '-', '^', '~', '|', '!', '?':
		return `\` + string(r)
	}
	return string(r)
}
