package interaction

import (
	"fmt"
	"io"
	"strings"

	"orchestrator/internal/domain"
)

// CommitOffer is what the operator is shown before deciding whether a finished run's
// changes are committed (MVP.md §42).
type CommitOffer struct {
	// Branch is the branch the commit goes on; "" for a detached HEAD.
	Branch string
	// Changes is the short status of what would be committed, one "XY path" per line.
	Changes string
	// Warnings are reasons to look closer first: a failed review, a likely secret.
	Warnings []string
	// Header is the proposed first line of the message, "" when there is none to propose
	// and the operator writes it; Body is everything after it (the trailers), kept when
	// the operator edits the header.
	Header string
	Body   string
	// PathRules are the policy.paths matches with approval: manual, with their files
	// (specs/governance/SPEC.md §4.5); listed before the question.
	PathRules []domain.PathRuleMatch
}

// Message is the full commit message for header.
func (o CommitOffer) Message(header string) string {
	if o.Body == "" {
		return header + "\n"
	}
	return header + "\n\n" + o.Body
}

// AskCommit shows the offer and asks whether to commit. The default is no: nothing is
// committed without an explicit yes. "e" replaces the message's first line and asks
// again. Without a proposed header the operator writes one first; an empty one commits
// nothing. It returns the header to commit with, or ok false.
func AskCommit(p Prompter, out io.Writer, o CommitOffer) (header string, ok bool, err error) {
	fmt.Fprintf(out, "\nChanges:\n%s\n", indent(o.Changes))
	for _, w := range o.Warnings {
		fmt.Fprintf(out, "Warning: %s\n", w)
	}
	if len(o.PathRules) > 0 {
		fmt.Fprintln(out, "\npolicy.paths requires your approval for:")
		for _, m := range o.PathRules {
			fmt.Fprintf(out, "  %s\n", m.Match)
			for _, f := range m.Files {
				fmt.Fprintf(out, "    %s\n", f)
			}
		}
	}
	where := o.Branch
	if where == "" {
		where = "a detached HEAD"
	}
	header = o.Header
	if header == "" {
		fmt.Fprintln(out, "\nThe implementation proposed no commit message.")
		line, err := p.Ask("Commit message (one line, empty to skip): ")
		if err != nil {
			return "", false, err
		}
		if header = strings.TrimSpace(line); header == "" {
			return "", false, nil
		}
	}
	for {
		fmt.Fprintf(out, "\nCommit message:\n%s\n", indent(strings.TrimRight(o.Message(header), "\n")))
		ans, err := p.Ask(fmt.Sprintf("Commit to %s? [y]es / [N]o / [e]dit message: ", where))
		if err != nil {
			return "", false, err
		}
		switch strings.ToLower(ans) {
		case "y", "yes":
			return header, true, nil
		case "n", "no", "":
			return "", false, nil
		case "e", "edit":
			line, err := p.Ask("Commit message (one line): ")
			if err != nil {
				return "", false, err
			}
			if line = strings.TrimSpace(line); line != "" {
				header = line
			}
		default:
			fmt.Fprintln(out, "unrecognized option")
		}
	}
}

// indent indents every non-empty line of s by two spaces.
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}
