package tools

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fixture is a repository with every case of SPEC §11's confinement list, and a directory
// outside it.
func fixture(t *testing.T) (*Root, string) {
	t.Helper()
	base := t.TempDir()
	repo, outside := filepath.Join(base, "repo"), filepath.Join(base, "outside")
	files := map[string]string{
		"a.go":                           "package a\n\nfunc Add(a, b int) int { return a + b }\n",
		"sub/b.go":                       "package sub\n\nfunc B() {}\n",
		".env":                           "SECRET=1\n",
		"config/credentials.json":        `{"SECRET": 1}` + "\n",
		"secrets/.env.local":             "SECRET=2\n",
		"deep/x/server.PEM":              "SECRET\n",
		".git/config":                    "[core]\n\tSECRET = 3\n",
		".claude/settings.json":          "{}\n",
		".orchestrator/runs/r1/run.json": `{"SECRET": 4}` + "\n",
		"bin.dat":                        "SECRET\x00\x01",
	}
	for rel, content := range files {
		write(t, filepath.Join(repo, rel), content)
	}
	write(t, filepath.Join(repo, "big.txt"), strings.Repeat("x", maxFileBytes+1))
	write(t, filepath.Join(outside, "x.txt"), "SECRET outside\nfunc Outside() {}\n")
	for link, target := range map[string]string{
		"link_in":     "sub/b.go",
		"link_out":    filepath.Join(outside, "x.txt"),
		"linkdir_in":  "sub",
		"linkdir_out": outside,
		"link_to_env": ".env",
		"link_to_git": ".git/config",
	} {
		if err := os.Symlink(target, filepath.Join(repo, link)); err != nil {
			t.Fatal(err)
		}
	}
	root, err := NewRoot(repo, []string{".orchestrator/runs"})
	if err != nil {
		t.Fatal(err)
	}
	return root, repo
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolve(t *testing.T) {
	root, repo := fixture(t)
	for _, p := range []string{"a.go", "./sub/../a.go", "sub/b.go", "link_in", "linkdir_in/b.go", ".claude/settings.json", "", "."} {
		if _, _, err := root.Resolve(p, false); err != nil {
			t.Errorf("read %q: refused: %v", p, err)
		}
	}
	for _, p := range []string{
		"/etc/hosts", filepath.Join(repo, "a.go"), "~/x", "..", "../outside/x.txt", "sub/../../outside/x.txt",
		".env", ".ENV", "config/credentials.json", "secrets/.env.local", "deep/x/server.PEM",
		".git", ".git/config", ".GIT/config", "sub/.git/x", ".orchestrator/runs/r1/run.json",
		"link_out", "linkdir_out/x.txt", "link_to_env", "link_to_git",
	} {
		_, _, err := root.Resolve(p, false)
		var r *refusal
		if !errors.As(err, &r) {
			t.Errorf("read %q: want a refusal, got %v", p, err)
		} else if strings.Contains(err.Error(), filepath.Dir(repo)) && p != filepath.Join(repo, "a.go") {
			t.Errorf("read %q: the refusal names the root's location: %v", p, err)
		}
	}
	for _, p := range []string{"sub/new.go", "newdir/deeper/new.go", "a.go"} {
		if _, _, err := root.Resolve(p, true); err != nil {
			t.Errorf("write %q: refused: %v", p, err)
		}
	}
	for _, p := range []string{".claude/settings.json", ".Claude/x", "link_in", "linkdir_out/new.txt", ".git/hooks/pre-commit", ".env.new"} {
		if _, _, err := root.Resolve(p, true); err == nil {
			t.Errorf("write %q: want a refusal", p)
		}
	}
}

func run(t *testing.T, tool Tool, input string) (string, error) {
	t.Helper()
	return tool.Run(context.Background(), json.RawMessage(input))
}

func byName(r *Root, name string) Tool {
	for _, tool := range ReadTools(r) {
		if tool.Name == name {
			return tool
		}
	}
	panic(name)
}

func TestReadFile(t *testing.T) {
	root, repo := fixture(t)
	read := byName(root, "read_file")
	out, err := run(t, read, `{"path": "a.go", "offset": null, "limit": null}`)
	if err != nil || out != "     1\tpackage a\n     2\t\n     3\tfunc Add(a, b int) int { return a + b }\n" {
		t.Fatalf("read a.go: %q, %v", out, err)
	}
	if out, err := run(t, read, `{"path": "a.go", "offset": 2, "limit": 1}`); err != nil || out != "     2\t\n[1 more lines; read on with offset 3]\n" {
		t.Errorf("offset/limit: %q, %v", out, err)
	}
	if out, err := run(t, read, `{"path": "link_in", "offset": null, "limit": null}`); err != nil || !strings.Contains(out, "func B()") {
		t.Errorf("a symlink inside the root is read: %q, %v", out, err)
	}
	for input, want := range map[string]string{
		`{"path": "big.txt", "offset": null, "limit": null}`:      "over the 1048576-byte limit",
		`{"path": "bin.dat", "offset": null, "limit": null}`:      "binary file",
		`{"path": "sub", "offset": null, "limit": null}`:          "is a directory",
		`{"path": "nope.go", "offset": null, "limit": null}`:      "no such file",
		`{"path": ".env", "offset": null, "limit": null}`:         "not accessible",
		`{"path": "a.go", "offset": 9, "limit": null}`:            "past the end",
		`{"path": "a.go", "offset": 0, "limit": null}`:            "must be positive",
		`{"path": "a.go", "offset": null, "limit": null, "x": 1}`: "invalid input",
		`{"path": "", "offset": null, "limit": null}`:             "path is required",
		`{"path": 3, "offset": null, "limit": null}`:              "invalid input",
	} {
		if _, err := run(t, read, input); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", input, want, err)
		}
	}

	// A long result is cut with a marker, at a line boundary.
	write(t, filepath.Join(repo, "long.txt"), strings.Repeat(strings.Repeat("y", 99)+"\n", 2000))
	out, err = run(t, read, `{"path": "long.txt", "offset": null, "limit": null}`)
	if err != nil || len(out) > maxResultBytes+200 || !strings.Contains(out, "[cut: the result is") {
		t.Errorf("long result: %d bytes, %v", len(out), err)
	}
}

