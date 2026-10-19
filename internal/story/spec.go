package story

import "strings"

// maxFileSlugLen bounds the title part of a task file's name.
const maxFileSlugLen = 40

// RenderSpec is the SPEC.md of a spec directory (SPEC §8): the title, then the five sections of
// the specify answer in a fixed order, each its trimmed text or "—" when it is empty. Output is
// LF only, ends with one line break, and is a pure function of the arguments.
func RenderSpec(title string, s SpecText) []byte {
	var b strings.Builder
	b.WriteString("# " + oneLine(title) + "\n")
	for _, sec := range []struct{ heading, text string }{
		{"Problem", s.Problem},
		{"Approach", s.Approach},
		{"Contracts", s.Contracts},
		{"Out of scope", s.OutOfScope},
		{"Risks", s.Risks},
	} {
		text := strings.TrimSpace(strings.ReplaceAll(sec.text, "\r\n", "\n"))
		if text == "" {
			text = "—"
		}
		b.WriteString("\n## " + sec.heading + "\n\n" + text + "\n")
	}
	return []byte(b.String())
}

// TaskFileName is the name of a task's file, TASK-<ID>-<slug>.md, as ParseTaskFile reads it.
// The slug is built here from the title, never taken from it: ASCII letters (lowercased) and
// digits are kept, every other run of characters becomes one "-", and the result is cut to 40
// bytes at a "-" where there is one. A title with nothing left (a title in another script)
// gives "task".
func TaskFileName(id, title string) string {
	var b strings.Builder
	dash := false
	for _, r := range title {
		switch {
		case r >= 'A' && r <= 'Z':
			r += 'a' - 'A'
			fallthrough
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	slug := b.String()
	if len(slug) > maxFileSlugLen {
		cut := slug[:maxFileSlugLen]
		if i := strings.LastIndexByte(cut, '-'); slug[maxFileSlugLen] != '-' && i > 0 {
			cut = cut[:i]
		}
		slug = strings.TrimRight(cut, "-")
	}
	if slug == "" {
		slug = "task"
	}
	return "TASK-" + id + "-" + slug + ".md"
}
