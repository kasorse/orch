package logging

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func kinds(segs []seg) map[segKind][]string {
	out := map[segKind][]string{}
	for _, s := range segs {
		out[s.kind] = append(out[s.kind], s.text)
	}
	return out
}

func TestTokenize(t *testing.T) {
	line := "OPS-15489 asks for `docs/orders/export.md` and packages/admin/src/tasks and index.tsx, " +
		"see https://yourcompany.atlassian.net/browse/OPS-1. Keep the 'Technical Landscape' and \"AUTO-GENERATED\" markers. " +
		"They can't be verified; the Endpoint/API hints and/or **only** this."
	k := kinds(tokenize(line))
	check := func(kind segKind, want ...string) {
		t.Helper()
		if strings.Join(k[kind], "|") != strings.Join(want, "|") {
			t.Errorf("kind %d: got %q, want %q", kind, k[kind], want)
		}
	}
	check(segCode, "docs/orders/export.md")
	check(segPath, "packages/admin/src/tasks", "index.tsx")
	check(segURL, "https://yourcompany.atlassian.net/browse/OPS-1")
	check(segSingleQuoted, "'Technical Landscape'")
	check(segDoubleQuoted, `"AUTO-GENERATED"`)
	check(segIssueKey, "OPS-15489")
	check(segBold, "only")
	for _, p := range k[segPlain] {
		if strings.Contains(p, "Endpoint/API") || strings.Contains(p, "can't") {
			continue
		}
	}
	if !strings.Contains(strings.Join(k[segPlain], ""), "Endpoint/API hints and/or") {
		t.Errorf("prose with a slash must stay plain: %q", k[segPlain])
	}
	if !strings.Contains(strings.Join(k[segPlain], ""), "can't") {
		t.Errorf("an apostrophe is not a quote: %q", k[segPlain])
	}
}

func TestRichPara_WrapsWithHangingIndent(t *testing.T) {
	var buf bytes.Buffer
	r := rich{st: style{on: true}, width: 40}
	r.para(&buf, "  Summary: ", "  ", "OPS-15489 asks for the existing Order Export context in `docs/orders/export.md` to be extended into a map.")
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected wrapping, got %q", lines)
	}
	for i, l := range lines {
		if w := visibleWidth(l); w > 39 {
			t.Errorf("line %d is %d wide: %q", i, w, l)
		}
		if i > 0 && !strings.HasPrefix(ansiRE.ReplaceAllString(l, ""), "  ") {
			t.Errorf("continuation line %d lost its indent: %q", i, l)
		}
	}
	if !strings.Contains(buf.String(), "\x1b[38;5;147mdocs/orders/export.md\x1b[0m") {
		t.Errorf("code span not highlighted (or its backticks kept): %q", buf.String())
	}
}

func TestRichBlock_ListsAndFences(t *testing.T) {
	var buf bytes.Buffer
	r := rich{st: style{}, width: 80}
	r.block(&buf, "    ", "Edit it.\n\n1. Add a section.\n   - nested item\n```\nfunc x() {}\n```")
	want := "    Edit it.\n\n    1. Add a section.\n       • nested item\n    │ func x() {}\n"
	if buf.String() != want {
		t.Errorf("got\n%q\nwant\n%q", buf.String(), want)
	}
}

// TestRich_Demo prints the screenshot's analysis result through the renderer:
// ORCHESTRATOR_DEMO=1 go test ./internal/logging -run TestRich_Demo -v
func TestRich_Demo(t *testing.T) {
	if os.Getenv("ORCHESTRATOR_DEMO") == "" {
		t.Skip("set ORCHESTRATOR_DEMO=1 to print")
	}
	var buf bytes.Buffer
	f := resultFormat{st: style{on: true}, bullet: style{on: true}.gray("•"), rich: &rich{st: style{on: true}, width: termWidth()}}
	f.writeText(&buf, "Summary", "OPS-15489 asks for the existing Order Export context in `docs/orders/export.md` to be extended into a single Order Export Context Map. The scanner flows (Assign Slot, Move Item, Receive) have no code mapping. See https://yourcompany.atlassian.net/browse/OPS-15489.")
	f.writeText(&buf, "Root cause", "They can only be inferred from endpoint declarations (`/api/v1/journey/*`, `/api/v3/journey/*`) and from views `tasks/containers/details/components/{loading,packing,moving,sorting,...}` and `sorting-dashboard`.")
	f.writeList(&buf, "Affected files", []string{"docs/orders/export.md", "packages/admin/src/export-plan (reference only: journeys, details, settings)"})
	f.writeText(&buf, "Recommendation", "Edit `docs/orders/export.md` only, and keep hand-written sections outside the \"AUTO-GENERATED\" markers.\n\n1. Add a 'Technical Landscape' section. State plainly that the scanner app is separate.\n2. Add a **Product Feature to Codebase Map** table.\n   - Journeys map to `export-plan`.\n   - Issues map to src/issues and the task issue-table.\n```\nGET /api/v1/export/slot/reassign\n```")
	os.Stdout.WriteString("\n" + buf.String() + "\n")
}

const sampleDiff = `diff --git a/internal/repository/git.go b/internal/repository/git.go
index 1111111..2222222 100644
--- a/internal/repository/git.go
+++ b/internal/repository/git.go
@@ -15,7 +15,9 @@ import (
 	"os"
 	"path/filepath"
 	"slices"
+	"strconv"
 	"strings"
+	"time"
 
 	"orchestrator/internal/apperr"
@@ -266,3 +268,2 @@ func x() {
 	return strings.TrimSpace(commit), nil
 }
-// StatusSummary is a short, human-readable list of the uncommitted changes (one
diff --git a/new.go b/new.go
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/new.go
@@ -0,0 +1,2 @@
+func add(a, b int) int { return a + b } // sum
+var s = "hi"
`

func TestRenderDiff(t *testing.T) {
	r := &ConsoleReporter{Pretty: true}
	out := r.renderDiff(sampleDiff)
	plain := ansiRE.ReplaceAllString(out, "")
	for _, want := range []string{
		"╭─ diff · internal/repository/git.go +2 -1",
		`│  18+   "strconv"`,
		`│  20+   "time"`,
		"│ 268- // StatusSummary",
		"├─ diff · new.go (new) +2 -0",
		"│ 1+ func add(a, b int) int { return a + b } // sum",
		"╰─ (+4 -1 total, 2 files)",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("expected %q in:\n%s", want, plain)
		}
	}
	if os.Getenv("ORCHESTRATOR_DEMO") != "" {
		os.Stdout.WriteString("\n" + out + "\n")
	}
}
