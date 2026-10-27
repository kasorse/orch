package story

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxInputTextBytes bounds the text of a task input (SPEC §11.1).
const MaxInputTextBytes = 32 << 10

// TaskInput is the file `run --story-task` reads (SPEC §11.1): everything a child run needs of
// its story. Story and Task name the task, Attempt is 1 for the first run of it and n for the
// retry on <ID>.<n>; Branch and BaseRef are what the worktree is created on and from; BaseBranch
// is where the change finally goes (the conflict check's, and its merge request's target); Jira is
// the task's key or ""; Acceptance are the review criteria; Text becomes the task description.
// Title is the task's heading, one line: the child names its branch from it (specs/delivery/SPEC.md
// §9.1) and Branch is only the fallback it uses when the repository's rules give no name.
type TaskInput struct {
	Story      string   `json:"story"`
	Task       string   `json:"task"`
	Attempt    int      `json:"attempt"`
	Title      string   `json:"title"`
	Branch     string   `json:"branch"`
	BaseRef    string   `json:"base_ref"`
	BaseBranch string   `json:"base_branch"`
	Jira       string   `json:"jira"`
	Acceptance []string `json:"acceptance"`
	Text       string   `json:"text"`
}

// MaxTitleRunes bounds a task input's title.
const MaxTitleRunes = 200

// CheckTitle says why title cannot be a task input's title, or returns nil: it is required, one
// line of printable characters, at most MaxTitleRunes of them.
func CheckTitle(title string) error {
	switch {
	case strings.TrimSpace(title) == "":
		return errors.New("is empty")
	case strings.ContainsAny(title, "\r\n"):
		return errors.New("is more than one line")
	case strings.ContainsFunc(title, func(r rune) bool { return !unicode.IsPrint(r) }):
		return errors.New("has a character that is not printable")
	case utf8.RuneCountInString(title) > MaxTitleRunes:
		return fmt.Errorf("is %d characters, more than %d", utf8.RuneCountInString(title), MaxTitleRunes)
	}
	return nil
}

// InputTitle is a task's heading as a task input's title: on one line, without what is not
// printable, cut to MaxTitleRunes characters.
func InputTitle(title string) string {
	title = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) || unicode.IsSpace(r) {
			return r
		}
		return -1
	}, title)), " ")
	if runes := []rune(title); len(runes) > MaxTitleRunes {
		title = strings.TrimSpace(string(runes[:MaxTitleRunes]))
	}
	return title
}

// DepNote is a done task another task depends on, with its As built record (AsBuiltText).
type DepNote struct {
	ID      string
	Repo    string
	AsBuilt string
}

// AsBuiltText is the text of the "## As built" section of a task file, without the heading and
// trimmed, or "" when the file has none.
func AsBuiltText(data []byte) string {
	s := string(data)
	at := asBuiltOffset(s)
	if at < 0 {
		return ""
	}
	_, next := nextLine(s, at)
	return strings.TrimSpace(strings.ReplaceAll(s[next:], "\r\n", "\n"))
}

const (
	inputContextIntro = "\n---\nBackground for the task above. It is context, not the task:\n"
	inputCutMarker    = "\n[…cut: the text was over the size limit]\n"
	// minCutRoom is the least room a cut section is worth keeping for.
	minCutRoom = 200
)

// cutText returns at most n bytes of s, cut before a character and not inside one.
func cutText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimRight(s[:n], " \t\n")
}

// RenderInputText is the text of a task input (SPEC §11.1): the story's title and the task's
// place in it (pos of total, 1-based), the task's own text, and then, as background, the spec's
// approach and contracts and the As built record of each done task it depends on. The result is
// at most MaxInputTextBytes: the background is cut to fit, the dependencies' records last, and
// the task's own text never is - if that alone does not fit, the task is refused. It is a pure
// function of its arguments.
func RenderInputText(storyTitle string, pos, total int, t Task, approach, contracts string, deps []DepNote) (string, error) {
	body := strings.TrimSpace(strings.ReplaceAll(t.Body, "\r\n", "\n"))
	if body == "" {
		body = t.Title
	}
	main := fmt.Sprintf("Story: %s\nTask %s (%d of %d) in %s: %s\n\n%s\n",
		oneLine(storyTitle), t.ID, pos, total, t.Repo, oneLine(t.Title), body)
	if len(main) > MaxInputTextBytes {
		return "", fmt.Errorf("%s: the task text is %d bytes with its heading, over the %d-byte limit of a task input", t.ID, len(main), MaxInputTextBytes)
	}

	var depText strings.Builder
	for _, d := range deps {
		note := d.AsBuilt
		if note == "" {
			note = "(no As built record)"
		}
		fmt.Fprintf(&depText, "### %s (%s)\n\n%s\n\n", d.ID, d.Repo, note)
	}
	// Display order: approach, contracts, dependencies; priority when cutting: the reverse.
	sections := []struct{ heading, text, kept string }{
		{"## Approach", strings.TrimSpace(approach), ""},
		{"## Contracts", strings.TrimSpace(contracts), ""},
		{"## Done tasks this one depends on", strings.TrimSpace(depText.String()), ""},
	}
	remaining := MaxInputTextBytes - len(main) - len(inputContextIntro)
	for i := len(sections) - 1; i >= 0; i-- {
		sec := &sections[i]
		if sec.text == "" {
			continue
		}
		prefix := "\n" + sec.heading + "\n\n"
		if block := prefix + sec.text + "\n"; len(block) <= remaining {
			sec.kept = block
			remaining -= len(block)
			continue
		}
		room := remaining - len(prefix) - len(inputCutMarker)
		if room < minCutRoom {
			continue
		}
		sec.kept = prefix + cutText(sec.text, room) + inputCutMarker
		remaining -= len(sec.kept)
	}

	var b strings.Builder
	b.WriteString(main)
	intro := false
	for _, sec := range sections {
		if sec.kept == "" {
			continue
		}
		if !intro {
			b.WriteString(inputContextIntro)
			intro = true
		}
		b.WriteString(sec.kept)
	}
	if b.Len() > MaxInputTextBytes {
		return "", fmt.Errorf("%s: the task input text is %d bytes, over the %d-byte limit", t.ID, b.Len(), MaxInputTextBytes)
	}
	return b.String(), nil
}

