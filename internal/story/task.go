// Package story is the pure core of a story (specs/story/SPEC.md): the task files, the
// dependency graph with its order and bases, the generated README.md and the "As built"
// record. It runs no git, starts no process and opens no file: every function takes the bytes
// it works on and returns the bytes or the answer. It imports only domain and the standard
// library (imports_test.go).
package story

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxBodyBytes bounds a task's own text (SPEC §8).
const MaxBodyBytes = 16 << 10

// JiraNone is the front matter value with which the operator opts a task out of Jira.
const JiraNone = "none"

const asBuiltHeading = "## As built"

// Task is one task file. The front matter gives ID, Repo, DependsOn and Jira; the heading
// gives Title; Body is the text after the heading (and after the generated "**Size** ..."
// line, if the file has one) up to the "## As built" heading, as the operator left it;
// Acceptance is the list under "## Acceptance". Jira is "" while no key has been written,
// JiraNone, or a key. The task's size is not a field: it lives in the generated line only.
type Task struct {
	ID         string
	Repo       string
	DependsOn  []string
	Jira       string
	Title      string
	Body       string
	Acceptance []string
}

// ValidID reports whether id is a task ID: two capital letters and two digits (SPEC §7.4).
func ValidID(id string) bool {
	if len(id) != 4 {
		return false
	}
	return isUpper(id[0]) && isUpper(id[1]) && isDigit(id[2]) && isDigit(id[3])
}

