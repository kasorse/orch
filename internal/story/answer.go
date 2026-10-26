package story

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Bounds of the two planning answers (SPEC §7.2, §7.4). The prompts state the same numbers.
const (
	MaxSurveySummaryBytes = 4 << 10
	MaxSurveyFiles        = 30
	MaxSurveyInterfaces   = 20
	MaxSurveyConstraints  = 20
	// MaxSurveyItemBytes bounds one relevant file, interface or constraint, so that a survey
	// stays bounded as a whole when the specify prompt embeds it.
	MaxSurveyItemBytes = 1 << 10

	MaxSlugLen         = 40
	MaxTitleBytes      = 200
	MaxAcceptanceItems = 10
	MaxAcceptanceRunes = 300
	// MaxAcceptanceRunesCeiling bounds an acceptance item wherever it is read back (a task
	// input, run.json); story.max_acceptance_chars may raise the limit of a task file up to it.
	MaxAcceptanceRunesCeiling = 2000
	// MaxTaskTextBytes bounds a task's title, goal, acceptance items and notes together, so
	// the task file rendered from them stays well under MaxBodyBytes.
	MaxTaskTextBytes = 8 << 10
	// MaxSpecTextBytes bounds the five sections of the spec together.
	MaxSpecTextBytes = 16 << 10
)

// AnswerError is a planning call's answer that breaks a rule of SPEC §7.2 or §7.4. Field
// names the offending field the way the answer spells it (slug, tasks[1].repo,
// relevant_files[3]); it is "" for the answer as a whole (not a JSON object, trailing data).
type AnswerError struct {
	Field string
	Msg   string
}

func (e *AnswerError) Error() string {
	if e.Field == "" {
		return e.Msg
	}
	return e.Field + ": " + e.Msg
}

func answerErr(field, format string, args ...any) *AnswerError {
	return &AnswerError{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// decodeStrict decodes obj, one JSON object and nothing after it, into v. A key v has no
// field for is an error, and so is a value of the wrong type; both name the field.
func decodeStrict(obj []byte, v any) error {
	trimmed := bytes.TrimSpace(obj)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return answerErr("", "the answer is not a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return answerErr(typeErr.Field, "want a JSON %s, got %s", jsonKind(typeErr.Type), typeErr.Value)
		}
		if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			if unquoted, uerr := strconv.Unquote(name); uerr == nil {
				name = unquoted
			}
			return answerErr(name, "not a field of the answer")
		}
		return answerErr("", "the answer is not valid JSON: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return answerErr("", "the answer has data after its JSON object")
	}
	return nil
}

func jsonKind(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Struct, reflect.Map:
		return "object"
	case reflect.Bool:
		return "boolean"
	}
	return "number"
}

// SurveyAnswer is the answer of one survey call (SPEC §7.2): what in one repository relates to
// the request. Paths are displayed only, never opened by Go.
type SurveyAnswer struct {
	Summary       string   `json:"summary"`
	RelevantFiles []string `json:"relevant_files"`
	Interfaces    []string `json:"interfaces"`
	Constraints   []string `json:"constraints"`
}

// ParseSurvey decodes a survey answer strictly: no other key, a summary of at most
// MaxSurveySummaryBytes, at most MaxSurveyFiles clean relative paths, at most
// MaxSurveyInterfaces interfaces and MaxSurveyConstraints constraints, each entry at most
// MaxSurveyItemBytes. Any violation is an *AnswerError naming the field.
func ParseSurvey(obj []byte) (SurveyAnswer, error) {
	var a SurveyAnswer
	if err := decodeStrict(obj, &a); err != nil {
		return SurveyAnswer{}, err
	}
	switch {
	case strings.TrimSpace(a.Summary) == "":
		return SurveyAnswer{}, answerErr("summary", "is empty")
	case len(a.Summary) > MaxSurveySummaryBytes:
		return SurveyAnswer{}, answerErr("summary", "is %d bytes, over the %d-byte limit", len(a.Summary), MaxSurveySummaryBytes)
	}
	if err := checkCount("relevant_files", len(a.RelevantFiles), MaxSurveyFiles); err != nil {
		return SurveyAnswer{}, err
	}
	for i, p := range a.RelevantFiles {
		if problem := relPathProblem(p); problem != "" {
			return SurveyAnswer{}, answerErr(fmt.Sprintf("relevant_files[%d]", i), "%s", problem)
		}
	}
	for _, list := range []struct {
		name  string
		items []string
		limit int
	}{
		{"interfaces", a.Interfaces, MaxSurveyInterfaces},
		{"constraints", a.Constraints, MaxSurveyConstraints},
	} {
		if err := checkCount(list.name, len(list.items), list.limit); err != nil {
			return SurveyAnswer{}, err
		}
		for i, item := range list.items {
			field := fmt.Sprintf("%s[%d]", list.name, i)
			switch {
			case strings.TrimSpace(item) == "":
				return SurveyAnswer{}, answerErr(field, "is empty")
			case len(item) > MaxSurveyItemBytes:
				return SurveyAnswer{}, answerErr(field, "is %d bytes, over the %d-byte limit", len(item), MaxSurveyItemBytes)
			}
		}
	}
	return a, nil
}

