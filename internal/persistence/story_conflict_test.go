package persistence

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"orchestrator/internal/story"
)

// specRecord is runningRecord with the spec branch recorded and RF03 in the conflict status.
func specRecord() StoryRecord {
	r := runningRecord()
	for name, b := range r.Bases {
		b.SpecBranch = "story/refunds/spec"
		r.Bases[name] = b
	}
	r.Tasks["RF03"] = StoryTask{
		Status: story.TaskConflict, Repo: "billing",
		Conflict: &StoryConflict{Parents: []string{"RF01", "RF02"}, Files: []string{"a.go", "pkg/b.go"}},
	}
	return r
}

func TestStoryRecord_SpecBranchAndConflictRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := specRecord()
	if err := SaveStoryRecord(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStoryRecord(filepath.Join(dir, "story.json"))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded\n%+v, %v\nwant\n%+v", got, err, want)
	}
}

// A record from before the spec branch and the conflict state loads as it did.
func TestStoryRecord_WithoutSpecBranchLoads(t *testing.T) {
	r := runningRecord()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if r.Bases["billing"].SpecBranch != "" {
		t.Fatal("the fixture should have no spec branch")
	}
}

func TestStoryRecord_RefusesASpecBranchAndConflictThatAreNotValid(t *testing.T) {
	manyFiles := make([]string, MaxConflictFiles+1)
	for i := range manyFiles {
		manyFiles[i] = fmt.Sprintf("f%d.go", i)
	}
	setConflict := func(c *StoryConflict) func(r *StoryRecord) {
		return func(r *StoryRecord) {
			rt := r.Tasks["RF03"]
			rt.Conflict = c
			r.Tasks["RF03"] = rt
		}
	}
	cases := []struct {
		name   string
		change func(r *StoryRecord)
		want   string
	}{
		{"spec branch of another story", func(r *StoryRecord) {
			b := r.Bases["billing"]
			b.SpecBranch = "story/other/spec"
			r.Bases["billing"] = b
		}, "spec_branch"},
		{"spec branch that is a task branch", func(r *StoryRecord) {
			b := r.Bases["billing"]
			b.SpecBranch = "story/refunds/RF01"
			r.Bases["billing"] = b
		}, "spec_branch"},
		{"conflict task without a conflict", setConflict(nil), "no conflict"},
		{"conflict without parents", setConflict(&StoryConflict{Files: []string{"a.go"}}), "conflict.parents"},
		{"conflict parent that is no ID", setConflict(&StoryConflict{Parents: []string{"rf01"}, Files: []string{"a.go"}}), "conflict.parents"},
		{"conflict parent twice", setConflict(&StoryConflict{Parents: []string{"RF01", "RF01"}, Files: []string{"a.go"}}), "conflict.parents"},
		{"too many files", setConflict(&StoryConflict{Parents: []string{"RF01", "RF02"}, Files: manyFiles}), "conflict.files"},
		{"absolute file", setConflict(&StoryConflict{Parents: []string{"RF01", "RF02"}, Files: []string{"/etc/passwd"}}), "conflict.files"},
		{"file out of the repository", setConflict(&StoryConflict{Parents: []string{"RF01", "RF02"}, Files: []string{"../x"}}), "conflict.files"},
		{"unclean file", setConflict(&StoryConflict{Parents: []string{"RF01", "RF02"}, Files: []string{"a//b.go"}}), "conflict.files"},
		{"conflict on a task that is not in conflict", func(r *StoryRecord) {
			rt := r.Tasks["RF01"]
			rt.Conflict = &StoryConflict{Parents: []string{"RF02"}, Files: []string{"a.go"}}
			r.Tasks["RF01"] = rt
		}, "has a conflict"},
		{"conflict on a pending task", func(r *StoryRecord) {
			r.Tasks["RF04"] = StoryTask{Status: story.TaskPending, Conflict: &StoryConflict{Parents: []string{"RF02"}}}
		}, "has a conflict"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := specRecord()
			tc.change(&r)
			if err := r.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %v, want an error about %q", err, tc.want)
			}
		})
	}
}

func TestConflictFilesKeepsCleanPathsUpToTheLimit(t *testing.T) {
	var in []string
	for i := 0; i < MaxConflictFiles+10; i++ {
		in = append(in, fmt.Sprintf("d/f%d.go", i))
	}
	in = append([]string{"/abs", "../up", "a\nb"}, in...)
	got := ConflictFiles(in)
	if len(got) != MaxConflictFiles || got[0] != "d/f0.go" {
		t.Errorf("ConflictFiles kept %d paths starting with %q, want %d starting with d/f0.go", len(got), got[0], MaxConflictFiles)
	}
	if got := ConflictFiles(nil); got == nil || len(got) != 0 {
		t.Errorf("ConflictFiles(nil) = %#v, want an empty slice", got)
	}
}

func TestStorySpecBranch(t *testing.T) {
	if got := StorySpecBranch("refunds"); got != "story/refunds/spec" {
		t.Errorf("StorySpecBranch = %s", got)
	}
}
