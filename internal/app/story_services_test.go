package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/execution"
)

// topLevelExecutor answers git rev-parse --show-toplevel with the working directory itself,
// unless top maps it elsewhere, and counts the calls.
type topLevelExecutor struct {
	calls []string
	top   map[string]string
}

func (e *topLevelExecutor) Run(_ context.Context, spec execution.Spec) (execution.ProcessResult, error) {
	e.calls = append(e.calls, spec.WorkingDir)
	if t, ok := e.top[spec.WorkingDir]; ok {
		return execution.ProcessResult{Stdout: t + "\n"}, nil
	}
	return execution.ProcessResult{Stdout: spec.WorkingDir + "\n"}, nil
}

func storyTree(t *testing.T, roots map[string][]string, plain map[string]int) []string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for root, repos := range roots {
		dir := filepath.Join(base, root)
		for _, r := range repos {
			if err := os.MkdirAll(filepath.Join(dir, r, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < plain[root]; i++ {
			if err := os.MkdirAll(filepath.Join(dir, fmt.Sprintf("plain%d", i)), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		out = append(out, dir)
	}
	return out
}

func names(cs []serviceCandidate) string {
	var n []string
	for _, c := range cs {
		n = append(n, c.Name)
	}
	return strings.Join(n, ",")
}

func TestFindServices(t *testing.T) {
	roots := storyTree(t, map[string][]string{"src": {"billing", "gateway", "docs", "bad name"}}, map[string]int{"src": 200})
	cands, err := candidates(roots, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cands); got != "billing,docs,gateway" {
		t.Fatalf("candidates = %s", got)
	}

	t.Run("found by mention, git only for the named", func(t *testing.T) {
		ex := &topLevelExecutor{}
		got, err := findServices(context.Background(), ex, "change billing and Gateway", nil, nil, cands, roots, 5)
		if err != nil {
			t.Fatal(err)
		}
		if names(got) != "billing,gateway" || len(ex.calls) != 2 {
			t.Errorf("got %s, git calls %v", names(got), ex.calls)
		}
	})

	t.Run("--repo overrides detection", func(t *testing.T) {
		ex := &topLevelExecutor{}
		got, err := findServices(context.Background(), ex, "change billing", nil, []string{"docs"}, cands, roots, 5)
		if err != nil || names(got) != "docs" {
			t.Errorf("got %v, err %v", got, err)
		}
		if _, err := findServices(context.Background(), ex, "x", nil, []string{"nope"}, cands, roots, 5); err == nil {
			t.Error("unknown --repo accepted")
		}
	})

	t.Run("none named", func(t *testing.T) {
		ex := &topLevelExecutor{}
		_, err := findServices(context.Background(), ex, "something", nil, nil, cands, roots, 5)
		if err == nil || !strings.Contains(err.Error(), "names no service under story.roots ("+roots[0]+")") || len(ex.calls) != 0 {
			t.Errorf("err = %v, calls = %v", err, ex.calls)
		}
	})

	t.Run("too many", func(t *testing.T) {
		ex := &topLevelExecutor{}
		_, err := findServices(context.Background(), ex, "billing gateway docs", nil, nil, cands, roots, 2)
		if err == nil || !strings.Contains(err.Error(), "billing, docs, gateway") || !strings.Contains(err.Error(), "narrow it with --repo") || len(ex.calls) != 0 {
			t.Errorf("err = %v, calls = %v", err, ex.calls)
		}
	})

	t.Run("not a top level", func(t *testing.T) {
		ex := &topLevelExecutor{top: map[string]string{filepath.Join(roots[0], "billing"): filepath.Dir(roots[0])}}
		_, err := findServices(context.Background(), ex, "billing", nil, nil, cands, roots, 5)
		if err == nil || !strings.Contains(err.Error(), "not the top level") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestFindServices_SameNameUnderTwoRoots(t *testing.T) {
	roots := storyTree(t, map[string][]string{"a": {"billing"}, "b": {"billing"}}, nil)
	cands, err := candidates(roots, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = findServices(context.Background(), &topLevelExecutor{}, "billing", nil, nil, cands, roots, 5)
	if err == nil || !strings.Contains(err.Error(), "remove the ambiguity in story.roots") {
		t.Fatalf("err = %v", err)
	}
	for _, r := range roots {
		if !strings.Contains(err.Error(), filepath.Join(r, "billing")) {
			t.Errorf("error lacks %s: %v", r, err)
		}
	}
}

func TestCandidates_HomeOnceAndOutsideRoots(t *testing.T) {
	roots := storyTree(t, map[string][]string{"src": {"billing"}}, nil)
	inRoot := filepath.Join(roots[0], "billing")

	cands, err := candidates(roots, inRoot)
	if err != nil || names(cands) != "billing" {
		t.Errorf("home under a root: %v, %v", cands, err)
	}

	outside := filepath.Join(t.TempDir(), "home-repo")
	if err := os.MkdirAll(filepath.Join(outside, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cands, err = candidates(roots, outside)
	if err != nil || names(cands) != "billing,home-repo" {
		t.Errorf("home outside roots: %v, %v", cands, err)
	}
}
