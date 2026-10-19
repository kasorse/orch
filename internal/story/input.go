package story

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxInputTextBytes bounds the text of a task input (SPEC §11.1).
const MaxInputTextBytes = 32 << 10

// TaskInput is the file `run --story-task` reads (SPEC §11.1): everything a child run needs of
// its story. Story and Task name the task, Attempt is 1 for the first run of it and n for the
// retry on <ID>.<n>; Branch and BaseRef are what the worktree is created on and from; BaseBranch
// is where the change finally goes (the conflict check's); Jira is the task's key or "";
// Acceptance are the review criteria; Text becomes the task description.
type TaskInput struct {
	Story      string   `json:"story"`
	Task       string   `json:"task"`
	Attempt    int      `json:"attempt"`
	Branch     string   `json:"branch"`
	BaseRef    string   `json:"base_ref"`
	BaseBranch string   `json:"base_branch"`
	Jira       string   `json:"jira"`
	Acceptance []string `json:"acceptance"`
	Text       string   `json:"text"`
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
// line, not blank and at most MaxAcceptanceRunes. field names the list in the error.
func CheckAcceptance(field string, items []string) error {
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
		case utf8.RuneCountInString(item) > MaxAcceptanceRunes:
			return fmt.Errorf("%s is %d characters, more than %d", f, utf8.RuneCountInString(item), MaxAcceptanceRunes)
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
	if err := CheckAcceptance("the task input: acceptance", in.Acceptance); err != nil {
		return TaskInput{}, err
	}
	return in, nil
}
