package action

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"orchestrator/internal/domain"
	"orchestrator/internal/repository"
)

// statBarWidth is the longest +/- bar of a stat line, as in git diff --stat.
const statBarWidth = 40

// renderDiffStat renders a numstat in the style of git diff --stat, for a retried
// implement's prompt (specs/quality/SPEC.md §4.3): one line per file, then a totals line.
// A binary file (Added < 0) shows "Bin". With maxBytes > 0 the result is at most that long:
// it keeps whole file lines from the start, then the totals (always over every file), and
// ends with "… N more files" for the lines it dropped. No files gives "". The result
// depends only on its input, so a resume rebuilds the same prompt.
func renderDiffStat(files []repository.FileChange, maxBytes int) string {
	if len(files) == 0 {
		return ""
	}
	nameWidth, maxTotal := 0, 0
	for _, f := range files {
		nameWidth = max(nameWidth, utf8.RuneCountInString(f.Path))
		maxTotal = max(maxTotal, f.Added+f.Deleted)
	}
	countWidth := len(strconv.Itoa(maxTotal))

	lines := make([]string, len(files))
	for i, f := range files {
		pad := strings.Repeat(" ", nameWidth-utf8.RuneCountInString(f.Path))
		if f.Added < 0 {
			lines[i] = fmt.Sprintf(" %s%s | Bin", f.Path, pad)
			continue
		}
		line := fmt.Sprintf(" %s%s | %*d %s", f.Path, pad, countWidth, f.Added+f.Deleted, statBar(f.Added, f.Deleted, maxTotal))
		lines[i] = strings.TrimRight(line, " ")
	}
	totals := statTotals(diffStat(files))

	full := strings.Join(lines, "\n") + "\n" + totals
	if maxBytes <= 0 || len(full) <= maxBytes {
		return full
	}

	kept, used := 0, 0
	for kept < len(lines) {
		next := used + len(lines[kept]) + 1
		if next+len(totals)+1+len(moreFiles(len(lines)-kept-1)) > maxBytes {
			break
		}
		used = next
		kept++
	}
	var b strings.Builder
	for _, l := range lines[:kept] {
		b.WriteString(l + "\n")
	}
	b.WriteString(totals + "\n" + moreFiles(len(lines)-kept))
	return b.String()
}

func moreFiles(n int) string { return fmt.Sprintf("… %d more files", n) }

// statBar is the "+++--" of one file, scaled like git when the largest change in the stat
// is wider than statBarWidth.
func statBar(added, deleted, maxTotal int) string {
	if maxTotal > statBarWidth {
		total := scaleStat(added+deleted, maxTotal)
		if total < 2 && added > 0 && deleted > 0 {
			total = 2
		}
		if added < deleted {
			added = scaleStat(added, maxTotal)
			deleted = total - added
		} else {
			deleted = scaleStat(deleted, maxTotal)
			added = total - deleted
		}
	}
	return strings.Repeat("+", added) + strings.Repeat("-", deleted)
}

// scaleStat is git's scale_linear: 0 stays 0, anything else is at least 1.
func scaleStat(n, maxTotal int) int {
	if n == 0 {
		return 0
	}
	return 1 + n*(statBarWidth-1)/maxTotal
}

// statTotals is the last line of git diff --stat, with the singular and the left-out zero
// parts git uses.
func statTotals(s domain.DiffStat) string {
	line := fmt.Sprintf(" %d %s changed", s.Files, plural(s.Files, "file", "files"))
	if s.Insertions > 0 || s.Deletions == 0 {
		line += fmt.Sprintf(", %d %s(+)", s.Insertions, plural(s.Insertions, "insertion", "insertions"))
	}
	if s.Deletions > 0 || s.Insertions == 0 {
		line += fmt.Sprintf(", %d %s(-)", s.Deletions, plural(s.Deletions, "deletion", "deletions"))
	}
	return line
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
