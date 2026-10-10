package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRemoveStaleVerifyDirs(t *testing.T) {
	tmp := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)

	mkdir := func(name string, mtime time.Time) string {
		t.Helper()
		p := filepath.Join(tmp, name)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "calls.jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stale := mkdir("orch-verify-stale", old)
	fresh := mkdir("orch-verify-fresh", time.Now().Add(-time.Hour))
	other := mkdir("not-orch-verify-old", old)

	// A symlink to an old directory outside the matches: the link is not followed, and the
	// target is not ours to remove.
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(target, old, old); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "orch-verify-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(tmp, "orch-verify-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}

	removeStaleVerifyDirs(tmp, time.Now())

	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Errorf("the stale directory is still there: %v", err)
	}
	for _, kept := range []string{fresh, other, link, target, file} {
		if _, err := os.Lstat(kept); err != nil {
			t.Errorf("%s must be kept: %v", kept, err)
		}
	}
}

func TestRemoveStaleVerifyDirs_MissingTmp(t *testing.T) {
	removeStaleVerifyDirs(filepath.Join(t.TempDir(), "absent"), time.Now())
}
