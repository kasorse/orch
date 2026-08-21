package action

import "errors"

var errNoJSONObject = errors.New("no JSON object found in review output")

// extractJSONObject returns the first top-level {...} object found in s (tolerant of
// markdown fences or leading/trailing prose), or "" if none is found.
func extractJSONObject(s string) string {
	start := -1
	depth := 0
	for i, r := range s {
		switch r {
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && start != -1 {
					return s[start : i+1]
				}
			}
		}
	}
	return ""
}
