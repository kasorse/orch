package repository

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitAll(t *testing.T) {
	for _, ignoreRuns := range []bool{false, true} {
		name := "runs dir tracked"
		if ignoreRuns {
			name = "runs dir ignored"
		}
		t.Run(name, func(t *testing.T) {
			repo, dir := realRepo(t)
			if ignoreRuns {
				writeFile(t, filepath.Join(dir, ".gitignore"), ".orchestrator/\n")
				gitCmd(t, dir, "add", "-A")
				gitCmd(t, dir, "commit", "-qm", "ignore runs")
			}
			writeFile(t, filepath.Join(dir, "a.go"), "package a\n\nfunc A() {}\n")
			writeFile(t, filepath.Join(dir, "sub", "new.go"), "package sub\n")
			writeFile(t, filepath.Join(dir, ".orchestrator", "runs", "x", "run.json"), "{}")
			// From a subdirectory, as the orchestrator may be started anywhere in the tree.
			repo.Dir = filepath.Join(dir, "sub")

			msg := "-starts with a dash\n\nOrchestrator-Run: x\n"
			sha, err := repo.CommitAll(context.Background(), msg)
			if err != nil {
				t.Fatalf("CommitAll: %v", err)
			}
			if head := gitOut(t, dir, "rev-parse", "HEAD"); sha != head {
				t.Errorf("sha = %s, HEAD = %s", sha, head)
			}
			if got := gitOut(t, dir, "log", "-1", "--format=%B"); got != strings.TrimSpace(msg) {
				t.Errorf("message = %q", got)
			}
			files := gitOut(t, dir, "show", "--name-only", "--format=", "HEAD")
			if files != "a.go\nsub/new.go" {
				t.Errorf("committed files = %q", files)
			}
			if status := gitOut(t, dir, "status", "--porcelain"); ignoreRuns && status != "" {
				t.Errorf("tree not clean after commit: %q", status)
			}
		})
	}
}

func TestCommitAll_FailedCommitLeavesChangesStaged(t *testing.T) {
	repo, dir := realRepo(t)
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho refused by hook >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "a.go"), "package a // changed\n")

	_, err := repo.CommitAll(context.Background(), "change")
	if err == nil || !strings.Contains(err.Error(), "refused by hook") || !strings.Contains(err.Error(), "staged") {
		t.Fatalf("err = %v, want the hook's refusal", err)
	}
	if status := gitOut(t, dir, "status", "--porcelain"); status != "M  a.go" {
		t.Errorf("status = %q, want a.go staged", status)
	}
}
