package story

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden from the current output")

func readmeTasks() []Task {
	return []Task{
		{ID: "RF03", Repo: "billing", DependsOn: []string{"RF01"}, Jira: "none", Title: "Refund report"},
		{ID: "RF02", Repo: "gateway", DependsOn: []string{"RF01"}, Jira: "PAY-124", Title: "Expose partial refunds in the public API"},
		{ID: "RF01", Repo: "billing", Jira: "PAY-123", Title: "Refund amount in the billing model"},
	}
}

func TestRenderREADMEGolden(t *testing.T) {
	cases := []struct {
		name   string
		status Status
		states map[string]TaskState
	}{
		{"planned", StatusPlanned, nil},
		{"running", StatusRunning, map[string]TaskState{
			"RF01": {Status: TaskDone, Branch: "story/refunds/RF01"},
			"RF02": {Status: TaskRunning, Branch: "story/refunds/RF02"},
		}},
		{"completed", StatusCompleted, map[string]TaskState{
			"RF01": {Status: TaskDone, Branch: "story/refunds/RF01"},
			"RF02": {Status: TaskDone, Branch: "story/refunds/RF02"},
			"RF03": {Status: TaskDone, Branch: "story/refunds/RF03"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderREADME("Partial refunds", tc.status, readmeTasks(), tc.states)
			if again := RenderREADME("Partial refunds", tc.status, readmeTasks(), tc.states); again != got {
				t.Fatal("README is not deterministic")
			}

			path := filepath.Join("testdata", "readme_"+tc.name+".golden")
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (go test ./internal/story -update): %v", err)
			}
			if got != string(want) {
				t.Errorf("README differs from %s (go test ./internal/story -update to accept):\n--- got\n%s\n--- want\n%s", path, got, want)
			}
		})
	}
}

func TestRenderREADMEFollowsTheStoryOrderNotTheInputOrder(t *testing.T) {
	got := RenderREADME("T", StatusPlanned, readmeTasks(), nil)
	i1, i2, i3 := strings.Index(got, "| RF01 |"), strings.Index(got, "| RF02 |"), strings.Index(got, "| RF03 |")
	if !(i1 > 0 && i1 < i2 && i2 < i3) {
		t.Errorf("rows out of order:\n%s", got)
	}
}

func TestRenderREADMEKeepsTasksThatCannotRun(t *testing.T) {
	tasks := []Task{{ID: "RF01", Repo: "a", Title: "One"}, {ID: "RF02", Repo: "a", DependsOn: []string{"RF09"}, Title: "Two"}}
	got := RenderREADME("T", StatusPlanned, tasks, nil)
	if !strings.Contains(got, "| RF02 |") || !strings.Contains(got, "(0 of 2 tasks done)") {
		t.Errorf("a task with a missing dependency must still be listed:\n%s", got)
	}
}

func TestRenderREADMEShowsConflict(t *testing.T) {
	states := map[string]TaskState{"RF01": {Status: TaskConflict}}
	got := RenderREADME("T", StatusBlocked, []Task{{ID: "RF01", Repo: "a", Title: "One"}}, states)
	if !strings.Contains(got, "| conflict |") {
		t.Errorf("status cell missing:\n%s", got)
	}
}

func TestRenderREADMEEscapesCells(t *testing.T) {
	tasks := []Task{{ID: "RF01", Repo: "a", Title: "a | b\nsecond line"}}
	got := RenderREADME("one\ntwo", StatusPlanned, tasks, nil)
	if !strings.Contains(got, `| a \| b second line |`) || !strings.HasPrefix(got, "# one two\n") {
		t.Errorf("got:\n%s", got)
	}
}
