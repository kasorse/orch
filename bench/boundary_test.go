package bench_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoImportOfTheCore enforces the black-box rule of specs/benchmark/SPEC.md §3 and §4 a
// second time. The module path `orchbench` already makes orchestrator/internal/... a compile
// error; this catches a replace directive or a vendored copy that would work around it.
// It reads the sources, so it needs no go command and no network.
func TestNoImportOfTheCore(t *testing.T) {
	fset := token.NewFileSet()
	files := 0

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != "." && (strings.HasPrefix(name, ".") || name == "testdata") {
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
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Errorf("%s: bad import %s", path, imp.Path.Value)
				continue
			}
			if strings.HasPrefix(p, "orchestrator") {
				t.Errorf("%s imports %q: bench is a black box and must not import the core", path, p)
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
