// Package story is the pure core of a story (specs/story/SPEC.md): it imports only domain and
// the standard library.
package story

import (
	"strings"
	"unicode"
)

// Named returns the candidates a task names (SPEC.md §7.1 point 2), in candidate order: one
// whose name occurs in text as a whole word, case-insensitively - not next to a letter (any
// script), a digit, "_" or "-" - or equals a Jira component's name, case-insensitively. No
// regular expression is built from a name.
func Named(text string, components, candidates []string) []string {
	lowerText := []rune(strings.ToLower(text))
	var out []string
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if mentioned(lowerText, []rune(strings.ToLower(c))) || equalFoldAny(c, components) {
			out = append(out, c)
		}
	}
	return out
}

func equalFoldAny(name string, list []string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), name) {
			return true
		}
	}
	return false
}

// mentioned reports whether word occurs in text with a boundary on both sides.
func mentioned(text, word []rune) bool {
	for i := 0; i+len(word) <= len(text); i++ {
		if !runesEqual(text[i:i+len(word)], word) {
			continue
		}
		if i > 0 && wordRune(text[i-1]) {
			continue
		}
		if end := i + len(word); end < len(text) && wordRune(text[end]) {
			continue
		}
		return true
	}
	return false
}

func runesEqual(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}
