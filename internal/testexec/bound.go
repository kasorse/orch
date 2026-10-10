package testexec

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Omitted is what BoundTestOutput left out of a test's output.
type Omitted struct {
	Lines int
	Bytes int
}

// Any reports whether anything was left out.
func (o Omitted) Any() bool { return o.Bytes > 0 }

// Shares of one stream's budget (specs/hardening/SPEC.md §5.2): the beginning, the failure
// lines from the middle, the end. The failure lines get what the other two leave, at least
// failurePercent.
const (
	headPercent    = 20
	failurePercent = 30
)

// BoundTestOutput cuts a failing test's stdout and stderr so together they fit budget
// bytes, for the implement prompt and for the in-session verify tools. The budget is split
// evenly, and what the shorter stream does not use goes to the other. A stream within its
// share is unchanged; a longer one keeps its beginning, the lines that look like failures
// (failureLine) from the middle, and its end, cut on line boundaries where it can and on
// rune boundaries where a single line is too long, with an in-place marker for each cut.
// The markers are the only bytes beyond the budget: everything else is the original output,
// unchanged.
//
// The result depends only on the input and the budget, so a resume rebuilds the same
// prompt from the same test result. Artifacts and logs keep the full output.
func BoundTestOutput(stdout, stderr string, budget int) (string, string, Omitted) {
	outShare := budget / 2
	errShare := budget - outShare
	switch {
	case len(stdout) < outShare:
		errShare += outShare - len(stdout)
	case len(stderr) < errShare:
		outShare += errShare - len(stderr)
	}
	out, outOmitted := boundStream(stdout, outShare)
	errText, errOmitted := boundStream(stderr, errShare)
	return out, errText, Omitted{Lines: outOmitted.Lines + errOmitted.Lines, Bytes: outOmitted.Bytes + errOmitted.Bytes}
}

// span is a kept byte range [start, end) of a stream.
type span struct{ start, end int }

func boundStream(s string, share int) (string, Omitted) {
	if len(s) <= share {
		return s, Omitted{}
	}
	if share <= 0 {
		return marker(s), Omitted{Lines: strings.Count(s, "\n"), Bytes: len(s)}
	}

	headEnd := headCut(s, share*headPercent/100)
	// First pass: the end gets everything but the beginning and the failure share, to
	// learn how much the failure lines in the middle actually need.
	tailStart := tailCut(s, headEnd, share-headEnd-share*failurePercent/100)
	failures := failureSpans(s, headEnd, tailStart, share-headEnd-(len(s)-tailStart))
	// Second pass: the end also gets the failure share the failure lines did not use.
	tailStart = tailCut(s, headEnd, share-headEnd-spansLen(failures))
	failures = failureSpans(s, headEnd, tailStart, share-headEnd-(len(s)-tailStart))

	kept := append([]span{{0, headEnd}}, failures...)
	kept = append(kept, span{tailStart, len(s)})

	var b strings.Builder
	var omitted Omitted
	pos := 0
	for _, k := range kept {
		if k.start > pos {
			gap := s[pos:k.start]
			b.WriteString(marker(gap))
			omitted.Lines += strings.Count(gap, "\n")
			omitted.Bytes += len(gap)
		}
		b.WriteString(s[k.start:k.end])
		pos = k.end
	}
	return b.String(), omitted
}

// marker is the in-place note for one cut. After a line cut short it continues that line,
// so every byte outside the markers is a byte of the original output.
func marker(gap string) string {
	if lines := strings.Count(gap, "\n"); lines > 0 {
		return fmt.Sprintf("[... %d lines, %d bytes omitted ...]\n", lines, len(gap))
	}
	return fmt.Sprintf("[... %d bytes omitted ...]\n", len(gap))
}

// headCut returns the end of the beginning kept within limit bytes: after the last newline
// that fits, or - when the first line alone is longer - on the last rune boundary that fits.
func headCut(s string, limit int) int {
	if limit <= 0 {
		return 0
	}
	if i := strings.LastIndexByte(s[:limit], '\n'); i >= 0 {
		return i + 1
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return limit
}

// tailCut returns the start of the end kept within limit bytes, never before from: the
// first line start in the last limit bytes, or - when the last line alone is longer - the
// first rune boundary in them.
func tailCut(s string, from, limit int) int {
	if limit <= 0 {
		return len(s)
	}
	start := max(len(s)-limit, from)
	if start == from || s[start-1] == '\n' {
		return start
	}
	if i := strings.IndexByte(s[start:], '\n'); i >= 0 && start+i+1 < len(s) {
		return start + i + 1
	}
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return start
}

// failureSpans returns the lines in s[from:to] that look like failures, in order, as long
// as their total fits limit. A line that does not fit is skipped, a shorter later one may
// still be taken.
func failureSpans(s string, from, to, limit int) []span {
	var spans []span
	used := 0
	for pos := from; pos < to; {
		end := to
		if i := strings.IndexByte(s[pos:to], '\n'); i >= 0 {
			end = pos + i + 1
		}
		if n := end - pos; failureLine(s[pos:end]) && used+n <= limit {
			spans = append(spans, span{pos, end})
			used += n
		}
		pos = end
	}
	return spans
}

func spansLen(spans []span) int {
	n := 0
	for _, sp := range spans {
		n += sp.end - sp.start
	}
	return n
}

// failurePrefixes start a line - after leading whitespace - that reports a failure in the
// output of common test runners: go test, pytest, jest, Python tracebacks (SPEC.md §5.2.1).
// They only choose what to show; pass or fail is the exit code alone.
var failurePrefixes = []string{
	"--- FAIL", "FAIL", "panic:", "Error", "ERROR", "error:", "E   ", "●", "✕", "AssertionError", "Traceback",
}

func failureLine(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	for _, p := range failurePrefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	// A go test failure location: "    foo_test.go:42: want 1, got 2".
	if i := strings.Index(line, "_test.go:"); i >= 0 {
		rest := line[i+len("_test.go:"):]
		return rest != "" && rest[0] >= '0' && rest[0] <= '9'
	}
	return false
}
