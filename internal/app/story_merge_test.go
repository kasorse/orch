package app

import (
	"strings"
	"testing"

	"orchestrator/internal/persistence"
	"orchestrator/internal/story"
)

func mergeRunner() *storyRunner {
	r := &storyRunner{loc: storyLocation{slug: "refunds"}, dirArg: "specs/refunds"}
	r.spec.tasks = []story.Task{
		{ID: "RF01", Repo: "billing"},
		{ID: "RF02", Repo: "billing"},
		{ID: "RF03", Repo: "billing", DependsOn: []string{"RF01", "RF02"}},
		{ID: "RF04", Repo: "billing", DependsOn: []string{"RF03"}},
	}
	r.rec.Services = map[string]string{"billing": "/src/billing"}
	r.rec.Tasks = map[string]persistence.StoryTask{"RF03": {Branch: "story/refunds/RF03.2"}}
	return r
}

func TestStartLabelAndTargetBranch(t *testing.T) {
	r := mergeRunner()
	cases := []struct {
		id            string
		label, target string
	}{
		{"RF01", "", "story/refunds/spec"},
		{"RF03", "story/refunds/spec (RF01 + RF02)", "story/refunds/spec"},
		{"RF04", "story/refunds/RF03.2", "story/refunds/RF03.2"},
	}
	for _, tc := range cases {
		if got := r.startLabel(tc.id); got != tc.label {
			t.Errorf("startLabel(%s) = %q, want %q", tc.id, got, tc.label)
		}
		if got := r.targetBranch(tc.id); got != tc.target {
			t.Errorf("targetBranch(%s) = %q, want %q", tc.id, got, tc.target)
		}
	}
	// Before a parent has a record, its first attempt's branch is named.
	r.rec.Tasks = nil
	if got := r.startLabel("RF04"); got != "story/refunds/RF03" {
		t.Errorf("startLabel(RF04) = %q, want story/refunds/RF03", got)
	}
}

func TestConflictHints(t *testing.T) {
	r := mergeRunner()
	r.rec.Tasks["RF03"] = persistence.StoryTask{
		Status: story.TaskConflict, Repo: "billing",
		Conflict: &persistence.StoryConflict{Parents: []string{"RF01", "RF02"}, Files: []string{"a.go"}},
	}
	got := strings.Join(r.conflictHints(r.rec.Tasks["RF03"]), "\n")
	for _, want := range []string{
		"git -C /src/billing worktree add <dir> story/refunds/spec",
		"git merge story/refunds/RF01\n  git merge story/refunds/RF02",
		"git -C /src/billing worktree remove <dir>",
		"then: orchestrator story run specs/refunds",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the hints lack %q:\n%s", want, got)
		}
	}
}
