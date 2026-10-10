package action

import (
	"encoding/json"
	"errors"
	"strings"
)

var errNoJSONObject = errors.New("no JSON object found in review output")

// extractJSONObject returns the first valid JSON object found in s (tolerant of markdown
// fences or leading/trailing prose), or "" if none is found. A brace in the prose before
// it, such as Go code like []string{"x"}, is skipped rather than taken for the answer.
func extractJSONObject(s string) string {
	for i := strings.IndexByte(s, '{'); i != -1; {
		var raw json.RawMessage
		if json.NewDecoder(strings.NewReader(s[i:])).Decode(&raw) == nil {
			return string(raw)
		}
		next := strings.IndexByte(s[i+1:], '{')
		if next == -1 {
			break
		}
		i += 1 + next
	}
	return ""
}
