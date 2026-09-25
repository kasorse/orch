package projectctx

import (
	"regexp"
	"sort"
	"strings"

	"orchestrator/internal/domain"
)

// ruleFiles hold project instructions: every bullet in them is a candidate rule
// (MVP.md §7.1). Matched by name at the repository root, case-insensitively.
var ruleFiles = []string{"agents.md", "claude.md", "contributing.md"}

const (
	maxRuleLen       = 300
	maxRules         = 40
	maxExcerptLen    = 300
	maxDocExcerpts   = 5
	maxDocCandidates = 30 // docs/*.md files looked at, at most
)

var (
	bulletRe  = regexp.MustCompile(`^\s{0,3}(?:[-*+]|\d+[.)])\s+(.+)$`)
	headingRe = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*#*\s*$`)
)

// extractRules returns the bullet lines of a Markdown document, with wrapped
// continuation lines joined and code blocks skipped.
func extractRules(md string) []string {
	var rules []string
	inFence := false
	current := -1
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			current = -1
			continue
		}
		if inFence {
			continue
		}
		if m := bulletRe.FindStringSubmatch(line); m != nil {
			rules = append(rules, strings.TrimSpace(m[1]))
			current = len(rules) - 1
			continue
		}
		// An indented, non-empty line right after a bullet continues it.
		if current >= 0 && trimmed != "" && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")) {
			rules[current] += " " + trimmed
			continue
		}
		current = -1
	}
	for i, r := range rules {
		rules[i] = clip(r, maxRuleLen)
	}
	return rules
}

type section struct {
	heading string
	body    []string
}

// splitSections splits Markdown at headings; text before the first heading is a
// section with an empty heading.
func splitSections(md string) []section {
	var out []section
	cur := section{}
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
		}
		if !inFence {
			if m := headingRe.FindStringSubmatch(line); m != nil {
				out = append(out, cur)
				cur = section{heading: m[1]}
				continue
			}
		}
		cur.body = append(cur.body, line)
	}
	return append(out, cur)
}

// firstParagraph returns the first prose paragraph of a section: code blocks, tables
// and blank lines skipped, clipped to maxExcerptLen.
func firstParagraph(lines []string) string {
	var para []string
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			if len(para) > 0 {
				break
			}
			continue
		}
		if inFence || strings.HasPrefix(trimmed, "|") || strings.HasPrefix(trimmed, "<") {
			continue
		}
		if trimmed == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		para = append(para, trimmed)
	}
	return clip(strings.Join(para, " "), maxExcerptLen)
}

// docExcerpts returns the sections of README.md and docs/*.md that mention the
// request's terms, most matching first. Only the opening of each section is kept: the
// point is to show Jev what the docs say about the area, not to ship the docs.
func docExcerpts(root string, files []string, q queryTokens) []domain.DocExcerpt {
	if len(q.words) == 0 {
		return nil
	}
	var docs []string
	for _, f := range files {
		lower := strings.ToLower(f)
		if lower == "readme.md" || (strings.HasPrefix(lower, "docs/") && strings.Count(lower, "/") == 1 && strings.HasSuffix(lower, ".md")) {
			docs = append(docs, f)
		}
	}
	if len(docs) > maxDocCandidates {
		docs = docs[:maxDocCandidates]
	}

	type scored struct {
		excerpt domain.DocExcerpt
		score   int
	}
	var found []scored
	for _, doc := range docs {
		content, ok := readText(root, doc)
		if !ok {
			continue
		}
		for _, sec := range splitSections(content) {
			text := sec.heading + "\n" + strings.Join(sec.body, "\n")
			score := countMatches(q.words, textTokens(text))
			if score == 0 {
				continue
			}
			para := firstParagraph(sec.body)
			if para == "" {
				continue
			}
			found = append(found, scored{domain.DocExcerpt{Path: doc, Section: sec.heading, Text: para}, score})
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].score > found[j].score })
	var out []domain.DocExcerpt
	for i := 0; i < len(found) && i < maxDocExcerpts; i++ {
		out = append(out, found[i].excerpt)
	}
	return out
}

// projectRules collects the bullets of the root instruction files, deduplicated.
func projectRules(root string, files []string) []string {
	byName := map[string]string{}
	for _, f := range files {
		if !strings.Contains(f, "/") {
			byName[strings.ToLower(f)] = f
		}
	}
	seen := map[string]bool{}
	var rules []string
	for _, name := range ruleFiles {
		f, ok := byName[name]
		if !ok {
			continue
		}
		content, ok := readText(root, f)
		if !ok {
			continue
		}
		for _, r := range extractRules(content) {
			if !seen[r] && len(rules) < maxRules {
				seen[r] = true
				rules = append(rules, r)
			}
		}
	}
	return rules
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.ToValidUTF8(s[:n], "")
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}
