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

// ParseSpec reads the parts of a SPEC.md that story run uses: the title from the "# " heading
// and the text of the Approach and Contracts sections, as RenderSpec writes them. A hand-written
// file may lack any of them, which gives "" for it; "—", RenderSpec's mark of an empty section,
// is "" too. Headings inside a code fence are text.
func ParseSpec(data []byte) (title, approach, contracts string) {
	section := ""
	var cur strings.Builder
	flush := func() {
		text := strings.TrimSpace(cur.String())
		if text == "—" {
			text = ""
		}
		switch section {
		case "Approach":
			approach = text
		case "Contracts":
			contracts = text
		}
		cur.Reset()
	}
	fence := false
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "```") {
			fence = !fence
		} else if !fence {
			if t, ok := strings.CutPrefix(line, "# "); ok && title == "" && section == "" {
				title = oneLine(t)
				continue
			}
			if h, ok := strings.CutPrefix(line, "## "); ok {
				flush()
				section = strings.TrimSpace(h)
				continue
			}
		}
		if section != "" {
			cur.WriteString(line + "\n")
		}
	}
	flush()
	return title, approach, contracts
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