func checkCount(field string, n, limit int) error {
	if n > limit {
		return answerErr(field, "has %d entries, more than %d", n, limit)
	}
	return nil
}

// relPathProblem says what keeps p from being a clean, relative, slash-separated path, or "".
func relPathProblem(p string) string {
	switch {
	case p == "":
		return "is empty"
	case len(p) > MaxSurveyItemBytes:
		return fmt.Sprintf("is %d bytes, over the %d-byte limit", len(p), MaxSurveyItemBytes)
	case strings.ContainsFunc(p, unicode.IsControl):
		return fmt.Sprintf("%q holds a control character", p)
	case strings.HasPrefix(p, "/"):
		return fmt.Sprintf("%q is absolute; want a path relative to the repository root", p)
	case strings.Contains(p, `\`):
		return fmt.Sprintf("%q is not slash-separated", p)
	case p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") || strings.HasSuffix(p, "/.."):
		return fmt.Sprintf("%q names the repository's root or a path outside it", p)
	case path.Clean(p) != p:
		return fmt.Sprintf("%q is not clean; want %q", p, path.Clean(p))
	}
	return ""
}

// SpecifyAnswer is the answer of the specify call (SPEC §7.3): the spec and its tasks. The
// tasks are TaskAnswers, because Tasks is the method that turns them into task files' Tasks.
type SpecifyAnswer struct {
	Slug        string       `json:"slug"`
	Prefix      string       `json:"prefix"`
	Title       string       `json:"title"`
	Spec        SpecText     `json:"spec"`
	TaskAnswers []AnswerTask `json:"tasks"`
}

// SpecText is the text of SPEC.md, by section.
type SpecText struct {
	Problem    string `json:"problem"`
	Approach   string `json:"approach"`
	Contracts  string `json:"contracts"`
	OutOfScope string `json:"out_of_scope"`
	Risks      string `json:"risks"`
}

// AnswerTask is one task of a specify answer. Size has no place in Task (it lives in the
// generated "**Size**" line only), so whoever renders the task file takes it from here.
type AnswerTask struct {
	ID         string   `json:"id"`
	Repo       string   `json:"repo"`
	Title      string   `json:"title"`
	Size       string   `json:"size"`
	DependsOn  []string `json:"depends_on"`
	Goal       string   `json:"goal"`
	Acceptance []string `json:"acceptance"`
	Notes      string   `json:"notes"`
}

// ValidSlug reports whether s names a spec's directory: ^[a-z0-9][a-z0-9-]{1,39}$ (SPEC §7.4).
func ValidSlug(s string) bool {
	if len(s) < 2 || len(s) > MaxSlugLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := isDigit(c) || (c >= 'a' && c <= 'z')
		if !alnum && (i == 0 || c != '-') {
			return false
		}
	}
	return true
}

// ParseSpecify decodes a specify answer strictly and checks it against SPEC §7.4: the slug's
// shape, and that no spec of that slug exists yet (specDirExists; nil does not ask); the
// prefix and the title; the spec's size; every task's ID (the prefix and two digits, unique),
// repo (one of repos, the story's services), title, size, depends_on, goal and notes,
// acceptance items and text size; at least one and at most maxTasks tasks; then the graph
// rules of §9 (Validate, and Base for every task). Any violation is an *AnswerError naming
// the field. The field rules come first, so that a foreign repository or a wrong prefix is
// reported by its field rather than by the graph.
func ParseSpecify(obj []byte, repos []string, maxTasks int, specDirExists func(slug string) bool) (SpecifyAnswer, error) {
	var a SpecifyAnswer
	if err := decodeStrict(obj, &a); err != nil {
		return SpecifyAnswer{}, err
	}
	if err := a.check(repos, maxTasks, specDirExists); err != nil {
		return SpecifyAnswer{}, err
	}
	return a, nil
}

func (a SpecifyAnswer) check(repos []string, maxTasks int, specDirExists func(string) bool) error {
	if !ValidSlug(a.Slug) {
		return answerErr("slug", "%q: want 2 to %d lowercase letters, digits and \"-\", starting with a letter or digit", a.Slug, MaxSlugLen)
	}
	if specDirExists != nil && specDirExists(a.Slug) {
		return answerErr("slug", "a spec named %q exists already", a.Slug)
	}
	if len(a.Prefix) != 2 || !isUpper(a.Prefix[0]) || !isUpper(a.Prefix[1]) {
		return answerErr("prefix", "%q: want two capital letters", a.Prefix)
	}
	if err := checkTitle("title", a.Title); err != nil {
		return err
	}
	s := a.Spec
	if n := len(s.Problem) + len(s.Approach) + len(s.Contracts) + len(s.OutOfScope) + len(s.Risks); n > MaxSpecTextBytes {
		return answerErr("spec", "is %d bytes, over the %d-byte limit", n, MaxSpecTextBytes)
	}

	switch {
	case len(a.TaskAnswers) == 0:
		return answerErr("tasks", "is empty")
	case len(a.TaskAnswers) > maxTasks:
		return answerErr("tasks", "has %d tasks, more than story.max_tasks (%d)", len(a.TaskAnswers), maxTasks)
	}
	index := make(map[string]int, len(a.TaskAnswers))
	for i, t := range a.TaskAnswers {
		field := fmt.Sprintf("tasks[%d].id", i)
		switch {
		case !ValidID(t.ID):
			return answerErr(field, "%q: want the prefix and two digits, like %s01", t.ID, a.Prefix)
		case t.ID[:2] != a.Prefix:
			return answerErr(field, "%s does not start with the prefix %s", t.ID, a.Prefix)
		}
		if j, dup := index[t.ID]; dup {
			return answerErr(field, "%s is also the ID of tasks[%d]", t.ID, j)
		}
		index[t.ID] = i
	}
	services := slices.Sorted(slices.Values(repos))
	for i, t := range a.TaskAnswers {
		if err := t.check(i, services, index); err != nil {
			return err
		}
	}

	if err := Validate(a.Tasks(), repos, maxTasks); err != nil {
		return answerErr("tasks", "%v", err)
	}
	return nil
}

func (t AnswerTask) check(i int, services []string, index map[string]int) error {
	field := func(name string) string { return fmt.Sprintf("tasks[%d].%s", i, name) }
	if !slices.Contains(services, t.Repo) {
		return answerErr(field("repo"), "%q is not a service of the story (services: %s)", t.Repo, strings.Join(services, ", "))
	}
	if err := checkTitle(field("title"), t.Title); err != nil {
		return err
	}
	switch t.Size {
	case "S", "M", "L":
	default:
		return answerErr(field("size"), "%q: want S, M or L", t.Size)
	}
	seen := map[string]bool{}
	for _, dep := range t.DependsOn {
		_, known := index[dep]
		switch {
		case dep == t.ID:
			return answerErr(field("depends_on"), "%s depends on itself", t.ID)
		case seen[dep]:
			return answerErr(field("depends_on"), "lists %s twice", dep)
		case !known:
			return answerErr(field("depends_on"), "%q is not a task of the answer", dep)
		}
		seen[dep] = true
	}
	if strings.TrimSpace(t.Goal) == "" {
		return answerErr(field("goal"), "is empty")
	}
	for _, s := range []struct{ name, text string }{{"goal", t.Goal}, {"notes", t.Notes}} {
		if problem := sectionProblem(s.text); problem != "" {
			return answerErr(field(s.name), "%s", problem)
		}
	}
	if len(t.Acceptance) > MaxAcceptanceItems {
		return answerErr(field("acceptance"), "has %d items, more than %d", len(t.Acceptance), MaxAcceptanceItems)
	}
	n := len(t.Title) + len(t.Goal) + len(t.Notes)
	for j, item := range t.Acceptance {
		f := fmt.Sprintf("%s[%d]", field("acceptance"), j)
		switch {
		case acceptanceItem(item) == "":
			return answerErr(f, "is empty")
		case utf8.RuneCountInString(item) > MaxAcceptanceRunes:
			return answerErr(f, "is %d characters, more than %d", utf8.RuneCountInString(item), MaxAcceptanceRunes)
		}
		n += len(item)
	}
	if n > MaxTaskTextBytes {
		return answerErr(fmt.Sprintf("tasks[%d]", i), "the task's text is %d bytes, over the %d-byte limit", n, MaxTaskTextBytes)
	}
	return nil
}

func checkTitle(field, title string) error {
	switch {
	case strings.TrimSpace(title) == "":
		return answerErr(field, "is empty")
	case len(title) > MaxTitleBytes:
		return answerErr(field, "is %d bytes, over the %d-byte limit", len(title), MaxTitleBytes)
	case strings.ContainsAny(title, "\r\n"):
		return answerErr(field, "is more than one line")
	}
	return nil
}

// sectionProblem says why text cannot be a section of a task file, or "". A heading of level 1
// or 2 would end the section, "## As built" (which counts even inside a code fence) would end
// the task's text, and an unclosed code fence would hide the "## Acceptance" list after it from
// ParseTaskFile.
func sectionProblem(text string) string {
	fences := 0
	for pos := 0; pos < len(text); {
		line, next := nextLine(text, pos)
		pos = next
		line = trimCR(line)
		if strings.HasPrefix(line, asBuiltHeading) {
			return fmt.Sprintf("holds the line %q, which Go reserves for the As built record", line)
		}
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "```") {
			fences++
			continue
		}
		if fences%2 == 0 && (strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ")) {
			return fmt.Sprintf("holds the heading %q; use ### or lower inside a section", line)
		}
	}
	if fences%2 != 0 {
		return "has a code fence that is not closed"
	}
	return ""
}

