// Package testreport extracts the names of failing tests from the output of common test
// runners (specs/quality/SPEC.md §8.2).
//
// It never decides pass/fail: the exit code does (MVP.md §31.4). The names only go into a
// retry prompt and into the progress comparison, and an unrecognised or truncated output
// costs nothing but a shorter list. Every parser is pure and tolerant: a line it cannot read
// is skipped, never an error. The package imports only the standard library.
package testreport

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The formats Parse recognises, as recorded in TestResult.ReportFormat.
const (
	FormatGoJSON   = "gojson"
	FormatJestJSON = "jestjson"
	FormatPytest   = "pytest"
	FormatGoText   = "gotext"
	FormatJestText = "jesttext"
)

const (
	// MaxNames is the most failing tests a Report lists.
	MaxNames = 200
	// MaxNameBytes is the longest a listed name is; a longer one is cut on a UTF-8 boundary.
	MaxNameBytes = 300
)

// Report is what Parse found. Format is "" and Failed empty when no parser recognised the
// output; a recognised format can still list no failures (a green run, or a build error).
type Report struct {
	Format string
	Failed []string
}

// KnownFormat reports whether f is a format Parse can return, "" excluded.
func KnownFormat(f string) bool {
	switch f {
	case FormatGoJSON, FormatJestJSON, FormatPytest, FormatGoText, FormatJestText:
		return true
	}
	return false
}

// input is the output being parsed, split once for all parsers.
type input struct {
	text  string
	lines []string
}

type formatParser struct {
	format string
	// parse returns the failing names and whether the output is in this parser's format.
	parse func(in input) ([]string, bool)
}

// parsers is the fixed order of the formats; the first that recognises the output wins. The
// structured formats come before the text ones, which are looser.
var parsers = []formatParser{
	{FormatGoJSON, parseGoJSON},
	{FormatJestJSON, parseJestJSON},
	{FormatPytest, parsePytest},
	{FormatGoText, parseGoText},
	{FormatJestText, parseJestText},
}

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// Parse reads the failing tests out of a command's stdout and stderr, read as one text.
func Parse(stdout, stderr string) Report {
	text := ansiRE.ReplaceAllString(stdout+"\n"+stderr, "")
	in := input{text: text, lines: splitLines(text)}
	for _, p := range parsers {
		if names, ok := p.parse(in); ok {
			return Report{Format: p.format, Failed: normalize(names)}
		}
	}
	return Report{}
}

func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// normalize makes names single-line, cuts each to MaxNameBytes, drops empty ones and
// duplicates, sorts them and keeps the first MaxNames.
func normalize(names []string) []string {
	seen := make(map[string]bool, len(names))
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, n))
		n = cutBytes(n, MaxNameBytes)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	if len(out) > MaxNames {
		out = out[:MaxNames]
	}
	return out
}

// cutBytes shortens s to at most max bytes without splitting a rune.
func cutBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return strings.TrimSpace(s[:max])
}

// joinName joins the non-empty parts with sep.
func joinName(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}
