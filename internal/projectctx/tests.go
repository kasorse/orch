package projectctx

import (
	"regexp"
	"strings"

	"github.com/kasorse/orch/internal/domain"
)

const maxBuildTagScans = 200

var (
	// testFileRe covers the common per-language conventions: foo_test.go,
	// test_foo.py / foo_test.py, foo.test.ts / foo.spec.js, FooTest.java / FooTests.kt.
	testFileRe = regexp.MustCompile(`(?i)(^|/)(test_[^/]+\.py|[^/]+_test\.(go|py)|[^/]+\.(test|spec)\.[jt]sx?|[^/]+Tests?\.(java|kt|cs))$`)
	// testDirRe: files under a tests/ directory (or Maven's src/test/).
	testDirRe = regexp.MustCompile(`(?i)(^|/)(tests?|__tests__|spec)/`)
	// integrationRe marks integration or end-to-end tests by path.
	integrationRe = regexp.MustCompile(`(?i)(^|[/_.-])(integration|e2e|it)([/_.-]|$)`)
	// goBuildTagRe finds `//go:build integration` (possibly with other terms).
	goBuildTagRe = regexp.MustCompile(`(?m)^//go:build\s+.*\b(integration|e2e)\b`)
)

func isTestFile(rel string) bool {
	return testFileRe.MatchString(rel) || testDirRe.MatchString(rel)
}

// testInfo reports whether the project has tests and of which kind, judged by paths
// and, for Go, by integration build tags.
func testInfo(root string, files []string) domain.TestInfo {
	var info domain.TestInfo
	scanned := 0
	for _, f := range files {
		if !isTestFile(f) {
			continue
		}
		info.Exists = true
		integration := integrationRe.MatchString(f)
		if !integration && strings.HasSuffix(f, "_test.go") && scanned < maxBuildTagScans {
			scanned++
			if content, ok := readText(root, f); ok {
				integration = goBuildTagRe.MatchString(content[:min(len(content), 1024)])
			}
		}
		if integration {
			info.IntegrationTests = true
		} else {
			info.UnitTests = true
		}
	}
	return info
}
