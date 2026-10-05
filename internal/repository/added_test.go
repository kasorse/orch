package repository

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func addedByPath(t *testing.T, files []AddedFile) map[string]AddedFile {
	t.Helper()
	m := map[string]AddedFile{}
	for _, f := range files {
		if _, dup := m[f.Path]; dup {
			t.Errorf("%s listed twice", f.Path)
		}
		m[f.Path] = f
	}
	return m
}

func TestAddedContent(t *testing.T) {
	repo, dir := realRepo(t)
	writeFile(t, filepath.Join(dir, "cfg.go"), "package a\n\nconst A = 1\nconst B = 2\n")
	writeFile(t, filepath.Join(dir, "gone.go"), "package a\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "*.local\n")
	writeFile(t, filepath.Join(dir, "with space.txt"), "x\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", "base")

	// Tracked: one line changed in the middle, one added at the end; one file deleted.
	writeFile(t, filepath.Join(dir, "cfg.go"), "package a\n\nconst A = 10\nconst B = 2\nconst C = 3\n")
	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "with space.txt"), "x\ny\n")
	// Untracked: a new file, an ignored one, a binary one, the runs directory, a symlink.
	writeFile(t, filepath.Join(dir, "new", "n.go"), "package n\nvar K = 1")
	writeFile(t, filepath.Join(dir, "secret.local"), "ignored\n")
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("a\x00b"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".orchestrator", "runs", "r", "run.json"), "{}")
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	files, err := repo.AddedContent(context.Background(), []string{".orchestrator/runs"})
	if err != nil {
		t.Fatal(err)
	}
	got := addedByPath(t, files)

	cfg := got["cfg.go"]
	if len(cfg.Lines) != 2 || cfg.Lines[0] != (AddedLine{3, "const A = 10"}) || cfg.Lines[1] != (AddedLine{5, "const C = 3"}) {
		t.Errorf("cfg.go lines = %+v", cfg.Lines)
	}
	if f := got["with space.txt"]; len(f.Lines) != 1 || f.Lines[0] != (AddedLine{2, "y"}) {
		t.Errorf("a path with a space: %+v", f)
	}
	if n := got["new/n.go"]; len(n.Lines) != 2 || n.Lines[1] != (AddedLine{2, "var K = 1"}) || n.Skipped {
		t.Errorf("new/n.go = %+v", n)
	}
	if !got["blob.bin"].Skipped || !got["link"].Skipped {
		t.Errorf("binary or symlink not skipped: %+v %+v", got["blob.bin"], got["link"])
	}
	for _, p := range []string{"gone.go", "secret.local", ".orchestrator/runs/r/run.json"} {
		if _, ok := got[p]; ok {
			t.Errorf("%s must not be scanned", p)
		}
	}
}

func TestParseAddedLines_QuotedPathAndBinary(t *testing.T) {
	patch := "diff --git \"a/caf\\303\\251.go\" \"b/caf\\303\\251.go\"\n" +
		"--- \"a/caf\\303\\251.go\"\n+++ \"b/caf\\303\\251.go\"\n@@ -1,0 +2 @@\n+++ not a header\n" +
		"diff --git a/logo.png b/logo.png\nBinary files a/logo.png and b/logo.png differ\n"
	files, err := parseAddedLines(patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "café.go" || len(files[0].Lines) != 1 || files[0].Lines[0] != (AddedLine{2, "++ not a header"}) {
		t.Errorf("files = %+v", files)
	}
	if _, err := parseAddedLines("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ garbage @@\n"); err == nil || !strings.Contains(err.Error(), "hunk header") {
		t.Errorf("a malformed hunk header must be an error, got %v", err)
	}
}
