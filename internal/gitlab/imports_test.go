package gitlab

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestImportsOnlyDomainApperrAndStdlib enforces specs/revise/SPEC.md §3: the client imports only
// domain, apperr and the standard library.
func TestImportsOnlyDomainApperrAndStdlib(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files: %v", err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(p, "/")
			switch p {
			case "github.com/kasorse/orch/internal/domain", "github.com/kasorse/orch/internal/apperr":
				continue
			}
			if strings.Contains(first, ".") {
				t.Errorf("%s imports %q: internal/gitlab may import only domain, apperr and the standard library", path, p)
			}
		}
	}
}

// TestSendsOnlyGET enforces the read-only rule: no source that ships names another HTTP method.
func TestSendsOnlyGET(t *testing.T) {
	other := regexp.MustCompile(`http\.Method(Post|Put|Patch|Delete|Head|Options|Connect|Trace)|"(POST|PUT|PATCH|DELETE)"`)
	files, _ := filepath.Glob("*.go")
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if m := other.Find(src); m != nil {
			t.Errorf("%s names %s: the GitLab client sends only GET", path, m)
		}
		if n := strings.Count(string(src), "http.NewRequest"); n != 1 {
			t.Errorf("%s builds %d requests; the one in once (a GET) is the only one allowed", path, n)
		}
	}
}
