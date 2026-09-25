package projectctx

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strings"

	"orchestrator/internal/domain"
)

const (
	maxSummaryLen    = 200
	summaryScanLines = 60
	maxContentScans  = 2000
	maxContentBytes  = 16 << 20
	identifierWeight = 4 // a verbatim identifier hit outweighs any number of path-word hits
)

// vendoredDirs hold third-party code, build output or test fixtures: matching them
// says nothing about where the request's work is.
var vendoredDirs = toSet("vendor", "node_modules", "third_party", "dist", "build", "testdata", "fixtures")

type candidate struct {
	path  string
	score int
}

// relevantFiles ranks files by how well they match the request and returns at most
// limit of them, each followed by its related tests. Matching is by path words (T08)
// plus verbatim identifiers in source contents, so "добавь кэш в ProductRepository"
// finds the files that declare or use ProductRepository even with no translation;
// symbol-level roles come in T17.
func relevantFiles(ctx context.Context, root string, files []string, q queryTokens, limit int) ([]domain.RelevantFile, error) {
	if q.empty() || limit <= 0 {
		return nil, nil
	}

	var cands []candidate
	scannedFiles, scannedBytes := 0, 0
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if isVendored(f) {
			continue
		}
		score := 2*countMatches(q.words, pathTokens(f)) + countMatches(q.words, pathTokens(path.Base(f)))
		if len(q.identifiers) > 0 && isSource(f) && scannedFiles < maxContentScans && scannedBytes < maxContentBytes {
			if content, ok := readText(root, f); ok {
				scannedFiles++
				scannedBytes += len(content)
				for _, id := range q.identifiers {
					if strings.Contains(content, id) {
						score += identifierWeight
					}
				}
			}
		}
		if score > 0 {
			cands = append(cands, candidate{f, score})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].path < cands[j].path
	})

	testsByKey := map[string][]string{}
	for _, f := range files {
		if isTestFile(f) && !isVendored(f) {
			k := testKey(f)
			testsByKey[k] = append(testsByKey[k], f)
		}
	}

	added := map[string]bool{}
	var out []domain.RelevantFile
	add := func(f string) {
		if added[f] || len(out) >= limit {
			return
		}
		content, ok := readText(root, f)
		if !ok {
			return
		}
		added[f] = true
		rf := domain.RelevantFile{Path: f, Summary: summarize(f, content)}
		if isTestFile(f) {
			rf.Purpose = domain.PurposeTest
		}
		out = append(out, rf)
	}
	for _, c := range cands {
		add(c.path)
		if !isTestFile(c.path) {
			for _, t := range testsByKey[sourceKey(c.path)] {
				add(t)
			}
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func isVendored(f string) bool {
	for _, seg := range strings.Split(f, "/") {
		if vendoredDirs[strings.ToLower(seg)] {
			return true
		}
	}
	return false
}

func isSource(f string) bool {
	_, ok := languageByExt[strings.ToLower(extOf(f))]
	return ok
}

var testMarkerRe = regexp.MustCompile(`(?i:^test_|_test$|\.test$|\.spec$)|Tests?$`)

// sourceKey and testKey map a source file and its tests to the same key: directory
// plus the stem without test markers (repo.go ~ repo_test.go, foo.ts ~ foo.test.ts,
// Foo.java ~ FooTest.java).
func sourceKey(f string) string {
	dir, base := path.Split(f)
	return dir + strings.ToLower(strings.TrimSuffix(base, extOf(base)))
}

func testKey(f string) string {
	dir, base := path.Split(f)
	stem := strings.TrimSuffix(base, extOf(base))
	return dir + strings.ToLower(testMarkerRe.ReplaceAllString(stem, ""))
}

var (
	commentLineRe = regexp.MustCompile(`^\s*(//+|#+|--|;+|/\*+|\*+/?|"""|''')\s?`)
	skipLineRe    = regexp.MustCompile(`(?i)(copyright|license|spdx|^#!|coding[:=]|go:build|\+build|eslint|prettier|@ts-)`)
	importLineRe  = regexp.MustCompile(`^\s*(package|import|from\s+\S+\s+import|use\s|using\s|#include|require\(|const\s+\w+\s*=\s*require)`)
)

// summarize describes a file in one line: its first comment block (a Go package doc,
// a module docstring, the doc comment of the first declaration), else its first code
// lines. Import blocks, build tags and license headers are skipped.
func summarize(f, content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) > summaryScanLines {
		lines = lines[:summaryScanLines]
	}
	if strings.EqualFold(extOf(f), ".md") {
		for _, line := range lines {
			if m := headingRe.FindStringSubmatch(line); m != nil {
				return clip(m[1], maxSummaryLen)
			}
		}
	}

	var comment, code []string
	inImport := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case inImport:
			inImport = trimmed != ")"
			continue
		case strings.HasPrefix(trimmed, "import ("):
			inImport = true
			continue
		case trimmed == "" || skipLineRe.MatchString(trimmed):
			if len(comment) > 0 && trimmed == "" {
				return clip(strings.Join(comment, " "), maxSummaryLen)
			}
			continue
		case commentLineRe.MatchString(line):
			if text := strings.TrimSpace(commentLineRe.ReplaceAllString(line, "")); text != "" {
				comment = append(comment, text)
			}
			continue
		}
		// A code line ends a comment block: that block was the doc of this line.
		if len(comment) > 0 {
			return clip(strings.Join(comment, " "), maxSummaryLen)
		}
		if !importLineRe.MatchString(trimmed) && len(code) < 2 {
			code = append(code, trimmed)
		}
	}
	if len(comment) > 0 {
		return clip(strings.Join(comment, " "), maxSummaryLen)
	}
	return clip(strings.Join(code, " "), maxSummaryLen)
}