func TestGrep(t *testing.T) {
	root, repo := fixture(t)
	grep := byName(root, "grep")
	// SECRET is in every file the call may not read, and in none it may.
	if out, err := run(t, grep, `{"pattern": "SECRET", "path": null, "glob": null}`); err != nil || out != "no matches\n" {
		t.Errorf("grep SECRET leaked: %q, %v", out, err)
	}
	out, err := run(t, grep, `{"pattern": "^func", "path": null, "glob": null}`)
	if err != nil || out != "a.go:3:func Add(a, b int) int { return a + b }\nsub/b.go:3:func B() {}\n" {
		t.Errorf("grep ^func: %q, %v", out, err)
	}
	if out, _ := run(t, grep, `{"pattern": "func", "path": "sub", "glob": "*.go"}`); out != "sub/b.go:3:func B() {}\n" {
		t.Errorf("grep in sub: %q", out)
	}
	if out, _ := run(t, grep, `{"pattern": "func", "path": null, "glob": "b.*"}`); out != "sub/b.go:3:func B() {}\n" {
		t.Errorf("grep with a glob: %q", out)
	}
	for input, want := range map[string]string{
		`{"pattern": "(", "path": null, "glob": null}`:          "not an RE2",
		`{"pattern": "x", "path": "../outside", "glob": null}`:  "leaves the repository",
		`{"pattern": "x", "path": "linkdir_out", "glob": null}`: "leaves the repository",
		`{"pattern": "x", "path": null, "glob": "["}`:           "malformed",
	} {
		if _, err := run(t, grep, input); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", input, want, err)
		}
	}
	write(t, filepath.Join(repo, "many.txt"), strings.Repeat("hit\n", maxMatches+10))
	if out, _ := run(t, grep, `{"pattern": "hit", "path": "many.txt", "glob": null}`); !strings.Contains(out, "[stopped at 2000 matches") {
		t.Errorf("grep did not stop at the bound: %d bytes", len(out))
	}
}

