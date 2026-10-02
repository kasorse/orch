package logging

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The diff card: what an implement step changed, shown the way agent UIs show edits -
// one section per file with line numbers, + and - lines coloured, light syntax
// highlighting, long changes elided in the middle, and the totals at the bottom.

type diffLine struct {
	op   byte // '+' or '-'
	num  int  // new line number for '+', old for '-'
	text string
}

type fileDiff struct {
	path    string
	note    string // "new", "deleted", "renamed from x", "binary"
	added   int
	removed int
	lines   []diffLine
}

var hunkRE = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// parseDiff reads `git diff` output into per-file changes.
func parseDiff(diff string) []fileDiff {
	var files []fileDiff
	var cur *fileDiff
	oldN, newN := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			files = append(files, fileDiff{})
			cur = &files[len(files)-1]
			if i := strings.LastIndex(line, " b/"); i >= 0 {
				cur.path = line[i+3:]
			}
			continue
		case cur == nil:
			continue
		case strings.HasPrefix(line, "new file mode"):
			cur.note = "new"
		case strings.HasPrefix(line, "deleted file mode"):
			cur.note = "deleted"
		case strings.HasPrefix(line, "rename from "):
			cur.note = "renamed from " + strings.TrimPrefix(line, "rename from ")
		case strings.HasPrefix(line, "Binary files"):
			cur.note = "binary"
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
		case strings.HasPrefix(line, "@@"):
			if m := hunkRE.FindStringSubmatch(line); m != nil {
				oldN, _ = strconv.Atoi(m[1])
				newN, _ = strconv.Atoi(m[2])
			}
		case strings.HasPrefix(line, "+"):
			cur.lines = append(cur.lines, diffLine{'+', newN, line[1:]})
			cur.added++
			newN++
		case strings.HasPrefix(line, "-"):
			cur.lines = append(cur.lines, diffLine{'-', oldN, line[1:]})
			cur.removed++
			oldN++
		case strings.HasPrefix(line, " "):
			oldN++
			newN++
		}
	}
	return files
}

const (
	diffHeadLines = 8 // changed lines shown at the start of each file
	diffTailLines = 3 // and at its end, when the middle is elided
	diffMaxFiles  = 8
)

// renderDiff draws the card; "" when the diff has no file changes.
func (r *ConsoleReporter) renderDiff(diff string) string {
	files := parseDiff(diff)
	if len(files) == 0 {
		return ""
	}
	st := r.st()
	width := termWidth() - 1
	var b strings.Builder
	totalAdd, totalDel := 0, 0
	for _, f := range files {
		totalAdd += f.added
		totalDel += f.removed
	}
	for i, f := range files {
		if i == diffMaxFiles {
			fmt.Fprintf(&b, "  %s %s\n", st.gray("├─"), st.gray(fmt.Sprintf("… %d more files", len(files)-i)))
			break
		}
		corner := "├─"
		if i == 0 {
			corner = "╭─"
		}
		head := st.gray(corner+" diff · ") + st.cyan(f.path)
		if f.note != "" {
			head += " " + st.gray("("+f.note+")")
		}
		head += " " + st.green(fmt.Sprintf("+%d", f.added)) + " " + st.red(fmt.Sprintf("-%d", f.removed))
		b.WriteString("  " + head + "\n")

		numW := 1
		for _, l := range f.lines {
			numW = max(numW, len(strconv.Itoa(l.num)))
		}
		show := f.lines
		elided := 0
		if len(show) > diffHeadLines+diffTailLines+1 {
			elided = len(show) - diffHeadLines - diffTailLines
		}
		for j, l := range show {
			if elided > 0 && j == diffHeadLines {
				fmt.Fprintf(&b, "  %s %s\n", st.gray("│"), st.gray(fmt.Sprintf("… %d more changes …", elided)))
			}
			if elided > 0 && j >= diffHeadLines && j < len(show)-diffTailLines {
				continue
			}
			text := strings.ReplaceAll(l.text, "\t", "  ")
			room := width - 2 - 2 - numW - 2
			if visibleWidth(text) > room {
				text = truncateVisible(text, max(room, 10))
			}
			num := fmt.Sprintf("%*d", numW, l.num)
			if l.op == '+' {
				fmt.Fprintf(&b, "  %s %s %s\n", st.gray("│"), st.green(num+"+"), highlightCode(st, text))
			} else {
				fmt.Fprintf(&b, "  %s %s %s\n", st.gray("│"), st.red(num+"-"), st.red(text))
			}
		}
	}
	total := fmt.Sprintf("(%s %s total", st.green(fmt.Sprintf("+%d", totalAdd)), st.red(fmt.Sprintf("-%d", totalDel)))
	if len(files) > 1 {
		total += st.gray(", " + countOf(len(files), "file"))
	}
	b.WriteString("  " + st.gray("╰─ ") + total + st.gray(")") + "\n")
	return b.String()
}

// SetStepDiff hands the reporter what the step that is about to finish changed; the
// pretty renderer prints it under the step's result.
func (r *ConsoleReporter) SetStepDiff(diff string) {
	r.stepDiff = diff
}

var codeTokenRE = regexp.MustCompile(
	`(//.*$|#.*$|/\*.*?\*/|--\s.*$)` + // 1 comment
		"|(\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'|`[^`]*`)" + // 2 string
		`|\b(\d+(?:\.\d+)?)\b` + // 3 number
		`|\b([A-Za-z_]\w*)\b(\s*\()?`) // 4 identifier, 5 call

var codeKeywords = map[string]bool{}

func init() {
	for _, k := range strings.Fields(`func return if else for range switch case default type struct interface
		package import const var go defer map chan select break continue fallthrough goto nil true false
		export from function let class extends new async await try catch finally throw yield typeof instanceof
		in of delete void this super null undefined static public private protected readonly enum implements
		def self None True False elif lambda with as pass raise not and or is fn mut pub use impl trait match`) {
		codeKeywords[k] = true
	}
}

// highlightCode colours one line of source: comments, strings, numbers, keywords and
// calls. It is a heuristic shared by every language - good enough to read a diff by.
func highlightCode(st style, line string) string {
	if !st.on {
		return line
	}
	var b strings.Builder
	pos := 0
	for _, m := range codeTokenRE.FindAllStringSubmatchIndex(line, -1) {
		b.WriteString(line[pos:m[0]])
		tok := line[m[0]:m[1]]
		switch {
		case m[2] >= 0:
			b.WriteString(st.gray(tok))
		case m[4] >= 0:
			b.WriteString(st.green(tok))
		case m[6] >= 0:
			b.WriteString(st.cyan(tok))
		case m[8] >= 0:
			id := line[m[8]:m[9]]
			rest := line[m[9]:m[1]]
			switch {
			case codeKeywords[id]:
				b.WriteString(st.magenta(id))
			case m[10] >= 0:
				b.WriteString(st.blue(id))
			default:
				b.WriteString(id)
			}
			b.WriteString(rest)
		default:
			b.WriteString(tok)
		}
		pos = m[1]
	}
	b.WriteString(line[pos:])
	return b.String()
}
