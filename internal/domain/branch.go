package domain

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// The branch name of a worktree run (specs/delivery/SPEC.md §4.3): proposed from the repository's
// instructions, else rendered from worktree.branch.template, else orchestrator/<run-id>. Go checks
// every name, whichever source it came from.

// Bounds of a branch name and of the {slug} placeholder.
const (
	MaxBranchNameBytes = 100
	maxSlugBytes       = 40
)

// StoryBranchPrefix is the namespace of a story task's fallback branch (specs/story/SPEC.md
// §11.1); a name from the instructions or the template is never under it, so a rule name cannot
// collide with a story branch.
const StoryBranchPrefix = "story/"

// Values of BranchNaming.Source.
const (
	BranchSourceInstructions = "instructions"
	BranchSourceTemplate     = "template"
	BranchSourceDefault      = "default"
	// BranchSourceExisting is a revise update's: the merge request's own branch (specs/revise/
	// SPEC.md §6.1), named by nobody in this run.
	BranchSourceExisting = "existing"
)

// BranchNaming records how a worktree run's branch was named: run.json: branch_naming. Absent for
// an inplace run. A story task's is recorded as any run's, its default being the fallback its task
// input gave (specs/delivery/SPEC.md §9.1).
type BranchNaming struct {
	// Source is where the name in worktree.branch came from.
	Source string `json:"source"`
	// Proposed is the name the instructions call answered, also when it was not used.
	Proposed string `json:"proposed,omitempty"`
	// Title is the merge request title pattern the instructions call answered, kept only when
	// CheckTitleTemplate accepted it; the merge request's title is rendered from it.
	Title string `json:"title,omitempty"`
	// Rejected is why a source before Source gave no name, on one line.
	Rejected string `json:"rejected,omitempty"`
	// Usage is what the instructions call cost, when the CLI reported it; the outcome counts it.
	Usage *Usage `json:"usage,omitempty"`
	// Call is that call's report; nil when none was made.
	Call *CallReport `json:"call,omitempty"`
}

// Bounds on what a BranchNaming may hold.
const (
	maxProposedBytes = 255
	maxRejectedBytes = 500
)

// Validate checks a recorded branch naming read back from disk.
func (b BranchNaming) Validate() error {
	switch b.Source {
	case BranchSourceInstructions, BranchSourceTemplate, BranchSourceDefault, BranchSourceExisting:
	default:
		return fmt.Errorf("source %q is not instructions, template, default or existing", b.Source)
	}
	if len(b.Proposed) > maxProposedBytes || strings.IndexFunc(b.Proposed, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return fmt.Errorf("proposed is not printable text of at most %d bytes", maxProposedBytes)
	}
	if len(b.Rejected) > maxRejectedBytes || strings.ContainsAny(b.Rejected, "\r\n") {
		return fmt.Errorf("rejected is not one line of at most %d bytes", maxRejectedBytes)
	}
	if b.Title != "" {
		if err := CheckTitleTemplate(b.Title); err != nil {
			return fmt.Errorf("title: %w", err)
		}
	}
	if b.Source == BranchSourceInstructions && b.Proposed == "" {
		return fmt.Errorf("source instructions without a proposed name")
	}
	if b.Usage != nil {
		if err := b.Usage.Validate(); err != nil {
			return fmt.Errorf("usage: %w", err)
		}
	}
	if b.Call != nil {
		if err := b.Call.Validate(); err != nil {
			return fmt.Errorf("call: %w", err)
		}
	}
	return nil
}

// MaxTitleTemplateBytes bounds a merge request title pattern.
const MaxTitleTemplateBytes = 200

// CheckTitleTemplate says why t cannot be a merge request title pattern, or returns nil: one line
// of printable text of at most MaxTitleTemplateBytes, with {summary}, no placeholder but {key} and
// {summary}, and no @, so the literal text mentions nobody.
func CheckTitleTemplate(t string) error {
	switch {
	case len(t) > MaxTitleTemplateBytes || strings.IndexFunc(t, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0:
		return fmt.Errorf("it is not one line of printable text of at most %d bytes", MaxTitleTemplateBytes)
	case !strings.Contains(t, "{summary}"):
		return fmt.Errorf("it has no {summary}")
	case strings.ContainsAny(strings.NewReplacer("{key}", "", "{summary}", "").Replace(t), "{}"):
		return fmt.Errorf("it has a placeholder other than {key} and {summary}")
	case strings.Contains(t, "@"):
		return fmt.Errorf("it has an @")
	}
	return nil
}

// RenderTitleTemplate is the title pattern t with {key} and {summary} filled in.
func RenderTitleTemplate(t, key, summary string) string {
	return strings.NewReplacer("{key}", key, "{summary}", summary).Replace(t)
}

// CheckBranchName says why name cannot be the branch of a worktree run, or returns nil. It is
// the part of the rules that needs no git: only [A-Za-z0-9._/-], at most MaxBranchNameBytes bytes,
// no leading "-" or "/", no "//" or "..", no path component that starts with "." or ends with
// ".lock", no trailing "/" or ".", not HEAD and not under story/. Git's own check-ref-format, the
// existing branches and the base branch are checked where there is a repository.
func CheckBranchName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("the name is empty")
	case len(name) > MaxBranchNameBytes:
		return fmt.Errorf("the name is %d bytes, at most %d", len(name), MaxBranchNameBytes)
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("the name starts with -")
	case strings.HasPrefix(name, "/"):
		return fmt.Errorf("the name starts with /")
	case strings.HasSuffix(name, "/"):
		return fmt.Errorf("the name ends with /")
	case strings.HasSuffix(name, "."):
		return fmt.Errorf("the name ends with .")
	case strings.Contains(name, ".."):
		return fmt.Errorf("the name contains ..")
	case strings.Contains(name, "//"):
		return fmt.Errorf("the name contains //")
	}
	for _, c := range name {
		if !branchNameChar(c) {
			return fmt.Errorf("the name has the character %q, only letters, digits, . _ / - are allowed", c)
		}
	}
	for _, part := range strings.Split(name, "/") {
		switch {
		case strings.HasPrefix(part, "."):
			return fmt.Errorf("a part of the name starts with .")
		case strings.HasSuffix(part, ".lock"):
			return fmt.Errorf("a part of the name ends with .lock")
		}
	}
	switch {
	case name == "HEAD":
		return fmt.Errorf("the name is HEAD")
	case strings.HasPrefix(name, StoryBranchPrefix):
		return fmt.Errorf("the name is under %s, which is for story tasks", StoryBranchPrefix)
	}
	return nil
}

