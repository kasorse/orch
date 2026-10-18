package story

import (
	"sort"
	"strings"
	"unicode"
)

// Named returns the candidates a task names (SPEC §7.1 point 2), sorted and without
// duplicates, in the candidates' own spelling. A candidate is named when its name occurs in
// text as a whole word, case-insensitively: the characters on both sides of the occurrence
// are not a letter of any script, a digit, "_" or "-". A candidate is also named by a
// component equal to it, case-insensitively. The text is scanned rune by rune and no pattern
// is built from a name.
func Named(text string, components, candidates []string) []string {
	hay := foldRunes(text)
	var named []string
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		if hasComponent(components, c) || containsWord(hay, foldRunes(c)) {
			named = append(named, c)
		}
	}
	sort.Strings(named)
	return named
}

func hasComponent(components []string, name string) bool {
	for _, comp := range components {
		if strings.EqualFold(strings.TrimSpace(comp), name) {
			return true
		}
	}
	return false
}

// foldRunes lower-cases rune by rune, so that positions in the result are positions in the
// text, which a string-wide ToLower does not promise.
func foldRunes(s string) []rune {
	runes := []rune(s)
	for i, r := range runes {
		runes[i] = unicode.ToLower(r)
	}
	return runes
}

func containsWord(hay, word []rune) bool {
	if len(word) == 0 {
		return false
	}
	for i := 0; i+len(word) <= len(hay); i++ {
		end := i + len(word)
		if !runesEqual(hay[i:end], word) {
			continue
		}
		if i > 0 && inWord(hay[i-1]) {
			continue
		}
		if end < len(hay) && inWord(hay[end]) {
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

// inWord is a character that keeps a service name from being a whole word.
func inWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}
