package story

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestImportsOnlyDomainAndStdlib enforces the rule of specs/story/README.md: the package is
// pure, so it imports the standard library and, if it ever needs it, domain, and nothing else.
// The sources that ship (not the tests, which read golden files) also stay off os, os/exec
// and the network, which is where git, a process or a file would come from.
func TestImportsOnlyDomainAndStdlib(t *testing.T) {
	banned := map[string]bool{"os": true, "os/exec": true, "net": true, "net/http": true, "syscall": true, "io/ioutil": true}
	fset := token.NewFileSet()
	files := 0

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		files++
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			return nil
		}
		isTest := strings.HasSuffix(path, "_test.go")
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Errorf("%s: bad import %s", path, imp.Path.Value)
				continue
			}
			first, _, _ := strings.Cut(p, "/")
			switch {
			case p == "orchestrator/internal/domain":
			case strings.HasPrefix(first, "orchestrator") || strings.Contains(first, "."):
				t.Errorf("%s imports %q: internal/story may import only domain and the standard library", path, p)
			case !isTest && banned[p]:
				t.Errorf("%s imports %q: internal/story does no I/O", path, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("found no Go files to check")
	}
}
