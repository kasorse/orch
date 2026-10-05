package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func denyForTest(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	return base == ".env" || strings.HasSuffix(base, ".pem") || strings.HasPrefix(base, "secrets")
}

func reviewOpts() ReviewDiffOptions {
	return ReviewDiffOptions{
		Exclude:   []string{".orchestrator/runs"},
		Denied:    denyForTest,
		Summarize: []string{"go.sum", "*.min.js"},
		MaxBytes:  256 << 10,
	}
}

func fileByPath(t *testing.T, d ReviewDiff, p string) FileChange {
	t.Helper()
	for _, f := range d.Files {
		if f.Path == p {
			return f
		}
	}
	t.Fatalf("%s is not in the review diff: %+v", p, d.Files)
	return FileChange{}
}

func TestReviewDiff_NewFileContentIsShown(t *testing.T) {
	repo, dir := realRepo(t)
	writeFile(t, filepath.Join(dir, "internal", "new.go"), "package internal\n\nfunc Added() int { return 42 }\n")

	d, err := repo.ReviewDiff(context.Background(), reviewOpts())
	if err != nil {
		t.Fatal(err)
	}
	f := fileByPath(t, d, "internal/new.go")
	if f.Treatment != TreatFull || !f.Untracked || f.Added != 3 {
		t.Errorf("new file = %+v", f)
	}
	for _, want := range []string{"diff --git a/internal/new.go b/internal/new.go", "new file mode 100644", "@@ -0,0 +1,3 @@", "+func Added() int { return 42 }\n"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("review diff lacks %q:\n%s", want, d.Text)
		}
	}
	if d.Truncated {
		t.Error("nothing was cut")
	}
}

func TestReviewDiff_TrackedChangeAndNoNewline(t *testing.T) {
	repo, dir := realRepo(t)
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n\nvar X = 1\n")
	writeFile(t, filepath.Join(dir, "tail.txt"), "no newline")

	d, err := repo.ReviewDiff(context.Background(), reviewOpts())
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByPath(t, d, "a.go"); f.Treatment != TreatFull || f.Added != 2 || f.Deleted != 0 || f.Untracked {
		t.Errorf("a.go = %+v", f)
	}
	if !strings.Contains(d.Text, "+var X = 1") {
		t.Errorf("tracked change missing:\n%s", d.Text)
	}
	if !strings.Contains(d.Text, "+no newline\n\\ No newline at end of file\n") {
		t.Errorf("missing final newline not marked:\n%s", d.Text)
	}
}

func TestReviewDiff_WithheldBinarySymlink(t *testing.T) {
	repo, dir := realRepo(t)
	// A tracked secrets file, changed by the run: its diff must not be shown either.
	writeFile(t, filepath.Join(dir, "config", "secrets.yaml"), "token: old\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", "secrets")
	writeFile(t, filepath.Join(dir, "config", "secrets.yaml"), "token: s3cr3t-value\n")

	writeFile(t, filepath.Join(dir, ".env"), "API_KEY=sk-live-should-not-appear\n")
	writeFile(t, filepath.Join(dir, "certs", "server.pem"), "-----BEGIN PRIVATE KEY-----\n")
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), []byte("\x89PNG\x00\x01\x02"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "hosts-link")); err != nil {
		t.Fatal(err)
	}

	d, err := repo.ReviewDiff(context.Background(), reviewOpts())
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]Treatment{
		"config/secrets.yaml": TreatWithheld, ".env": TreatWithheld, "certs/server.pem": TreatWithheld,
		"logo.png": TreatBinary, "hosts-link": TreatSymlink,
	} {
		if f := fileByPath(t, d, p); f.Treatment != want {
			t.Errorf("%s: treatment %q, want %q", p, f.Treatment, want)
		}
	}
	for _, secret := range []string{"s3cr3t-value", "sk-live-should-not-appear", "BEGIN PRIVATE KEY"} {
		if strings.Contains(d.Text, secret) {
			t.Errorf("withheld content %q reached the review diff", secret)
		}
	}
	if !strings.Contains(d.Text, "hosts-link  (new file, symlink -> /etc/hosts)") {
		t.Errorf("symlink line missing:\n%s", d.Text)
	}
	if !strings.Contains(d.Text, "config/secrets.yaml  +1 -1  (withheld: matches the secret deny list)") {
		t.Errorf("tracked withheld line missing:\n%s", d.Text)
	}
}

