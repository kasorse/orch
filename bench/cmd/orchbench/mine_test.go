package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchbench/internal/exitcode"
	"orchbench/internal/testutil"
)

func mineArgs(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = run(context.Background(), append(append([]string{"mine"}, args...), "--cache-dir", t.TempDir()), &out, &errOut)
	return out.String(), err
}

func TestMineUsageErrors(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit("root", map[string]string{"a.sh": "true\n"})
	out := filepath.Join(t.TempDir(), "tasks")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no --repo", []string{"--out", out}, "--repo is required"},
		{"no --out", []string{"--repo", repo.Dir}, "--out is required"},
		{"bad --since", []string{"--repo", repo.Dir, "--out", out, "--since", "01/02/2025"}, "YYYY-MM-DD"},
		{"--since past 2099", []string{"--repo", repo.Dir, "--out", out, "--since", "2150-01-01"}, "1970 to 2099"},
		{"--max-files 0", []string{"--repo", repo.Dir, "--out", out, "--max-files", "0"}, "must be at least 1"},
		{"--max-lines 0", []string{"--repo", repo.Dir, "--out", out, "--max-lines", "0"}, "must be at least 1"},
		{"a language without defaults", []string{"--repo", repo.Dir, "--out", out, "--language", "sh"}, "pass --test-glob"},
		{"no language to detect", []string{"--repo", repo.Dir, "--out", out}, "pass --language"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, err := mineArgs(t, tt.args...)
			if code := exitcode.FromError(err); code != exitcode.Usage {
				t.Fatalf("exit code %d (%v), want %d", code, err, exitcode.Usage)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not mention %q", err, tt.want)
			}
			if stdout != "" {
				t.Errorf("stdout = %q", stdout)
			}
		})
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("a usage error created %s (%v)", out, err)
	}
}

func TestMineWritesDrafts(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit("root", map[string]string{"go.mod": "module proj\n\ngo 1.22\n", "a.go": "package proj\n"})
	sha := repo.Commit("Fix a", map[string]string{
		"a.go":      "package proj\n\nvar A = 1\n",
		"a_test.go": "package proj\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
	})
	out := filepath.Join(t.TempDir(), "tasks")

	stdout, err := mineArgs(t, "--repo", repo.Dir, "--out", out, "--remote", "https://example.com/org/proj.git")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(out, "proj-"+sha[:7]+".yaml")
	want := "wrote " + file + "\nskipped: no test change: 0, only deleted tests: 0, too large: 0, lock file: 0, merge: 1\n"
	if stdout != want {
		t.Errorf("stdout\n%s\nwant\n%s", stdout, want)
	}
}
