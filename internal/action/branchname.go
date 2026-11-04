package action

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/provider"
)

// The branch-name call's prompt and answer (specs/delivery/SPEC.md §4.3). Like the translator and
// the clarify step it is a call outside a run's steps: no tools, so "do not read the code" is
// enforced by the authority (domain.AuthorityNone). What it knows of the repository is the
// instruction files Go read from base_ref, which come in fenced; whatever it answers only passes
// Go's name checks or is dropped.

// BranchInstructionFiles are the files, at the repository root of base_ref, whose rules about
// branch names the call is asked about, in the order they are shown.
var BranchInstructionFiles = []string{"CLAUDE.md", "AGENTS.md", "CONTRIBUTING.md"}

// Bounds of the call's input: the three files together, and the task's first line.
const (
	MaxBranchInstructionBytes = 32 << 10
	maxBranchTaskRunes        = 200
)

const branchNameIntro = `Propose the git branch name and the merge request title pattern for the following software ` +
	`engineering task, following only what the repository's own instructions say about branch names and merge request titles. You have no tools and cannot read the code: all you know of the ` +
	`repository is its instruction files, given after the task. Respond with a single JSON object, no markdown fences, no ` +
	`prose before or after it, matching exactly this shape:`

const branchNameShape = `{"branch": "...", "title": "..."}`

const branchNameRules = `"branch" is the complete branch name: letters, digits and the characters . _ - / only, no spaces, ` +
	`at most 100 bytes, without a remote name such as origin/. Apply the instructions' rule to the task, the Jira key, the ` +
	`run ID and the date given below. When the instructions set no rule for branch names, "branch" is "": never ` +
	`invent a rule of your own. "title" is the instructions' rule for merge request titles as a pattern: {key} ` +
	`stands for the Jira key and {summary} for the one-line summary of the change, written later; everything else is ` +
	`literal text, one line, at most 200 bytes ("{key}: {summary}" for a rule whose example is "PAY-12: Use + in Add"). ` +
	`When the instructions set no rule for merge request titles, "title" is "".`

// BranchFile is one instruction file as the call is given it.
type BranchFile struct {
	Path string
	Text string
}

// BoundBranchFiles keeps files within MaxBranchInstructionBytes together, in order: invalid UTF-8
// is dropped, a file that does not fit is cut after its last whole line that does, and a file
// left with nothing but blanks is omitted.
func BoundBranchFiles(files []BranchFile) []BranchFile {
	remaining := MaxBranchInstructionBytes
	var out []BranchFile
	for _, f := range files {
		text := cutAtLine(strings.ToValidUTF8(f.Text, ""), remaining)
		if strings.TrimSpace(text) == "" {
			continue
		}
		remaining -= len(text)
		out = append(out, BranchFile{Path: f.Path, Text: text})
	}
	return out
}

// cutAtLine cuts text to at most limit bytes after a whole line; a single line longer than limit
// is cut on a character boundary.
func cutAtLine(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	if i := strings.LastIndexByte(text[:limit], '\n'); i >= 0 {
		return text[:i+1]
	}
	return cutRunes(text, limit)
}

// MentionsBranch is the gate of the call: whether any of files contains "branch", "ветк" or "merge
// request", in any letter case. Without it nothing is called.
func MentionsBranch(files []BranchFile) bool {
	for _, f := range files {
		lower := strings.ToLower(f.Text)
		if strings.Contains(lower, "branch") || strings.Contains(lower, "ветк") || strings.Contains(lower, "merge request") {
			return true
		}
	}
	return false
}

// BranchTaskLine is the first non-blank line of the task, its white space collapsed and cut to 200
// runes: what the call is given as the task and what {slug} is made of.
func BranchTaskLine(task string) string {
	for _, line := range strings.Split(task, "\n") {
		if line = oneLine(line); line != "" {
			if r := []rune(line); len(r) > maxBranchTaskRunes {
				line = string(r[:maxBranchTaskRunes])
			}
			return line
		}
	}
	return ""
}

