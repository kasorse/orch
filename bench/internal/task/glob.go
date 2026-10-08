package task

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Match reports whether the slash-separated path matches the doublestar-style pattern:
//
//	pattern matches
//	*       any run of characters except /
//	?       one character except /
//	**/     zero or more directories
//	/**     everything below a directory (also / itself)
//	**      anything, / included
//	{a,b}   alternation, nestable
//
// The whole path must match. A malformed pattern (an unbalanced brace) matches nothing.
func Match(pattern, path string) bool {
	re, err := globRegexp(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(path)
}

func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`^`)
	depth := 0

	for i := 0; i < len(pattern); {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i += 2
				if i < len(pattern) && pattern[i] == '/' {
					// "**/": zero or more whole directories.
					b.WriteString(`(?:.*/)?`)
					i++
				} else {
					b.WriteString(`.*`)
				}
				continue
			}
			b.WriteString(`[^/]*`)
		case '?':
			b.WriteString(`[^/]`)
		case '{':
			depth++
			b.WriteString(`(?:`)
		case '}':
			if depth == 0 {
				return nil, fmt.Errorf("glob %q: unmatched }", pattern)
			}
			depth--
			b.WriteString(`)`)
		case ',':
			if depth > 0 {
				b.WriteString(`|`)
			} else {
				b.WriteString(`,`)
			}
		default:
			r, size := utf8.DecodeRuneInString(pattern[i:])
			b.WriteString(regexp.QuoteMeta(string(r)))
			i += size
			continue
		}
		i++
	}

	if depth != 0 {
		return nil, fmt.Errorf("glob %q: unmatched {", pattern)
	}
	b.WriteString(`$`)
	return regexp.Compile(b.String())
}
