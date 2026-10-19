package story

import (
	"fmt"
	"strings"
)

// AsBuilt is the record Go appends to a task file when the task is done (SPEC §10.3). All of
// it is data from the run's record; the only text a model wrote is Summary, the run's
// summary_short, which is put on one line after a fixed label.
//
// Date is the day as 2006-01-02. Base is the branch the task was stacked on, or the
// repository's story base branch, at BaseSHA; BaseBranch is the branch the conflict check
// ran against and Conflicts what it found, "" meaning none.
type AsBuilt struct {
	Date          string
	RunID         string
	Repo          string
	Status        string
	FixIterations int
	TestsPassed   bool
	ReviewVerdict string
	Branch        string
	Base          string
	BaseSHA       string
	Commit        string
	CostUSD       float64
	Summary       string
	BaseBranch    string
	Conflicts     string
}

// inline makes s one line that cannot close the code span it is put in.
func inline(s string) string {
	return strings.ReplaceAll(oneLine(s), "`", "'")
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (a AsBuilt) lines() []string {
	tests := "tests failed"
	if a.TestsPassed {
		tests = "tests passed"
	}
	review := "no review"
	if a.ReviewVerdict != "" {
		review = "review " + inline(a.ReviewVerdict)
	}
	summary := oneLine(a.Summary)
	if summary == "" {
		summary = "none"
	}
	if !strings.HasSuffix(summary, ".") {
		summary += "."
	}
	conflicts := oneLine(a.Conflicts)
	if conflicts == "" {
		conflicts = "none"
	}
	return []string{
		fmt.Sprintf("%s (%s)", asBuiltHeading, inline(a.Date)),
		"",
		fmt.Sprintf("Run `%s` in %s: %s, %s, %s, %s.", inline(a.RunID), inline(a.Repo), inline(a.Status), plural(a.FixIterations, "fix iteration", "fix iterations"), tests, review),
		fmt.Sprintf("Branch `%s` on `%s` @ %s, commit %s.", inline(a.Branch), inline(a.Base), shortSHA(inline(a.BaseSHA)), shortSHA(inline(a.Commit))),
		fmt.Sprintf("Cost $%.2f. Implementation summary: %s", a.CostUSD, summary),
		fmt.Sprintf("Conflicts with %s: %s", inline(a.BaseBranch), strings.TrimSuffix(conflicts, ".")+"."),
	}
}

// AppendAsBuilt returns data with the "## As built (date)" section added at the end, after a
// blank line, in the file's own line ending. It touches nothing above the section, so
// FrozenHash of the result equals FrozenHash of data.
func AppendAsBuilt(data []byte, a AsBuilt) []byte {
	s := string(data)
	eol := "\n"
	if first, _ := nextLine(s, 0); strings.HasSuffix(first, "\r") {
		eol = "\r\n"
	}
	var b strings.Builder
	b.WriteString(s)
	if s != "" && !strings.HasSuffix(s, "\n") {
		b.WriteString(eol)
	}
	if s != "" && !strings.HasSuffix(s, "\n\n") && !strings.HasSuffix(s, "\n\r\n") {
		b.WriteString(eol)
	}
	for _, line := range a.lines() {
		b.WriteString(line + eol)
	}
	return []byte(b.String())
}
