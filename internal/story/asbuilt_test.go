package story

import (
	"strings"
	"testing"
)

func sampleAsBuilt() AsBuilt {
	return AsBuilt{Date: "2026-10-20", RunID: "3f2a9c", Repo: "gateway", Status: "completed", FixIterations: 1, TestsPassed: true, ReviewVerdict: "pass", Branch: "story/refunds/RF02", Base: "story/refunds/RF01", BaseSHA: "8d0e4b2c9a1f", Commit: "1c9f2e7aa0b3", CostUSD: 0.42, Summary: "Add partial refund amount to POST /v1/refunds.", BaseBranch: "main"}
}

const wantSection = "## As built (2026-10-20)\n" +
	"\n" +
	"Run `3f2a9c` in gateway: completed, 1 fix iteration, tests passed, review pass.\n" +
	"Branch `story/refunds/RF02` on `story/refunds/RF01` @ 8d0e4b2, commit 1c9f2e7.\n" +
	"Cost $0.42. Implementation summary: Add partial refund amount to POST /v1/refunds.\n" +
	"Conflicts with main: none.\n"

func TestAppendAsBuilt(t *testing.T) {
	got := string(AppendAsBuilt([]byte(sampleFile), sampleAsBuilt()))
	if want := sampleFile + "\n" + wantSection; got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestAppendAsBuiltLineBreaks(t *testing.T) {
	cases := []struct{ name, in, sep string }{
		{"no trailing newline", "text", "\n\n"},
		{"one newline", "text\n", "\n"},
		{"a blank line already", "text\n\n", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(AppendAsBuilt([]byte(tc.in), sampleAsBuilt()))
			if want := tc.in + tc.sep + wantSection; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestAppendAsBuiltCRLF(t *testing.T) {
	in := strings.ReplaceAll(sampleFile, "\n", "\r\n")
	got := string(AppendAsBuilt([]byte(in), sampleAsBuilt()))
	want := in + "\r\n" + strings.ReplaceAll(wantSection, "\n", "\r\n")
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestAppendAsBuiltKeepsTheFrozenHash(t *testing.T) {
	inputs := []string{sampleFile, strings.TrimSuffix(sampleFile, "\n"), strings.ReplaceAll(sampleFile, "\n", "\r\n"), sampleFile + "\n\n"}
	for _, in := range inputs {
		once := AppendAsBuilt([]byte(in), sampleAsBuilt())
		if FrozenHash(once) != FrozenHash([]byte(in)) {
			t.Errorf("the hash changed for input %q", in)
		}
		again := AppendAsBuilt(once, sampleAsBuilt())
		if FrozenHash(again) != FrozenHash([]byte(in)) {
			t.Errorf("the hash changed after a second section for input %q", in)
		}
	}
}

func TestAppendAsBuiltKeepsTheFileReadable(t *testing.T) {
	got, err := ParseTaskFile(sampleName, AppendAsBuilt([]byte(sampleFile), sampleAsBuilt()))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "RF02" || len(got.Acceptance) != 4 || strings.Contains(got.Body, "As built") {
		t.Errorf("got %+v", got)
	}
}

func TestAppendAsBuiltVariants(t *testing.T) {
	a := sampleAsBuilt()
	a.FixIterations = 0
	a.TestsPassed = false
	a.ReviewVerdict = ""
	a.Summary = ""
	a.Conflicts = "src/a.go, src/b.go"
	a.Date = "2026-10-21"
	got := string(AppendAsBuilt(nil, a))
	for _, want := range []string{
		"## As built (2026-10-21)\n",
		": completed, 0 fix iterations, tests failed, no review.\n",
		"Implementation summary: none.\n",
		"Conflicts with main: src/a.go, src/b.go.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	a.FixIterations = 2
	if got := string(AppendAsBuilt(nil, a)); !strings.Contains(got, "2 fix iterations") {
		t.Errorf("got:\n%s", got)
	}
}

func TestAppendAsBuiltSummaryStaysOnItsLine(t *testing.T) {
	a := sampleAsBuilt()
	a.Summary = "Fix it\n\n## Acceptance\n- injected\r\n```\n## As built (1999-01-01)"
	a.Branch = "story/x/RF02`\n## Goal"
	out := string(AppendAsBuilt([]byte(sampleFile), a))
	section := out[strings.Index(out, "## As built (2026-10-20)"):]
	for _, line := range strings.Split(section, "\n")[1:] {
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "```") {
			t.Errorf("model text started a line: %q\n%s", line, section)
		}
	}
	if strings.Count(out, "\n## As built") != 1 {
		t.Errorf("a second As built heading was injected:\n%s", out)
	}
	if FrozenHash([]byte(out)) != FrozenHash([]byte(sampleFile)) {
		t.Error("the hash changed")
	}
}