// acceptanceItem is item on one line as ParseTaskFile reads it back from the "- " line Tasks
// writes, "" when nothing is left of it.
func acceptanceItem(item string) string {
	got, _ := listItem("- " + oneLine(item))
	return got
}

// Tasks are the answer's tasks as their task files hold them, for Validate and for the writer
// of the spec: Title on one line; Body the "## Goal", "## Acceptance" and "## Notes" sections in
// the format of SPEC §8; Acceptance the items as ParseTaskFile reads them back from that Body;
// Jira empty. RenderTaskFile of such a task parses back to the same Task.
func (a SpecifyAnswer) Tasks() []Task {
	out := make([]Task, 0, len(a.TaskAnswers))
	for _, t := range a.TaskAnswers {
		var items []string
		for _, item := range t.Acceptance {
			items = append(items, acceptanceItem(item))
		}
		var deps []string
		if len(t.DependsOn) > 0 {
			deps = slices.Clone(t.DependsOn)
		}
		out = append(out, Task{
			ID:         t.ID,
			Repo:       t.Repo,
			DependsOn:  deps,
			Title:      oneLine(t.Title),
			Body:       taskBody(t.Goal, items, t.Notes),
			Acceptance: items,
		})
	}
	return out
}

func taskBody(goal string, acceptance []string, notes string) string {
	var b strings.Builder
	b.WriteString("\n## Goal\n\n" + strings.TrimSpace(goal) + "\n")
	if len(acceptance) > 0 {
		b.WriteString("\n## Acceptance\n\n")
		for _, item := range acceptance {
			b.WriteString("- " + item + "\n")
		}
	}
	if notes = strings.TrimSpace(notes); notes != "" {
		b.WriteString("\n## Notes\n\n" + notes + "\n")
	}
	return b.String()
}
