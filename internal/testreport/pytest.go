package testreport

import (
	"regexp"
	"strings"
)

var pytestHeaderRE = regexp.MustCompile(`^=+ short test summary info =+\s*$`)

// parsePytest reads the `short test summary info` section: one `FAILED path::name - reason`
// line per failed test, until the next `===` line. The section exists only with a -r option
// that lists failures, which is pytest's default.
func parsePytest(in input) ([]string, bool) {
	var names []string
	recognised, inSection := false, false
	for _, line := range in.lines {
		switch {
		case pytestHeaderRE.MatchString(line):
			recognised, inSection = true, true
		case inSection && strings.HasPrefix(line, "="):
			inSection = false
		case inSection && strings.HasPrefix(line, "FAILED "):
			names = append(names, pytestName(strings.TrimPrefix(line, "FAILED ")))
		}
	}
	return names, recognised
}

// pytestName cuts the ` - reason` pytest appends to a summary line. A parametrized id can
// contain " - " inside its brackets, which stays.
func pytestName(s string) string {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case ' ':
			if depth == 0 && strings.HasPrefix(s[i:], " - ") {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return strings.TrimSpace(s)
}
