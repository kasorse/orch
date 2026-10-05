// Package secretscan finds likely secrets in text a run added to the repository
// (specs/hardening/SPEC.md §9.2). It reports where and which rule, never the value: a
// Finding carries a rule, a line and a fingerprint, so nothing built from it can leak what
// it found. It uses the standard library only.
package secretscan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
)

// Finding is one likely secret: the rule that matched, the 1-based line, and a fingerprint
// of rule, path and value that stays the same when the line moves.
type Finding struct {
	Rule        string
	Line        int
	Fingerprint string
}

// String is "line N (rule)" - never the value.
func (f Finding) String() string { return fmt.Sprintf("line %d (%s)", f.Line, f.Rule) }

// NumberedLine is one line of content with its line number in the file, for scanning the
// added lines of a diff.
type NumberedLine struct {
	Number int
	Text   string
}

// Limits of Scan: larger files and binary ones are skipped.
const (
	MaxFileSize   = 1 << 20
	binarySniffSz = 8 << 10
)

// RuleIDs returns every rule's ID, in reporting order.
func RuleIDs() []string {
	ids := make([]string, len(rules))
	for i, r := range rules {
		ids[i] = r.id
	}
	return ids
}

// Scan scans a file's content. skipped is true for a binary file (a NUL byte in its first
// 8 KiB) or one over MaxFileSize, which are not scanned.
func Scan(path string, content []byte) (findings []Finding, skipped bool) {
	if len(content) > MaxFileSize || bytes.IndexByte(content[:min(len(content), binarySniffSz)], 0) >= 0 {
		return nil, true
	}
	text := strings.Split(string(content), "\n")
	lines := make([]NumberedLine, len(text))
	for i, l := range text {
		lines[i] = NumberedLine{Number: i + 1, Text: l}
	}
	return ScanLines(path, lines), false
}

// ScanLines scans the given lines of path - the added lines of a diff, with their line
// numbers in the new file. Each rule is reported at most once per line, in line order. A
// value one rule reported is not reported again by a later, more general one (rules are
// ordered specific first): an AWS secret in "aws_secret = ..." is one finding, not two.
func ScanLines(path string, lines []NumberedLine) []Finding {
	var findings []Finding
	for _, l := range lines {
		lower := ""
		var taken [][2]int
		for _, r := range rules {
			if !r.hinted(l.Text, &lower) {
				continue
			}
			for _, m := range r.re.FindAllStringSubmatchIndex(l.Text, -1) {
				start, end := valueSpan(m)
				value := l.Text[start:end]
				if (r.accept != nil && !r.accept(value, l.Text)) || overlaps(taken, start, end) {
					continue
				}
				taken = append(taken, [2]int{start, end})
				findings = append(findings, Finding{Rule: r.id, Line: l.Number, Fingerprint: fingerprint(r.id, path, value)})
				break
			}
		}
	}
	return findings
}

func overlaps(spans [][2]int, start, end int) bool {
	for _, s := range spans {
		if start < s[1] && s[0] < end {
			return true
		}
	}
	return false
}

// Redact replaces every value Scan would report in text with [REDACTED:<rule>], and a
// private key's whole block. It applies the same rules and exclusions, so a value that is
// reported is a value that is redacted.
func Redact(text string) string {
	type span struct {
		start, end int
		rule       string
	}
	var spans []span
	for _, lineStart := range lineStarts(text) {
		lineEnd := len(text)
		if i := strings.IndexByte(text[lineStart:], '\n'); i >= 0 {
			lineEnd = lineStart + i
		}
		line := text[lineStart:lineEnd]
		lower := ""
		for _, r := range rules {
			if r.redact != nil || !r.hinted(line, &lower) {
				continue
			}
			for _, m := range r.re.FindAllStringSubmatchIndex(line, -1) {
				start, end := valueSpan(m)
				if r.accept != nil && !r.accept(line[start:end], line) {
					continue
				}
				spans = append(spans, span{lineStart + start, lineStart + end, r.id})
			}
		}
	}
	for _, r := range rules {
		if r.redact == nil {
			continue
		}
		for _, m := range r.redact.FindAllStringIndex(text, -1) {
			spans = append(spans, span{m[0], m[1], r.id})
		}
	}
	if len(spans) == 0 {
		return text
	}

	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var b strings.Builder
	pos := 0
	for _, s := range spans {
		if s.end <= pos { // inside a span already replaced
			continue
		}
		if s.start > pos {
			b.WriteString(text[pos:s.start])
		}
		b.WriteString("[REDACTED:" + s.rule + "]")
		pos = s.end
	}
	b.WriteString(text[pos:])
	return b.String()
}

// hinted reports whether line contains one of the rule's hints. lower caches the
// lower-cased line across rules.
func (r rule) hinted(line string, lower *string) bool {
	hay := line
	if r.foldHints {
		if *lower == "" {
			*lower = strings.ToLower(line)
		}
		hay = *lower
	}
	return slices.ContainsFunc(r.hints, func(h string) bool { return strings.Contains(hay, h) }) &&
		(r.pre == nil || r.pre(line))
}

// valueSpan is the first capture group that matched, or the whole match.
func valueSpan(m []int) (int, int) {
	for g := 2; g+1 < len(m); g += 2 {
		if m[g] >= 0 {
			return m[g], m[g+1]
		}
	}
	return m[0], m[1]
}

func lineStarts(text string) []int {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' && i+1 < len(text) {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// fingerprint is hex sha256(rule \0 path \0 hex(sha256(value))): stable when the line
// moves, different in another file, and not reversible to the value.
func fingerprint(rule, path, value string) string {
	v := sha256.Sum256([]byte(value))
	f := sha256.Sum256([]byte(rule + "\x00" + path + "\x00" + hex.EncodeToString(v[:])))
	return hex.EncodeToString(f[:])
}

// entropy is the Shannon entropy of s in bits per character.
func entropy(s string) float64 {
	if s == "" {
		return 0
	}
	counts := map[rune]int{}
	n := 0
	for _, r := range s {
		counts[r]++
		n++
	}
	h := 0.0
	for _, c := range counts {
		p := float64(c) / float64(n)
		h -= p * math.Log2(p)
	}
	return h
}
