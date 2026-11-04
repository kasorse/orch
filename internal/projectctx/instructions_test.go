package projectctx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

type listRepo struct{ files []string }

func (l listRepo) ListFiles(context.Context) ([]string, error) { return l.files, nil }
func (l listRepo) Branch(context.Context) (string, error)      { return "main", nil }
func (l listRepo) ChangedFiles(context.Context) ([]string, error) {
	return nil, nil
}

func writeDocs(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	var listed []string
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		listed = append(listed, rel)
	}
	return dir, listed
}

func TestDocs_NeverReadsDeniedFiles(t *testing.T) {
	dir, listed := writeDocs(t, map[string]string{
		"credentials/notes.md": "# Export\n\nexport format SECRET-A\n",
		".env.md":              "# Export\n\nexport format SECRET-B\n",
		"docs/export.md":       "# Export\n\nThe export format is CSV.\n",
	})
	ins, err := Docs(context.Background(), dir, DocsOptions{Repo: listRepo{listed}, Globs: []string{"**/*.md"}, MaxBytes: 16384},
		"which export format", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ins.Excerpts) != 1 || ins.Excerpts[0].Path != "docs/export.md" {
		t.Fatalf("excerpts = %+v, want only docs/export.md", ins.Excerpts)
	}
}

func TestDocs_SkipsSymlinksAndVendored(t *testing.T) {
	dir, listed := writeDocs(t, map[string]string{
		"real.md":                  "# Other\n\nnothing\n",
		"vendor/lib/README.md":     "# Export\n\nexport format VENDORED\n",
		"node_modules/x/readme.md": "# Export\n\nexport format VENDORED\n",
	})
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("# Export\n\nexport format LINKED\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.md")); err != nil {
		t.Skip("symlinks unavailable")
	}
	listed = append(listed, "link.md")
	ins, err := Docs(context.Background(), dir, DocsOptions{Repo: listRepo{listed}, Globs: []string{"**/*.md"}, MaxBytes: 16384},
		"export format", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ins.Excerpts) != 0 {
		t.Fatalf("excerpts = %+v, want none", ins.Excerpts)
	}
}

func TestDocs_BudgetKeepsBestExcerpt(t *testing.T) {
	dir, listed := writeDocs(t, map[string]string{
		"AGENTS.md": "# Rules\n\n- Always write tests for every change.\n- Keep functions small.\n",
		"a.md":      "# Export format\n\nThe export format is CSV and the export is fast.\n",
		"b.md":      "# Notes\n\nA format note.\n",
	})
	opts := DocsOptions{Repo: listRepo{listed}, Globs: []string{"*.md"}, MaxBytes: 1}
	ins, err := Docs(context.Background(), dir, opts, "export format", "")
	if err != nil {
		t.Fatal(err)
	}
	if !ins.Truncated || len(ins.Excerpts) != 1 || ins.Excerpts[0].Path != "a.md" || len(ins.Rules) != 0 {
		t.Fatalf("got %+v, want only a.md kept and Truncated", ins)
	}
	opts.MaxBytes = 16384
	ins, err = Docs(context.Background(), dir, opts, "export format", "")
	if err != nil {
		t.Fatal(err)
	}
	if ins.Truncated || len(ins.Excerpts) != 2 || len(ins.Rules) != 2 {
		t.Fatalf("got %+v, want everything and no Truncated", ins)
	}
	if !strings.Contains(ins.Excerpts[0].Text, "CSV") {
		t.Errorf("best excerpt = %+v", ins.Excerpts[0])
	}
}

func TestMatchGlob(t *testing.T) {
	for _, tc := range []struct {
		pattern, p string
		want       bool
	}{
		{"**/*.md", "README.md", true},
		{"**/*.md", "docs/a/b.md", true},
		{"**/*.md", "a.txt", false},
		{"*.md", "docs/a.md", false},
		{"docs/**", "docs/a/b.md", true},
		{"docs/**", "docs", false},
		{"docs/**/x.md", "docs/x.md", true},
		{"docs/**/x.md", "docs/a/b/x.md", true},
		{"docs/*.md", "docs/a/b.md", false},
	} {
		if got := domain.MatchGlob(tc.pattern, tc.p); got != tc.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", tc.pattern, tc.p, got, tc.want)
		}
	}
	if domain.ValidateGlob("docs/[") == nil || domain.ValidateGlob("") == nil {
		t.Error("ValidateGlob accepted an invalid glob")
	}
}
