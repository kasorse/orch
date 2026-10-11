package testreport

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// jestSep joins the parts of a jest test's name: file, suites, title.
const jestSep = " › "

// maxJestJSONStarts bounds how many line-leading '{' parseJestJSON tries as the start of the
// result object.
const maxJestJSONStarts = 8

type jestAssertion struct {
	Status         string   `json:"status"`
	AncestorTitles []string `json:"ancestorTitles"`
	Title          string   `json:"title"`
}

// errNotJest ends a stream that is not shaped like jest's result.
var errNotJest = errors.New("not a jest result")

// parseJestJSON reads the object `jest --json` prints. It reads the stream token by token,
// so a cut output still yields the failures before the cut. The test results are recognised
// by the top-level key "testResults".
func parseJestJSON(in input) ([]string, bool) {
	starts := 0
	for off := 0; off < len(in.text) && starts < maxJestJSONStarts; {
		if in.text[off] == '{' {
			starts++
			if names, ok := streamJest(in.text[off:]); ok {
				return names, true
			}
		}
		next := strings.IndexByte(in.text[off:], '\n')
		if next < 0 {
			break
		}
		off += next + 1
	}
	return nil, false
}

func streamJest(s string) ([]string, bool) {
	dec := json.NewDecoder(strings.NewReader(s))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	var names []string
	found := false
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			break
		}
		if key, _ := keyTok.(string); key != "testResults" {
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				break
			}
			continue
		}
		if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
			break
		}
		found = true
		for dec.More() {
			fileNames, err := streamJestFile(dec)
			names = append(names, fileNames...)
			if err != nil {
				return names, true
			}
		}
		break
	}
	return names, found
}

// streamJestFile reads one element of testResults. jest writes a file's name after its
// assertions, so the names are built when the element ends or the stream breaks; a stream
// cut before the name leaves them without the file.
func streamJestFile(dec *json.Decoder) ([]string, error) {
	var file string
	var titles []string
	build := func() []string {
		out := make([]string, len(titles))
		for i, t := range titles {
			out[i] = joinName(jestSep, file, t)
		}
		return out
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errNotJest
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return build(), err
		}
		key, _ := keyTok.(string)
		switch key {
		case "name":
			if err := dec.Decode(&file); err != nil {
				return build(), err
			}
		case "assertionResults":
			tok, err := dec.Token()
			if err != nil {
				return build(), err
			}
			if d, isDelim := tok.(json.Delim); isDelim {
				if d != '[' {
					return build(), errNotJest
				}
				for dec.More() {
					var a jestAssertion
					if err := dec.Decode(&a); err != nil {
						return build(), err
					}
					if a.Status == "failed" {
						titles = append(titles, joinName(jestSep, append(a.AncestorTitles, a.Title)...))
					}
				}
				if _, err := dec.Token(); err != nil {
					return build(), err
				}
			}
		default:
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return build(), err
			}
		}
	}
	if _, err := dec.Token(); err != nil {
		return build(), err
	}
	return build(), nil
}

var (
	// jestFileRE is the line that opens a test file's block: " FAIL  src/a.test.js", with a
	// duration after the path in newer versions.
	jestFileRE = regexp.MustCompile(`^\s*(?:PASS|FAIL) +(\S.*?)\s*$`)
	// jestTimeRE is the " (3 ms)" after a test's title or a file's path.
	jestTimeRE = regexp.MustCompile(`\s+\(\d+(?:\.\d+)? m?s\)$`)
)

// jestSuite is a describe block on the stack of a file's tree, by indentation.
type jestSuite struct {
	indent int
	title  string
}

// parseJestText reads jest's default reporter. In a file's tree, `✕ title` lines carry the
// failures, and the suites above them are the less indented lines; without the tree (jest
// prints it for a single file or with --verbose) the `● Suite › title` headings of the
// failure details carry them. For one file the tree wins, so a test is not listed twice.
func parseJestText(in input) ([]string, bool) {
	recognised, inTree := false, false
	file := ""
	var stack []jestSuite
	tree := map[string][]string{}
	bullets := map[string][]string{}
	for _, line := range in.lines {
		if m := jestFileRE.FindStringSubmatch(line); m != nil && strings.ContainsAny(m[1], "./") {
			file = jestTimeRE.ReplaceAllString(m[1], "")
			recognised, inTree, stack = true, true, nil
			continue
		}
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		switch {
		case trimmed == "":
		case strings.HasPrefix(trimmed, "●"):
			inTree = false
			title := strings.TrimSpace(strings.TrimPrefix(trimmed, "●"))
			if title != "" && !strings.HasPrefix(title, "Console") && !strings.HasPrefix(title, "Test suite failed to run") &&
				!strings.HasPrefix(title, "Validation Warning") && !strings.HasPrefix(title, "Deprecation Warning") {
				bullets[file] = append(bullets[file], joinName(jestSep, file, title))
			}
		case strings.HasPrefix(trimmed, "✓"), strings.HasPrefix(trimmed, "✕"), strings.HasPrefix(trimmed, "○"), strings.HasPrefix(trimmed, "✎"):
			recognised = true
			stack = popSuites(stack, indent)
			// Past the first failure detail a ✕ is output of the code under test.
			if inTree && strings.HasPrefix(trimmed, "✕") {
				title := strings.TrimSpace(jestTimeRE.ReplaceAllString(strings.TrimSpace(strings.TrimPrefix(trimmed, "✕")), ""))
				parts := []string{file}
				for _, s := range stack {
					parts = append(parts, s.title)
				}
				tree[file] = append(tree[file], joinName(jestSep, append(parts, title)...))
			}
		case inTree:
			stack = append(popSuites(stack, indent), jestSuite{indent, trimmed})
		}
	}

	var names []string
	for f, n := range tree {
		names = append(names, n...)
		delete(bullets, f)
	}
	for _, n := range bullets {
		names = append(names, n...)
	}
	return names, recognised
}

// popSuites drops the suites that are not ancestors of a line at indent.
func popSuites(stack []jestSuite, indent int) []jestSuite {
	for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
		stack = stack[:len(stack)-1]
	}
	return stack
}