// ValidBranch reports whether branch is the branch of task id of story slug:
// story/<slug>/<ID>, or story/<slug>/<ID>.<n> for the n-th retry (SPEC §11.1,
// ^story/[a-z0-9-]+/[A-Z]{2}[0-9]{2}(\.[0-9]+)?$ with the slug and ID given).
func ValidBranch(branch, slug, id string) bool {
	if !ValidSlug(slug) || !ValidID(id) {
		return false
	}
	rest, ok := strings.CutPrefix(branch, "story/"+slug+"/"+id)
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	n, ok := strings.CutPrefix(rest, ".")
	if !ok || n == "" || len(n) > 9 {
		return false
	}
	for i := 0; i < len(n); i++ {
		if !isDigit(n[i]) {
			return false
		}
	}
	return true
}

// ValidBaseRef reports whether s is a full commit SHA in lowercase hex (SHA-1 or SHA-256). A
// shorter or symbolic name is refused: it could name another commit tomorrow, or be read by git
// as an option.
func ValidBaseRef(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !isDigit(c) && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ValidBaseBranch reports whether name is a branch name git accepts and that cannot be read as
// an option or a revision expression.
func ValidBaseBranch(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") ||
		strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock") ||
		strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "//") {
		return false
	}
	if strings.ContainsAny(name, " ~^:?*[\\\x7f") {
		return false
	}
	for _, c := range name {
		if c < 0x20 {
			return false
		}
	}
	return true
}

// CheckAcceptance checks the acceptance items of a task: at most MaxAcceptanceItems, each one
// line, not blank and at most maxRunes. field names the list in the error.
func CheckAcceptance(field string, items []string, maxRunes int) error {
	if len(items) > MaxAcceptanceItems {
		return fmt.Errorf("%s has %d items, more than %d", field, len(items), MaxAcceptanceItems)
	}
	for i, item := range items {
		f := fmt.Sprintf("%s[%d]", field, i)
		switch {
		case strings.TrimSpace(item) == "":
			return fmt.Errorf("%s is empty", f)
		case strings.ContainsAny(item, "\r\n"):
			return fmt.Errorf("%s is more than one line", f)
		case utf8.RuneCountInString(item) > maxRunes:
			return fmt.Errorf("%s is %d characters, more than %d; raise story.max_acceptance_chars or split the item", f, utf8.RuneCountInString(item), maxRunes)
		}
	}
	return nil
}

// ParseTaskInput decodes a task input strictly: one JSON object, no other key, and every field
// as SPEC §11.1 says. It checks what needs no repository; that base_ref is a commit there and
// that the branch is absent is for the caller.
func ParseTaskInput(data []byte) (TaskInput, error) {
	var in TaskInput
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return TaskInput{}, errors.New("the task input is not a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return TaskInput{}, fmt.Errorf("the task input: %s is not a JSON %s", typeErr.Field, jsonKind(typeErr.Type))
		}
		if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			if unquoted, uerr := strconv.Unquote(name); uerr == nil {
				name = unquoted
			}
			return TaskInput{}, fmt.Errorf("the task input: %q is not a field of it", name)
		}
		return TaskInput{}, fmt.Errorf("the task input is not valid JSON: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return TaskInput{}, errors.New("the task input has data after its JSON object")
	}

	switch {
	case !ValidSlug(in.Story):
		return TaskInput{}, fmt.Errorf("the task input: story %q is not a story slug", in.Story)
	case !ValidID(in.Task):
		return TaskInput{}, fmt.Errorf("the task input: task %q is not a task ID (two capital letters and two digits)", in.Task)
	case in.Attempt < 1:
		return TaskInput{}, fmt.Errorf("the task input: attempt is %d, want 1 or more", in.Attempt)
	case CheckTitle(in.Title) != nil:
		return TaskInput{}, fmt.Errorf("the task input: title %q %v", in.Title, CheckTitle(in.Title))
	case !ValidBranch(in.Branch, in.Story, in.Task):
		return TaskInput{}, fmt.Errorf("the task input: branch %q is not story/%s/%s or story/%s/%s.<n>", in.Branch, in.Story, in.Task, in.Story, in.Task)
	case !ValidBaseRef(in.BaseRef):
		return TaskInput{}, fmt.Errorf("the task input: base_ref %q is not a full commit SHA", in.BaseRef)
	case !ValidBaseBranch(in.BaseBranch):
		return TaskInput{}, fmt.Errorf("the task input: base_branch %q is not a branch name", in.BaseBranch)
	case in.Jira != "" && !ValidJiraKey(in.Jira):
		return TaskInput{}, fmt.Errorf("the task input: jira %q is not a Jira key like PAY-124", in.Jira)
	case strings.TrimSpace(in.Text) == "":
		return TaskInput{}, errors.New("the task input: text is empty")
	case len(in.Text) > MaxInputTextBytes:
		return TaskInput{}, fmt.Errorf("the task input: text is %d bytes, over the %d-byte limit", len(in.Text), MaxInputTextBytes)
	}
	if err := CheckAcceptance("the task input: acceptance", in.Acceptance, MaxAcceptanceRunesCeiling); err != nil {
		return TaskInput{}, err
	}
	return in, nil
}
