package projectctx

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// queryTokens are the search terms taken from the request. Both the original and the
// translated text contribute: identifiers (ProductRepository) survive translation
// unchanged, while English words match paths better than Russian ones.
type queryTokens struct {
	// words are lowercase Latin words and identifier parts, stopwords removed.
	words []string
	// identifiers are code-shaped tokens (CamelCase, snake_case, dotted) searched for
	// verbatim in file contents.
	identifiers []string
}

var (
	wordRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	// identifierRe: an inner capital (productRepository, ProductRepository, HTTPClient)
	// or an underscore between letters (product_repository).
	identifierRe = regexp.MustCompile(`^(?:[A-Za-z][a-z0-9]*[A-Z][A-Za-z0-9]*|[A-Z]{2,}[a-z][A-Za-z0-9]*|[A-Za-z0-9]+_[A-Za-z0-9_]+)$`)
)

// stopwords are request words too generic to say anything about which files matter.
var stopwords = toSet(
	"the", "and", "for", "with", "into", "from", "this", "that", "these", "those", "there",
	"add", "adds", "fix", "make", "use", "using", "new", "all", "any", "should", "must", "need",
	"needs", "please", "code", "file", "files", "support", "implement", "change", "changes",
	"update", "remove", "create", "write", "when", "what", "how", "why", "which", "where",
	"are", "not", "can", "its", "has", "have", "was", "will", "via", "does", "doesn", "don",
	"test", "tests", "testing", "current", "existing", "project", "repository", "repo",
	"function", "method", "class", "some", "also", "only", "just", "like", "more", "less",
	"about", "then", "than", "but", "our", "your", "you", "them", "they", "his", "her",
)

// Identifier parts are exempt from stopwords: "Repository" inside ProductRepository is
// meaningful even though "repository" alone is not.

func newQueryTokens(texts ...string) queryTokens {
	words := map[string]bool{}
	idents := map[string]bool{}
	for _, text := range texts {
		for _, raw := range wordRe.FindAllString(text, -1) {
			isIdent := isIdentifier(raw)
			if isIdent && len(raw) >= 4 {
				idents[raw] = true
			}
			for _, part := range splitIdentifier(raw) {
				if len(part) < 3 || (!isIdent && stopwords[part]) {
					continue
				}
				words[part] = true
			}
		}
	}
	return queryTokens{words: sortedSet(words), identifiers: sortedSet(idents)}
}

// isIdentifier accepts code-shaped tokens only. An all-caps word (HTTP, API) is an
// ordinary word: searched verbatim it would match half the codebase.
func isIdentifier(s string) bool {
	if !identifierRe.MatchString(s) {
		return false
	}
	if strings.Contains(s, "_") {
		return true
	}
	return strings.ToUpper(s) != s && strings.ToLower(s) != s
}

func (q queryTokens) empty() bool { return len(q.words) == 0 && len(q.identifiers) == 0 }

// splitIdentifier breaks CamelCase, snake_case and digits into lowercase parts:
// "HTTPProductRepository_v2" -> http, product, repository, v2.
func splitIdentifier(s string) []string {
	var parts []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '_' || !(unicode.IsLetter(r) || unicode.IsDigit(r)):
			flush()
			continue
		case unicode.IsUpper(r) && len(cur) > 0:
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return parts
}

// pathTokens returns the lowercase parts of every segment of a path, extension dropped.
func pathTokens(rel string) []string {
	return textTokens(strings.TrimSuffix(rel, extOf(rel)))
}

// textTokens returns the lowercase word and identifier parts of free text.
func textTokens(s string) []string {
	var out []string
	for _, raw := range wordRe.FindAllString(s, -1) {
		out = append(out, splitIdentifier(raw)...)
	}
	return out
}

func extOf(rel string) string {
	base := rel[strings.LastIndex(rel, "/")+1:]
	if i := strings.LastIndex(base, "."); i > 0 {
		return base[i:]
	}
	return ""
}

// wordsMatch is a deliberately loose stem match: equal, or sharing a prefix of at
// least 4 letters that covers 80% of the shorter word (cache/caching,
// repository/repositories, test/testing).
func wordsMatch(a, b string) bool {
	if a == b {
		return true
	}
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n >= 4 && n*5 >= 4*min(len(a), len(b))
}

// countMatches returns how many of words match at least one of candidates.
func countMatches(words, candidates []string) int {
	n := 0
	for _, w := range words {
		for _, c := range candidates {
			if wordsMatch(w, c) {
				n++
				break
			}
		}
	}
	return n
}

func toSet(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
