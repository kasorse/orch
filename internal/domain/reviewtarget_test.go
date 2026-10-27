package domain

import (
	"strings"
	"testing"
)

func TestReviewTarget_Validate(t *testing.T) {
	sha := func(c string) string { return strings.Repeat(c, 40) }
	valid := ReviewTarget{Base: sha("a"), Head: sha("b"), MergeBase: sha("c"), BaseRefName: "main", HeadRefName: "feature/x"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a valid target = %v", err)
	}
	long := valid
	long.HeadRefName = strings.Repeat("x", 255)
	if err := long.Validate(); err != nil {
		t.Errorf("a 255-byte ref name = %v", err)
	}

	for name, mutate := range map[string]func(*ReviewTarget){
		"short base":         func(r *ReviewTarget) { r.Base = "abc123" },
		"upper-case head":    func(r *ReviewTarget) { r.Head = strings.Repeat("B", 40) },
		"merge base a ref":   func(r *ReviewTarget) { r.MergeBase = "main" },
		"empty merge base":   func(r *ReviewTarget) { r.MergeBase = "" },
		"empty base name":    func(r *ReviewTarget) { r.BaseRefName = "" },
		"control in head":    func(r *ReviewTarget) { r.HeadRefName = "feature\nx" },
		"too long base name": func(r *ReviewTarget) { r.BaseRefName = strings.Repeat("x", 256) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := valid
			mutate(&bad)
			if err := bad.Validate(); err == nil {
				t.Errorf("%+v validated", bad)
			}
		})
	}
}

// A review run's worktree is detached: only a run with a review target has no branch.
func TestRunWorktreeBranch_ReviewRunHasNone(t *testing.T) {
	run := &Run{ID: "20261023-101500-ab12cd"}
	if got := RunWorktreeBranch(run); got != "orchestrator/20261023-101500-ab12cd" {
		t.Errorf("RunWorktreeBranch = %q", got)
	}
	run.ReviewTarget = &ReviewTarget{}
	if got := RunWorktreeBranch(run); got != "" {
		t.Errorf("RunWorktreeBranch of a review run = %q, want none", got)
	}
}
