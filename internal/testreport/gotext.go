package testreport

import (
	"regexp"
	"strings"
)

var (
	// goFailRE is a failed test or subtest; a subtest's name already includes its parent
	// (TestTable/empty).
	goFailRE = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)
	// goPkgFailRE is the line that ends a failed package: FAIL<TAB>pkg<TAB>0.004s, or
	// FAIL<TAB>pkg [build failed].
	goPkgFailRE = regexp.MustCompile(`^FAIL\t(\S+)`)
	// goMarkerRE is any line only `go test` prints; one of them makes the output go text.
	goMarkerRE = regexp.MustCompile(`^(\s*--- (FAIL|PASS|SKIP): |=== (RUN|PAUSE|CONT)\s|FAIL\t|ok  \t)`)
)

// parseGoText reads the plain output of `go test`. A package prints its failed tests and
// then the `FAIL<TAB>pkg` line that names it, so the names seen since the last package line
// belong to the next one. Names seen before the output was cut keep no package.
func parseGoText(in input) ([]string, bool) {
	var names, pending []string
	flush := func(pkg string) {
		for _, p := range pending {
			names = append(names, joinName(".", pkg, p))
		}
		pending = nil
	}
	recognised := false
	for _, line := range in.lines {
		if goMarkerRE.MatchString(line) {
			recognised = true
		}
		if m := goFailRE.FindStringSubmatch(line); m != nil {
			pending = append(pending, m[1])
		} else if m := goPkgFailRE.FindStringSubmatch(line); m != nil {
			flush(m[1])
		} else if strings.HasPrefix(line, "ok  \t") {
			flush("")
		}
	}
	flush("")
	return names, recognised
}