// ValidBranchName reports whether CheckBranchName accepts name.
func ValidBranchName(name string) bool { return CheckBranchName(name) == nil }

func branchNameChar(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '/' || c == '-'
}

// BranchVars are the values of worktree.branch.template's placeholders: {id}, {key}, {slug} and
// {date}. An empty value makes its placeholder disappear (RenderBranchTemplate).
type BranchVars struct {
	ID   string
	Key  string
	Slug string
	Date string
}

func (v BranchVars) value(name string) (string, bool) {
	switch name {
	case "id":
		return v.ID, true
	case "key":
		return v.Key, true
	case "slug":
		return v.Slug, true
	case "date":
		return v.Date, true
	}
	return "", false
}

// placeholderRE finds {...} in a template; whether the name is one of the four is the next check.
var placeholderRE = regexp.MustCompile(`\{[^{}]*\}`)

// CheckBranchTemplate refuses a template with a placeholder other than {id}, {key}, {slug} or
// {date}, and a brace that is not part of a placeholder. {type} is not offered: the commit type
// exists only after implement, and the name is fixed before it.
func CheckBranchTemplate(tmpl string) error {
	for _, m := range placeholderRE.FindAllString(tmpl, -1) {
		if _, ok := (BranchVars{}).value(m[1 : len(m)-1]); !ok {
			return fmt.Errorf("unknown placeholder %s (the placeholders are {id}, {key}, {slug} and {date})", m)
		}
	}
	if strings.ContainsAny(placeholderRE.ReplaceAllString(tmpl, ""), "{}") {
		return fmt.Errorf("a brace that is not part of a placeholder (the placeholders are {id}, {key}, {slug} and {date})")
	}
	return nil
}

// RenderBranchTemplate fills tmpl's placeholders from vars. A placeholder whose value is empty
// takes the separator after it ("-", "_" or "/") with it, or, at the end of the template, the one
// before it: feature/{key}-{slug} is feature/PAY-12-use-plus-in-add with a key and
// feature/use-plus-in-add without. A placeholder CheckBranchTemplate would refuse is left as it is.
func RenderBranchTemplate(tmpl string, vars BranchVars) string {
	var b strings.Builder
	last := 0
	for _, loc := range placeholderRE.FindAllStringIndex(tmpl, -1) {
		b.WriteString(tmpl[last:loc[0]])
		last = loc[1]
		value, ok := vars.value(tmpl[loc[0]+1 : loc[1]-1])
		switch {
		case !ok:
			b.WriteString(tmpl[loc[0]:loc[1]])
		case value != "":
			b.WriteString(value)
		case last < len(tmpl) && isBranchSeparator(tmpl[last]):
			last++
		case last == len(tmpl):
			if s := b.String(); s != "" && isBranchSeparator(s[len(s)-1]) {
				b.Reset()
				b.WriteString(s[:len(s)-1])
			}
		}
	}
	b.WriteString(tmpl[last:])
	return b.String()
}

func isBranchSeparator(c byte) bool { return c == '-' || c == '_' || c == '/' }

// cyrillic transliterates the letters the table knows (Russian and the Ukrainian additions), lower
// case, one fixed table so the same title always gives the same slug. A hard or soft sign maps to
// nothing, so it drops out of a word without splitting it.
var cyrillic = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh", 'з': "z", 'и': "i",
	'й': "y", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t",
	'у': "u", 'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch", 'ъ': "", 'ы': "y", 'ь': "",
	'э': "e", 'ю': "yu", 'я': "ya", 'і': "i", 'ї': "yi", 'є': "ye", 'ґ': "g",
}

// Slug is the {slug} of a branch template: text with Cyrillic transliterated, lower-cased, every
// run of characters outside [a-z0-9] turned into one "-", trimmed of "-", and, when longer than 40
// bytes, cut at a "-" (or at 40 bytes when the first word is longer). "" when nothing is left.
func Slug(text string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(text) {
		part, ok := cyrillic[r]
		if !ok {
			part = string(r)
		}
		for _, c := range part {
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
				if dash && b.Len() > 0 {
					b.WriteByte('-')
				}
				dash = false
				b.WriteRune(c)
			} else {
				dash = true
			}
		}
	}
	s := b.String()
	if len(s) > maxSlugBytes {
		cut := s[:maxSlugBytes]
		if s[maxSlugBytes] != '-' {
			if i := strings.LastIndexByte(cut, '-'); i > 0 {
				cut = cut[:i]
			}
		}
		s = strings.Trim(cut, "-")
	}
	return s
}