func TestReviewDiff_TrackedBinary(t *testing.T) {
	repo, dir := realRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("a\x00b"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", "blob")
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("c\x00d"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := repo.ReviewDiff(context.Background(), reviewOpts())
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByPath(t, d, "blob.bin"); f.Treatment != TreatBinary {
		t.Errorf("blob.bin = %+v", f)
	}
	if strings.Contains(d.Text, "Binary files") {
		t.Error("a binary file's patch was shown")
	}
}

func TestReviewDiff_SummarizedFiles(t *testing.T) {
	repo, dir := realRepo(t)
	writeFile(t, filepath.Join(dir, "go.mod"), "module x\n")
	writeFile(t, filepath.Join(dir, "go.sum"), "a v1 h1:x\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", "mod")
	writeFile(t, filepath.Join(dir, "go.mod"), "module x\n\nrequire evil v1.0.0\n")
	writeFile(t, filepath.Join(dir, "go.sum"), "a v1 h1:x\nevil v1.0.0 h1:LOCKLINE\n")
	writeFile(t, filepath.Join(dir, "web", "app.min.js"), "var a=1;MINIFIED\n")

	d, err := repo.ReviewDiff(context.Background(), reviewOpts())
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByPath(t, d, "go.sum"); f.Treatment != TreatSummarized || f.Added != 1 {
		t.Errorf("go.sum = %+v", f)
	}
	if f := fileByPath(t, d, "web/app.min.js"); f.Treatment != TreatSummarized {
		t.Errorf("app.min.js = %+v", f)
	}
	if f := fileByPath(t, d, "go.mod"); f.Treatment != TreatFull {
		t.Errorf("go.mod = %+v, a manifest stays visible", f)
	}
	if strings.Contains(d.Text, "LOCKLINE") || strings.Contains(d.Text, "MINIFIED") {
		t.Error("a summarized file's content was shown")
	}
	if !strings.Contains(d.Text, "+require evil v1.0.0") {
		t.Error("the go.mod change is missing")
	}
}

func TestReviewDiff_Budget(t *testing.T) {
	repo, dir := realRepo(t)
	writeFile(t, filepath.Join(dir, "big.txt"), "x\n")
	writeFile(t, filepath.Join(dir, "z.go"), "package z\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", "files")
	writeFile(t, filepath.Join(dir, "big.txt"), strings.Repeat("a long changed line of text\n", 12000)) // ~330 KiB
	writeFile(t, filepath.Join(dir, "z.go"), "package z\n\nvar Small = true\n")

	opts := reviewOpts()
	opts.MaxBytes = 64 << 10
	d, err := repo.ReviewDiff(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if f := fileByPath(t, d, "big.txt"); f.Treatment != TreatBudget {
		t.Errorf("big.txt = %+v, want budget", f)
	}
	if f := fileByPath(t, d, "z.go"); f.Treatment != TreatFull {
		t.Errorf("z.go = %+v, a later small file still fits", f)
	}
	if !d.Truncated || len(d.Text) > opts.MaxBytes {
		t.Errorf("truncated = %v, text %d bytes, budget %d", d.Truncated, len(d.Text), opts.MaxBytes)
	}
	if !strings.Contains(d.Text, "big.txt  +12000 -1  (not shown: diff budget)") {
		t.Errorf("the list does not show big.txt's final treatment:\n%s", d.Text[:min(len(d.Text), 500)])
	}
}

func TestReviewDiff_ManyUntracked(t *testing.T) {
	repo, dir := realRepo(t)
	for i := range 600 {
		writeFile(t, filepath.Join(dir, "gen", fmt.Sprintf("f%03d.txt", i)), "x\n")
	}
	d, err := repo.ReviewDiff(context.Background(), reviewOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 500 || d.Unlisted != 100 || !d.Truncated {
		t.Errorf("files %d, unlisted %d, truncated %v", len(d.Files), d.Unlisted, d.Truncated)
	}
	if !strings.Contains(d.Text, "Changed files (600)") || !strings.Contains(d.Text, "... and 100 more files, neither listed nor shown") {
		t.Errorf("header does not account for every file:\n%s", d.Text[:200])
	}
}

func TestReviewDiff_ExcludesRunsDirAndHandlesRename(t *testing.T) {
	repo, dir := realRepo(t)
	writeFile(t, filepath.Join(dir, "old.go"), "package a\n\nfunc Keep() {}\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-qm", "old")
	gitCmd(t, dir, "mv", "old.go", "renamed.go")
	writeFile(t, filepath.Join(dir, ".orchestrator", "runs", "r1", "run.json"), "{}")

	d, err := repo.ReviewDiff(context.Background(), reviewOpts())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.Text, ".orchestrator") {
		t.Error("the runs directory reached the review diff")
	}
	fileByPath(t, d, "renamed.go")
	if len(d.Files) != 1 {
		t.Errorf("a rename is one file, got %+v", d.Files)
	}
}

func TestReviewDiff_Empty(t *testing.T) {
	repo, _ := realRepo(t)
	d, err := repo.ReviewDiff(context.Background(), reviewOpts())
	if err != nil {
		t.Fatal(err)
	}
	if !d.Empty() {
		t.Errorf("a clean tree is not empty: %+v", d)
	}
}

func TestParseNumstat(t *testing.T) {
	got, err := parseNumstat("3\t1\ta.go\x00-\t-\tlogo.png\x000\t0\t\x00old name.go\x00new name.go\x00")
	if err != nil {
		t.Fatal(err)
	}
	want := []FileChange{{Path: "a.go", Added: 3, Deleted: 1}, {Path: "logo.png", Added: -1, Deleted: -1}, {Path: "new name.go"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if _, err := parseNumstat("garbage\x00"); err == nil {
		t.Error("an unexpected entry must be an error")
	}
}