func TestGlob(t *testing.T) {
	root, _ := fixture(t)
	glob := byName(root, "glob")
	if out, err := run(t, glob, `{"pattern": "**/*.go"}`); err != nil || out != "a.go\nsub/b.go\n" {
		t.Errorf("**/*.go: %q, %v", out, err)
	}
	out, err := run(t, glob, `{"pattern": "**"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{".env", "credentials", ".git", "server.PEM", ".orchestrator", "link_", "x.txt"} {
		if strings.Contains(out, hidden) {
			t.Errorf("** lists %s:\n%s", hidden, out)
		}
	}
	if !strings.Contains(out, ".claude/settings.json") || !strings.Contains(out, "big.txt") {
		t.Errorf("** misses readable files:\n%s", out)
	}
	if out, _ := run(t, glob, `{"pattern": "sub/*"}`); out != "sub/b.go\n" {
		t.Errorf("sub/*: %q", out)
	}
	for _, p := range []string{`{"pattern": "../*"}`, `{"pattern": "/etc/*"}`, `{"pattern": ""}`, `{"pattern": "["}`} {
		if _, err := run(t, glob, p); err == nil {
			t.Errorf("%s: want a refusal", p)
		}
	}
}

func TestGlobMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"**/*.go", "a.go", true}, {"**/*.go", "x/y/z.go", true}, {"internal/**/*_test.go", "internal/a/b/c_test.go", true},
		{"internal/**/*_test.go", "internal/c_test.go", true}, {"*.go", "x/a.go", false}, {"**", "a/b", true},
		{"a/**/b", "a/b", true}, {"a/**/b", "a/x/c", false},
	} {
		if got := globMatch(strings.Split(tc.pattern, "/"), strings.Split(tc.path, "/")); got != tc.want {
			t.Errorf("%s ~ %s = %v", tc.pattern, tc.path, got)
		}
	}
}

// Every schema is strict-ready: an object, no extra properties, every property required.
func TestSchemas(t *testing.T) {
	root, _ := fixture(t)
	tools := ReadTools(root)
	for i, tool := range tools {
		if i > 0 && tools[i-1].Name >= tool.Name {
			t.Errorf("tools are not sorted by name: %s before %s", tools[i-1].Name, tool.Name)
		}
		s := tool.Schema
		props, _ := s["properties"].(map[string]any)
		required, _ := s["required"].([]string)
		if s["type"] != "object" || s["additionalProperties"] != false || len(required) != len(props) {
			t.Errorf("%s: schema %v", tool.Name, s)
		}
		if _, err := json.Marshal(s); err != nil {
			t.Errorf("%s: %v", tool.Name, err)
		}
	}
}

func TestCancelledWalk(t *testing.T) {
	root, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := byName(root, "grep").Run(ctx, json.RawMessage(`{"pattern": "x", "path": null, "glob": null}`)); !errors.Is(err, context.Canceled) {
		t.Errorf("want the context's error, got %v", err)
	}
}

// No tool can run a command or open a connection (SPEC §5 item 1).
func TestImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".go" || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			switch {
			case p == "os/exec", p == "net", strings.HasPrefix(p, "net/"), p == "syscall", p == "plugin", p == "unsafe":
				t.Errorf("%s imports %q: the tools run no command and open no connection", e.Name(), p)
			case strings.HasPrefix(p, "orchestrator/") && p != "orchestrator/internal/projectctx":
				t.Errorf("%s imports %q: tools may import only projectctx and the standard library", e.Name(), p)
			}
		}
	}
}