// BranchNameInput is what the branch-name prompt is built from. Task is the task's first line
// (BranchTaskLine), Key the Jira key, "" when there is none, and Date today's date. Files are the
// bounded instruction files.
type BranchNameInput struct {
	Task  string
	Key   string
	RunID string
	Date  string
	Files []BranchFile
}

// BuildBranchNamePrompt is the prompt of the branch-name call: the task through taskBlock, the Jira
// key, the run ID and the date, then each instruction file with its path through fence.
func BuildBranchNamePrompt(in BranchNameInput) string {
	var b strings.Builder
	b.WriteString("ACTION: branch-name\n\n")
	b.WriteString(branchNameIntro + "\n\n")
	b.WriteString(branchNameShape + "\n\n")
	b.WriteString(branchNameRules + "\n\n")
	b.WriteString("Task:\n" + taskBlock(in.Task))
	b.WriteString("\n")
	if key := oneLine(in.Key); key != "" {
		b.WriteString("Jira key: " + key + "\n")
	}
	b.WriteString("Run ID: " + oneLine(in.RunID) + "\n")
	b.WriteString("Date: " + oneLine(in.Date) + "\n")
	b.WriteString("\nInstruction files of the repository, each with its path:\n")
	for i, f := range in.Files {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fence("instructions", "an instruction file of the repository", "file: "+oneLine(f.Path)+"\n"+strings.TrimSpace(f.Text)))
	}
	return b.String()
}

// BranchNameResult is what a branch-name call gave, as far as it got: the prompt as sent, the raw
// output (also when it is refused, for the run's logs), the usage (nil when the CLI reported none
// or the call did not run), the name the answer proposed, "" for the answer that the
// instructions set no rule, and likewise the merge request title pattern.
type BranchNameResult struct {
	Name   string
	Title  string
	Prompt string
	Output string
	Usage  *domain.Usage
}

// BranchName makes the one call of the branch-name step: the prompt of in on call's route with
// domain.AuthorityNone, passed explicitly like the translator's and never through AuthorityFor, so
// the model has no tools. Its logs are logs/branch-name.*. Any failure, a credential problem
// included, is the caller's to judge; an answer that holds no JSON object or no "branch" is an
// *apperr.InvalidResponseError naming the field. Neither the name nor the title is checked here.
func BranchName(ctx context.Context, call PlanningCall, in BranchNameInput) (BranchNameResult, error) {
	res := BranchNameResult{Prompt: BuildBranchNamePrompt(in)}
	kind := planningKind{action: "branch-name", log: "branch-name", timeout: "worktree.branch.from_instructions.timeout"}
	var err error
	res.Output, res.Usage, err = call.run(ctx, kind, provider.Request{Prompt: res.Prompt, Authority: domain.AuthorityNone})
	if err != nil {
		return res, err
	}
	res.Name, res.Title, err = ParseBranchName(res.Output)
	return res, err
}

type branchNameWire struct {
	Branch *string `json:"branch"`
	Title  string  `json:"title"`
}

// ParseBranchName decodes the call's output strictly: a JSON object with the string "branch" and,
// optionally, the string "title". Anything else, prose without JSON included, is an
// *apperr.InvalidResponseError. Both are trimmed; "" means the instructions set no rule.
func ParseBranchName(output string) (branch, title string, err error) {
	obj := extractJSONObject(output)
	if obj == "" {
		return "", "", branchNameInvalid("", nil, "the answer holds no JSON object")
	}
	var wire branchNameWire
	dec := json.NewDecoder(strings.NewReader(obj))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			if unquoted, uerr := strconv.Unquote(name); uerr == nil {
				name = unquoted
			}
			return "", "", branchNameInvalid(name, err, "not a field of the answer")
		}
		return "", "", branchNameInvalid("branch", err, "%s", err.Error())
	}
	if wire.Branch == nil {
		return "", "", branchNameInvalid("branch", nil, "is missing")
	}
	return strings.TrimSpace(*wire.Branch), strings.TrimSpace(wire.Title), nil
}

func branchNameInvalid(field string, err error, format string, args ...any) error {
	return &apperr.InvalidResponseError{Call: "branch-name", Field: field, Msg: fmt.Sprintf(format, args...), Err: err}
}
