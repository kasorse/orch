package action

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/repository"
)

func TestRenderDiffStat_Small(t *testing.T) {
	files := []repository.FileChange{
		{Path: "internal/store/store.go", Added: 3, Deleted: 1},
		{Path: "internal/store/store_test.go", Added: 12},
	}
	want := " internal/store/store.go      |  4 +++-\n" +
		" internal/store/store_test.go | 12 ++++++++++++\n" +
		" 2 files changed, 15 insertions(+), 1 deletion(-)"
	if got := renderDiffStat(files, 0); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if got := renderDiffStat(files, 4096); got != want {
		t.Errorf("a stat within the bound changed:\n%s", got)
	}
}

func TestRenderDiffStat_NoFilesIsEmpty(t *testing.T) {
	if got := renderDiffStat(nil, 100); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestRenderDiffStat_BinaryAndWording(t *testing.T) {
	cases := []struct {
		name  string
		files []repository.FileChange
		want  string
	}{
		{"binary", []repository.FileChange{{Path: "logo.png", Added: -1, Deleted: -1}, {Path: "a.go", Added: 1}},
			" logo.png | Bin\n a.go     | 1 +\n 2 files changed, 1 insertion(+)"},
		{"only deletions", []repository.FileChange{{Path: "a.go", Deleted: 2}},
			" a.go | 2 --\n 1 file changed, 2 deletions(-)"},
		{"only a binary file", []repository.FileChange{{Path: "x.bin", Added: -1, Deleted: -1}},
			" x.bin | Bin\n 1 file changed, 0 insertions(+), 0 deletions(-)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderDiffStat(tc.files, 0); got != tc.want {
				t.Errorf("got\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestRenderDiffStat_ScalesTheBar(t *testing.T) {
	got := renderDiffStat([]repository.FileChange{{Path: "big.go", Added: 300, Deleted: 100}, {Path: "s.go", Added: 1}}, 0)
	for _, line := range strings.Split(got, "\n")[:2] {
		bar := line[strings.LastIndex(line, " ")+1:]
		if len(bar) > statBarWidth {
			t.Errorf("bar %q is longer than %d: %q", bar, statBarWidth, line)
		}
	}
	if !strings.Contains(got, " s.go   |   1 +\n") {
		t.Errorf("a small change keeps at least one mark:\n%s", got)
	}
}

func TestRenderDiffStat_BoundKeepsWholeLines(t *testing.T) {
	const n = 2000
	files := make([]repository.FileChange, n)
	for i := range files {
		files[i] = repository.FileChange{Path: fmt.Sprintf("pkg/dir%04d/file.go", i), Added: i%7 + 1, Deleted: i % 3}
	}
	limit := config.MinMaxDiffBytes

	got := renderDiffStat(files, limit)
	if len(got) > limit {
		t.Errorf("%d bytes, over the %d bound", len(got), limit)
	}
	lines := strings.Split(got, "\n")
	last := lines[len(lines)-1]
	m := regexp.MustCompile(`^… (\d+) more files$`).FindStringSubmatch(last)
	if m == nil {
		t.Fatalf("last line = %q, want \"… N more files\"", last)
	}
	more, _ := strconv.Atoi(m[1])
	kept := len(lines) - 2 // the totals line and the "more" line are not files
	if kept+more != n {
		t.Errorf("kept %d files and %d more, want %d in all", kept, more, n)
	}
	if kept == 0 || more == 0 {
		t.Errorf("kept %d, more %d: the test must exercise a cut", kept, more)
	}
	for i, l := range lines[:kept] {
		if !strings.HasPrefix(l, fmt.Sprintf(" pkg/dir%04d/file.go ", i)) {
			t.Fatalf("line %d = %q: not the %dth file whole", i, l, i)
		}
	}
	if !strings.HasPrefix(lines[kept], " 2000 files changed, ") {
		t.Errorf("the totals cover every file, got %q", lines[kept])
	}
}