// ValidRepo reports whether name has the shape of a service name (SPEC §7.1).
func ValidRepo(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		alnum := isUpper(c) || isDigit(c) || (c >= 'a' && c <= 'z')
		if i == 0 && !alnum {
			return false
		}
		if !alnum && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// ValidJiraKey reports whether s is a Jira issue key: <PROJECT>-<number>, the project made of
// capital letters, digits and underscores and starting with a letter.
func ValidJiraKey(s string) bool {
	i := strings.LastIndexByte(s, '-')
	if i < 2 {
		return false
	}
	project, number := s[:i], s[i+1:]
	if !isUpper(project[0]) {
		return false
	}
	for j := 1; j < len(project); j++ {
		c := project[j]
		if !isUpper(c) && !isDigit(c) && c != '_' {
			return false
		}
	}
	if number == "" || len(number) > 9 || number[0] == '0' {
		return false
	}
	for j := 0; j < len(number); j++ {
		if !isDigit(number[j]) {
			return false
		}
	}
	return true
}

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// nextLine returns the line of s that starts at pos without its "\n" and the offset after it.
func nextLine(s string, pos int) (string, int) {
	i := strings.IndexByte(s[pos:], '\n')
	if i < 0 {
		return s[pos:], len(s)
	}
	end := pos + i
	return s[pos:end], end + 1
}

func trimCR(line string) string { return strings.TrimSuffix(line, "\r") }

// asBuiltOffset is the offset of the first line that is the "## As built" heading (alone or
// followed by a space and more, as in "## As built (2026-10-20)"), or -1. The heading counts
// wherever it stands, also inside a code fence: the rule has to be the one Go itself
// applies when it appends the section.
func asBuiltOffset(s string) int {
	for pos := 0; pos < len(s); {
		line, next := nextLine(s, pos)
		line = trimCR(line)
		if line == asBuiltHeading || strings.HasPrefix(line, asBuiltHeading+" ") {
			return pos
		}
		pos = next
	}
	return -1
}

// FrozenHash is the sha256 (hex) of a task file up to its first "## As built" line, or of the
// whole file when there is none: what ran is what the record says (SPEC A6). Trailing line
// breaks are not part of it, so that the blank line Go adds before the section it appends
// does not change the hash of a file that was hashed without one.
func FrozenHash(data []byte) string {
	s := string(data)
	if at := asBuiltOffset(s); at >= 0 {
		s = s[:at]
	}
	sum := sha256.Sum256([]byte(strings.TrimRight(s, "\r\n")))
	return hex.EncodeToString(sum[:])
}

type fmEntry struct {
	key   string
	value string
	start int
	end   int
	next  int
}

type frontMatter struct {
	entries   []fmEntry
	closeAt   int
	bodyStart int
	eol       string
}

// parseFrontMatter splits the front matter between the two "---" lines into key: value
// entries, keeping the offsets SetJira edits. It checks the shape only, not the keys.
func parseFrontMatter(name string, data []byte) (frontMatter, error) {
	var fm frontMatter
	if !utf8.Valid(data) {
		return fm, fmt.Errorf("%s: not valid UTF-8", name)
	}
	s := string(data)
	first, pos := nextLine(s, 0)
	if trimCR(first) != "---" {
		return fm, fmt.Errorf(`%s: front matter missing: the file must start with a "---" line`, name)
	}
	fm.eol = "\n"
	if strings.HasSuffix(first, "\r") {
		fm.eol = "\r\n"
	}
	lineNo := 1
	for pos < len(s) {
		start := pos
		raw, next := nextLine(s, pos)
		pos = next
		lineNo++
		text := trimCR(raw)
		if text == "---" {
			fm.closeAt = start
			fm.bodyStart = next
			return fm, nil
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		i := strings.IndexByte(text, ':')
		if i <= 0 || text[0] == ' ' || text[0] == '\t' {
			return fm, fmt.Errorf(`%s: front matter line %d: want "key: value", got %q`, name, lineNo, text)
		}
		fm.entries = append(fm.entries, fmEntry{key: text[:i], value: strings.TrimSpace(text[i+1:]), start: start, end: start + len(text), next: next})
	}
	return fm, fmt.Errorf(`%s: front matter is not closed: there is no second "---" line`, name)
}

func keyErr(name, key, format string, args ...any) error {
	return fmt.Errorf("%s: front matter key %q: %s", name, key, fmt.Sprintf(format, args...))
}

// idFromFileName reads the ID out of TASK-<ID>-<slug>.md.
func idFromFileName(name string) (string, error) {
	base := name
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	mid, ok := strings.CutPrefix(base, "TASK-")
	if ok {
		mid, ok = strings.CutSuffix(mid, ".md")
	}
	if !ok || len(mid) < 6 || !ValidID(mid[:4]) || mid[4] != '-' {
		return "", fmt.Errorf("%s: the file name must be TASK-<ID>-<slug>.md, like TASK-RF01-refund-amount.md", name)
	}
	return mid[:4], nil
}

func parseDependsOn(v string) ([]string, error) {
	if v == "" {
		return nil, nil
	}
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return nil, fmt.Errorf("want a list like [RF01, RF02] or [], got %q", v)
	}
	inner := strings.TrimSpace(v[1 : len(v)-1])
	if inner == "" {
		return nil, nil
	}
	var ids []string
	seen := map[string]bool{}
	for _, part := range strings.Split(inner, ",") {
		id := strings.TrimSpace(part)
		if !ValidID(id) {
			return nil, fmt.Errorf("%q is not a task ID (two capital letters and two digits)", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("%s is listed twice", id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

// isMetaLine tells the generated "**Size** M · **Repository** ..." line, which is redundant
// with the front matter and is not part of the body.
func isMetaLine(line string) bool {
	return strings.HasPrefix(line, "**Size** ") || strings.HasPrefix(line, "**Repository** ")
}

// ParseTaskFile reads a task file. name is the file's name (TASK-<ID>-<slug>.md) and must
// carry the same ID as the front matter. Front matter has a closed set of keys: id and repo
// are required, depends_on (a list, or empty) and jira (a key or "none") are optional, and
// anything else, a repeated key or a value of another shape is an error that names the key
// and the file. The first heading must be "# <ID> — <title>" with the file's ID.
func ParseTaskFile(name string, data []byte) (Task, error) {
	var t Task
	fileID, err := idFromFileName(name)
	if err != nil {
		return t, err
	}
	fm, err := parseFrontMatter(name, data)
	if err != nil {
		return t, err
	}
	vals := map[string]string{}
	for _, e := range fm.entries {
		switch e.key {
		case "id", "repo", "depends_on", "jira":
		default:
			return t, keyErr(name, e.key, "not allowed; the keys are id, repo, depends_on and jira")
		}
		if _, dup := vals[e.key]; dup {
			return t, keyErr(name, e.key, "given twice")
		}
		vals[e.key] = e.value
	}

	id, ok := vals["id"]
	if !ok {
		return t, keyErr(name, "id", "missing")
	}
	if !ValidID(id) {
		return t, keyErr(name, "id", "%q is not a task ID (two capital letters and two digits)", id)
	}
	if id != fileID {
		return t, keyErr(name, "id", "is %s, but the file name carries %s", id, fileID)
	}
	t.ID = id

	repo, ok := vals["repo"]
	if !ok {
		return t, keyErr(name, "repo", "missing")
	}
	if !ValidRepo(repo) {
		return t, keyErr(name, "repo", "%q is not a service name", repo)
	}
	t.Repo = repo

	if v, ok := vals["depends_on"]; ok {
		deps, err := parseDependsOn(v)
		if err != nil {
			return t, keyErr(name, "depends_on", "%v", err)
		}
		t.DependsOn = deps
	}

	if v, ok := vals["jira"]; ok {
		if v != JiraNone && !ValidJiraKey(v) {
			return t, keyErr(name, "jira", `%q is neither a Jira key like PAY-124 nor "none"`, v)
		}
		t.Jira = v
	}

	rest := string(data)[fm.bodyStart:]
	if at := asBuiltOffset(rest); at >= 0 {
		rest = rest[:at]
	}

	pos := 0
	heading := ""
	for pos < len(rest) {
		line, next := nextLine(rest, pos)
		pos = next
		if strings.TrimSpace(line) != "" {
			heading = trimCR(line)
			break
		}
	}
	title, err := parseHeading(heading, id)
	if err != nil {
		return t, fmt.Errorf("%s: %v", name, err)
	}
	t.Title = title

	bodyFrom := pos
	for p := pos; p < len(rest); {
		line, next := nextLine(rest, p)
		if strings.TrimSpace(line) == "" {
			p = next
			continue
		}
		if isMetaLine(trimCR(line)) {
			bodyFrom = next
		}
		break
	}
	t.Body = rest[bodyFrom:]
	if len(t.Body) > MaxBodyBytes {
		return t, fmt.Errorf("%s: the task text is %d bytes, over the %d-byte limit", name, len(t.Body), MaxBodyBytes)
	}
	t.Acceptance = acceptanceItems(t.Body)
	return t, nil
}

// parseHeading reads "# <ID> — <title>" and checks the ID.
func parseHeading(heading, id string) (string, error) {
	if heading == "" {
		return "", fmt.Errorf(`the heading "# %s — <title>" is missing`, id)
	}
	rest, ok := strings.CutPrefix(heading, "# ")
	if !ok {
		return "", fmt.Errorf(`the first line after the front matter must be the heading "# %s — <title>", got %q`, id, heading)
	}
	hid, title, ok := strings.Cut(rest, " — ")
	if !ok {
		return "", fmt.Errorf(`the heading must read "# %s — <title>", got %q`, id, heading)
	}
	if hid != id {
		return "", fmt.Errorf("the heading names %s, but the id is %s", hid, id)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("the heading has no title: %q", heading)
	}
	return title, nil
}

func isHeadingLine(line string) bool {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	return n >= 1 && n <= 6 && n < len(line) && line[n] == ' '
}

// acceptanceItems returns the list items ("- " or "* " at the start of a line, an optional
// "[ ]" or "[x]" dropped) under the "## Acceptance" heading, up to the next heading. An
// indented line right after an item continues it. Code fences are skipped.
func acceptanceItems(body string) []string {
	var items []string
	in, open, fence := false, false, false
	for pos := 0; pos < len(body); {
		line, next := nextLine(body, pos)
		pos = next
		l := strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(strings.TrimLeft(l, " \t"), "```") {
			fence = !fence
			open = false
			continue
		}
		if fence {
			continue
		}
		if isHeadingLine(l) {
			in = l == "## Acceptance"
			open = false
			continue
		}
		if !in {
			continue
		}
		if item, ok := listItem(l); ok {
			if item != "" {
				items = append(items, item)
			}
			open = item != ""
			continue
		}
		if l != "" && open && (l[0] == ' ' || l[0] == '\t') {
			items[len(items)-1] += " " + strings.TrimSpace(l)
			continue
		}
		open = false
	}
	return items
}

func listItem(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "- ")
	if !ok {
		rest, ok = strings.CutPrefix(line, "* ")
	}
	if !ok {
		return "", false
	}
	for _, box := range []string{"[ ] ", "[x] ", "[X] "} {
		if r, ok := strings.CutPrefix(rest, box); ok {
			rest = r
			break
		}
	}
	return strings.TrimSpace(rest), true
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// RenderTaskFile writes a task file: the front matter, the heading, the generated line
// "**Size** <size> · **Repository** <repo> · **Depends on** <ids>" (without the size part
// when size is empty) and then Body verbatim. ParseTaskFile gives the same Task back: Body
// is written as it is, so it starts with the blank line and the first section, and ends with
// the line break the author wants. Output is LF only and a pure function of the arguments.
func RenderTaskFile(t Task, size string) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("id: " + t.ID + "\n")
	b.WriteString("repo: " + t.Repo + "\n")
	b.WriteString("depends_on: [" + strings.Join(t.DependsOn, ", ") + "]\n")
	if t.Jira != "" {
		b.WriteString("jira: " + t.Jira + "\n")
	}
	b.WriteString("---\n")
	b.WriteString("# " + t.ID + " — " + oneLine(t.Title) + "\n\n")

	var meta []string
	if size != "" {
		meta = append(meta, "**Size** "+oneLine(size))
	}
	meta = append(meta, "**Repository** "+t.Repo)
	deps := "—"
	if len(t.DependsOn) > 0 {
		deps = strings.Join(t.DependsOn, ", ")
	}
	meta = append(meta, "**Depends on** "+deps)
	b.WriteString(strings.Join(meta, " · ") + "\n")

	b.WriteString(t.Body)
	return []byte(b.String())
}

// SetJira writes the jira key (or JiraNone) into the front matter of a task file: it replaces
// the value of an existing jira line or adds the line as the last one. Every other byte is
// kept, and the new line takes the file's line ending, so setting the same key twice gives
// the same bytes.
func SetJira(data []byte, key string) ([]byte, error) {
	if key != JiraNone && !ValidJiraKey(key) {
		return nil, fmt.Errorf(`task file: jira %q is neither a Jira key like PAY-124 nor "none"`, key)
	}
	fm, err := parseFrontMatter("task file", data)
	if err != nil {
		return nil, err
	}
	s := string(data)
	for _, e := range fm.entries {
		if e.key == "jira" {
			return []byte(s[:e.start] + "jira: " + key + s[e.end:]), nil
		}
	}
	return []byte(s[:fm.closeAt] + "jira: " + key + fm.eol + s[fm.closeAt:]), nil
}
