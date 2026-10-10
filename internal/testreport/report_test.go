package testreport

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestParse_Golden runs every testdata/<name>.txt through Parse and compares the result with
// testdata/<name>.golden: the format on the first line, then one failing test per line.
func TestParse_Golden(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "*.txt"))
	if err != nil || len(inputs) == 0 {
		t.Fatalf("no testdata: %v", err)
	}
	for _, in := range inputs {
		name := strings.TrimSuffix(filepath.Base(in), ".txt")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			golden, err := os.ReadFile(strings.TrimSuffix(in, ".txt") + ".golden")
			if err != nil {
				t.Fatal(err)
			}
			wantLines := strings.Split(strings.TrimSuffix(string(golden), "\n"), "\n")
			wantFormat, wantNames := wantLines[0], []string(nil)
			for _, l := range wantLines[1:] {
				wantNames = append(wantNames, l)
			}

			got := Parse(string(raw), "")
			if got.Format != wantFormat || !slices.Equal(got.Failed, wantNames) {
				t.Errorf("Parse() = {%q, %q}, want {%q, %q}", got.Format, got.Failed, wantFormat, wantNames)
			}
			// stdout and stderr are one text, in either stream.
			if swapped := Parse("", string(raw)); swapped.Format != got.Format || !slices.Equal(swapped.Failed, got.Failed) {
				t.Errorf("Parse(\"\", output) = %+v, differs from Parse(output, \"\") = %+v", swapped, got)
			}
		})
	}
}

func TestParse_UnknownFormatHasNoNames(t *testing.T) {
	for _, out := range []string{"", "\n", "FAIL", "exit status 1", "{\"a\": 1}", "--- not a test line"} {
		if got := Parse(out, ""); got.Format != "" || len(got.Failed) != 0 {
			t.Errorf("Parse(%q) = %+v, want an empty report", out, got)
		}
	}
}

func TestParse_JoinsStdoutAndStderr(t *testing.T) {
	got := Parse(`{"Action":"fail","Package":"p","Test":"TestA"}`, `{"Action":"fail","Package":"p","Test":"TestB"}`)
	if want := []string{"p.TestA", "p.TestB"}; got.Format != FormatGoJSON || !slices.Equal(got.Failed, want) {
		t.Errorf("Parse() = %+v, want %s %q", got, FormatGoJSON, want)
	}
}

// TestParse_FixedOrder: the first format that recognises the output wins, whatever else the
// text contains.
func TestParse_FixedOrder(t *testing.T) {
	goJSON := `{"Action":"fail","Package":"p","Test":"TestJSON"}` + "\n"
	jestJSON := `{"testResults":[{"assertionResults":[{"status":"failed","title":"j"}],"name":"f.js"}]}` + "\n"
	pytest := "=== short test summary info ===\nFAILED t.py::test_p\n"
	goText := "--- FAIL: TestText (0s)\nFAIL\tp\t0s\n"
	jestText := " FAIL  src/a.test.js\n  ✕ jt (1 ms)\n"

	for _, tc := range []struct {
		name, out, format string
	}{
		{"go json over everything", goJSON + jestJSON + pytest + goText + jestText, FormatGoJSON},
		{"jest json over the rest", jestJSON + pytest + goText + jestText, FormatJestJSON},
		{"pytest over the text formats", pytest + goText + jestText, FormatPytest},
		{"go text over jest text", goText + jestText, FormatGoText},
		{"jest text last", jestText, FormatJestText},
	} {
		if got := Parse(tc.out, ""); got.Format != tc.format {
			t.Errorf("%s: format = %q, want %q", tc.name, got.Format, tc.format)
		}
	}
}

func TestParse_StripsColour(t *testing.T) {
	out := "\x1b[1m=== short test summary info ===\x1b[0m\n\x1b[31mFAILED\x1b[0m t.py::test_x - boom\n"
	got := Parse(out, "")
	if want := []string{"t.py::test_x"}; got.Format != FormatPytest || !slices.Equal(got.Failed, want) {
		t.Errorf("Parse() = %+v, want %s %q", got, FormatPytest, want)
	}
}

