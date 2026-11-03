package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func editor(t *testing.T, root *Root, input string) (string, error) {
	t.Helper()
	return EditorTool(root).Run(context.Background(), json.RawMessage(input))
}

func content(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEditor(t *testing.T) {
	root, repo := fixture(t)
	if out, err := editor(t, root, `{"command":"view","path":"a.go","view_range":[3,-1]}`); err != nil || out != "     3\tfunc Add(a, b int) int { return a + b }\n" {
		t.Errorf("view range: %q, %v", out, err)
	}
	if out, err := editor(t, root, `{"command":"view","path":"."}`); err != nil || !strings.Contains(out, "sub/b.go") || strings.Contains(out, ".env") || strings.Contains(out, ".git") {
		t.Errorf("view of the root: %q, %v", out, err)
	}
	if _, err := editor(t, root, `{"command":"str_replace","path":"a.go","old_str":"a + b","new_str":"a - b"}`); err != nil {
		t.Fatal(err)
	}
	if got := content(t, filepath.Join(repo, "a.go")); !strings.Contains(got, "return a - b") {
		t.Errorf("str_replace: %q", got)
	}
	if _, err := editor(t, root, `{"command":"insert","path":"a.go","insert_line":1,"insert_text":"// added"}`); err != nil {
		t.Fatal(err)
	}
	if got := content(t, filepath.Join(repo, "a.go")); !strings.HasPrefix(got, "package a\n// added\n\nfunc") {
		t.Errorf("insert: %q", got)
	}
	if _, err := editor(t, root, `{"command":"create","path":"new/dir/c.go","file_text":"package c\n"}`); err != nil {
		t.Fatal(err)
	}
	if got := content(t, filepath.Join(repo, "new/dir/c.go")); got != "package c\n" {
		t.Errorf("create: %q", got)
	}
	for input, want := range map[string]string{
		`{"command":"str_replace","path":"a.go","old_str":"nothing like it","new_str":"x"}`: "was not found",
		`{"command":"str_replace","path":"sub/b.go","old_str":"u","new_str":"x"}`:           "occurs 2 times",
		`{"command":"str_replace","path":"a.go","old_str":"","new_str":"x"}`:                "non-empty old_str",
		`{"command":"insert","path":"a.go","insert_line":99,"insert_text":"x"}`:             "insert_line must be",
		`{"command":"undo_edit","path":"a.go"}`:                                             "not available",
		`{"command":"rm","path":"a.go"}`:                                                    "unknown command",
		`{"command":"create","path":".env","file_text":"x"}`:                                "not accessible",
		`{"command":"create","path":".git/hooks/pre-commit","file_text":"#!/bin/sh"}`:       ".git is not accessible",
		`{"command":"create","path":".claude/settings.json","file_text":"{}"}`:              ".claude is not writable",
		`{"command":"create","path":"linkdir_out/evil.txt","file_text":"x"}`:                "leaves the repository",
		`{"command":"str_replace","path":"link_in","old_str":"B","new_str":"C"}`:            "symlink is not written through",
		`{"command":"create","path":"../escape.txt","file_text":"x"}`:                       "leaves the repository",
		`{"command":"create","path":"sub","file_text":"x"}`:                                 "is a directory",
		`{"command":"view","path":"bin.dat"}`:                                               "binary",
	} {
		if _, err := editor(t, root, input); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", input, want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(repo), "outside", "evil.txt")); err == nil {
		t.Error("a file was written outside the repository")
	}
	if got := content(t, filepath.Join(repo, "sub/b.go")); got != "package sub\n\nfunc B() {}\n" {
		t.Errorf("a refused edit changed sub/b.go: %q", got)
	}
}

func TestApplyPatch(t *testing.T) {
	root, repo := fixture(t)
	write(t, filepath.Join(repo, "m.go"), "package m\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 1\n}\n")
	// Two hunks, the second found after the first; the anchor narrows to B.
	diff := "@@ func B() int {\n-\treturn 1\n+\treturn 2\n"
	if _, err := root.ApplyPatch(PatchOp{Type: "update_file", Path: "m.go", Diff: diff}); err != nil {
		t.Fatal(err)
	}
	if got := content(t, filepath.Join(repo, "m.go")); got != "package m\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n" {
		t.Errorf("anchored update: %q", got)
	}
	diff = " func A() int {\n-\treturn 1\n+\treturn 10\n }\n@@\n func B() int {\n-\treturn 2\n+\treturn 20\n"
	if _, err := root.ApplyPatch(PatchOp{Type: "update_file", Path: "m.go", Diff: diff}); err != nil {
		t.Fatal(err)
	}
	if got := content(t, filepath.Join(repo, "m.go")); !strings.Contains(got, "return 10") || !strings.Contains(got, "return 20") {
		t.Errorf("two hunks: %q", got)
	}
	// Whitespace-tolerant context; an addition at the end.
	diff = "  func A() int {   \n-\treturn 10\n+\treturn 11\n"
	if _, err := root.ApplyPatch(PatchOp{Type: "update_file", Path: "m.go", Diff: diff}); err != nil {
		t.Fatalf("fuzzy context: %v", err)
	}
	if _, err := root.ApplyPatch(PatchOp{Type: "update_file", Path: "m.go", Diff: "@@\n+// end\n*** End of File\n"}); err != nil {
		t.Fatal(err)
	}
	if got := content(t, filepath.Join(repo, "m.go")); !strings.HasSuffix(got, "}\n// end\n") || !strings.Contains(got, "return 11") {
		t.Errorf("eof addition: %q", got)
	}
	// A hunk that does not match changes nothing, the earlier hunk of the same diff included.
	before := content(t, filepath.Join(repo, "m.go"))
	diff = "-\treturn 11\n+\treturn 12\n@@\n-\tno such line\n+\tx\n"
	if _, err := root.ApplyPatch(PatchOp{Type: "update_file", Path: "m.go", Diff: diff}); err == nil || !strings.Contains(err.Error(), "hunk 2") || !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("want hunk 2 refused, got %v", err)
	}
	if content(t, filepath.Join(repo, "m.go")) != before {
		t.Error("a failed patch changed the file")
	}
	// CRLF is kept.
	write(t, filepath.Join(repo, "w.txt"), "one\r\ntwo\r\n")
	if _, err := root.ApplyPatch(PatchOp{Type: "update_file", Path: "w.txt", Diff: "-two\n+2\n"}); err != nil {
		t.Fatal(err)
	}
	if got := content(t, filepath.Join(repo, "w.txt")); got != "one\r\n2\r\n" {
		t.Errorf("crlf: %q", got)
	}
	// Create and delete.
	if _, err := root.ApplyPatch(PatchOp{Type: "create_file", Path: "pkg/n.go", Diff: "+package n\n+\n+func N() {}"}); err != nil {
		t.Fatal(err)
	}
	if got := content(t, filepath.Join(repo, "pkg/n.go")); got != "package n\n\nfunc N() {}\n" {
		t.Errorf("create: %q", got)
	}
	if _, err := root.ApplyPatch(PatchOp{Type: "delete_file", Path: "pkg/n.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "pkg/n.go")); err == nil {
		t.Error("delete_file left the file")
	}
	for _, tc := range []struct {
		op   PatchOp
		want string
	}{
		{PatchOp{Type: "create_file", Path: "x.go", Diff: "package x"}, "does not start with +"},
		{PatchOp{Type: "update_file", Path: "m.go", Diff: "+only added"}, "without context"},
		{PatchOp{Type: "update_file", Path: "m.go", Diff: "@@ func Nope() {\n-x\n+y"}, "anchor"},
		{PatchOp{Type: "update_file", Path: "m.go", Diff: "?bad line"}, "does not start with"},
		{PatchOp{Type: "update_file", Path: ".env", Diff: "-SECRET=1\n+SECRET=2"}, "not accessible"},
		{PatchOp{Type: "delete_file", Path: ".git/config"}, ".git is not accessible"},
		{PatchOp{Type: "delete_file", Path: "link_in"}, "symlink is not written through"},
		{PatchOp{Type: "delete_file", Path: "nope.go"}, "no such file"},
		{PatchOp{Type: "create_file", Path: "linkdir_out/x.go", Diff: "+x"}, "leaves the repository"},
		{PatchOp{Type: "rename_file", Path: "m.go"}, "unknown operation"},
	} {
		if _, err := root.ApplyPatch(tc.op); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: want %q, got %v", tc.op, tc.want, err)
		}
	}
	if content(t, filepath.Join(repo, ".env")) != "SECRET=1\n" {
		t.Error(".env was changed")
	}
}