func TestParse_NamesAreBounded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxNames+50; i++ {
		fmt.Fprintf(&b, `{"Action":"fail","Package":"p","Test":"Test%04d"}`+"\n", i)
		// The same failure twice is listed once.
		fmt.Fprintf(&b, `{"Action":"fail","Package":"p","Test":"Test%04d"}`+"\n", i)
	}
	got := Parse(b.String(), "")
	if len(got.Failed) != MaxNames {
		t.Fatalf("%d names, want %d", len(got.Failed), MaxNames)
	}
	if !slices.IsSorted(got.Failed) || got.Failed[0] != "p.Test0000" {
		t.Errorf("names are not the first %d in sorted order: %q ...", MaxNames, got.Failed[:3])
	}

	long := `{"Action":"fail","Package":"p","Test":"` + strings.Repeat("é", 400) + `"}`
	name := Parse(long, "").Failed[0]
	if len(name) > MaxNameBytes || !utf8.ValidString(name) {
		t.Errorf("a long name is %d bytes, valid UTF-8 %v; want at most %d and valid", len(name), utf8.ValidString(name), MaxNameBytes)
	}

	multi := `{"Action":"fail","Package":"p","Test":"a\nb\u0000c"}`
	if got := Parse(multi, "").Failed; len(got) != 1 || strings.ContainsAny(got[0], "\n\x00") {
		t.Errorf("a name with control characters = %q, want one single-line name", got)
	}
}

func TestPytestName(t *testing.T) {
	for _, tc := range [][2]string{
		{"t.py::test_a", "t.py::test_a"},
		{"t.py::test_a - KeyError: 'a'", "t.py::test_a"},
		{"t.py::test_a[x - y] - assert 0", "t.py::test_a[x - y]"},
		{"t.py::test_a[x - y]", "t.py::test_a[x - y]"},
		{"t.py::test_a[unclosed - assert", "t.py::test_a[unclosed - assert"},
		{"  t.py::test_a  ", "t.py::test_a"},
		{"t.py::test_a -not a separator -", "t.py::test_a -not a separator -"},
	} {
		if got := pytestName(tc[0]); got != tc[1] {
			t.Errorf("pytestName(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}

func TestKnownFormat(t *testing.T) {
	for _, f := range []string{FormatGoJSON, FormatJestJSON, FormatPytest, FormatGoText, FormatJestText} {
		if !KnownFormat(f) {
			t.Errorf("KnownFormat(%q) = false", f)
		}
	}
	for _, f := range []string{"", "junit", "GOJSON"} {
		if KnownFormat(f) {
			t.Errorf("KnownFormat(%q) = true", f)
		}
	}
}

func FuzzParse(f *testing.F) {
	seeds, _ := filepath.Glob(filepath.Join("testdata", "*.txt"))
	for _, s := range seeds {
		if raw, err := os.ReadFile(s); err == nil {
			f.Add(string(raw), "")
			f.Add("", string(raw))
			// Every prefix cut at a few places, as a bounded output is.
			for _, n := range []int{len(raw) / 3, len(raw) / 2, len(raw) - 1} {
				if n > 0 && n < len(raw) {
					f.Add(string(raw[:n]), "")
				}
			}
		}
	}
	f.Add(`{"Action":"fail","Test":"T"}`, "FAIL\tp")
	f.Add(`{"testResults":[{"assertionResults":[`, "")
	f.Add("\x1b[", "\xff\xfe")
	f.Fuzz(func(t *testing.T, stdout, stderr string) {
		got := Parse(stdout, stderr)
		if len(got.Failed) > MaxNames {
			t.Fatalf("%d names, at most %d", len(got.Failed), MaxNames)
		}
		if got.Format == "" && len(got.Failed) != 0 {
			t.Fatalf("names without a format: %q", got.Failed)
		}
		if got.Format != "" && !KnownFormat(got.Format) {
			t.Fatalf("unknown format %q", got.Format)
		}
		for _, n := range got.Failed {
			if n == "" || len(n) > MaxNameBytes {
				t.Fatalf("name %q is empty or over %d bytes", n, MaxNameBytes)
			}
		}
		if !slices.IsSorted(got.Failed) {
			t.Fatalf("names are not sorted: %q", got.Failed)
		}
	})
}

// TestImportsOnlyTheStandardLibrary: the package is a leaf (specs/quality/README.md ground
// rules). It may import domain, and today needs nothing from it.
func TestImportsOnlyTheStandardLibrary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files: %v", err)
	}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			// A standard library path has no dot in its first element.
			first, _, _ := strings.Cut(path, "/")
			if path != "orchestrator/internal/domain" && (strings.Contains(first, ".") || first == "orchestrator") {
				t.Errorf("%s imports %q: only domain and the standard library are allowed", file, path)
			}
		}
	}
}
