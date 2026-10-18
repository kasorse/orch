package story

import (
	"reflect"
	"strings"
	"testing"
)

const sampleName = "TASK-RF02-expose-partial-refunds.md"

const sampleFile = "---\n" +
	"id: RF02\n" +
	"repo: gateway\n" +
	"depends_on: [RF01]\n" +
	"jira: PAY-124\n" +
	"---\n" +
	"# RF02 — Expose partial refunds in the public API\n" +
	"\n" +
	"**Size** M · **Repository** gateway · **Depends on** RF01\n" +
	"\n" +
	"## Goal\n" +
	"Expose it.\n" +
	"\n" +
	"## Acceptance\n" +
	"- POST /v1/refunds takes an amount\n" +
	"- an amount over the payment is a 422\n" +
	"  with the reason\n" +
	"- [ ] a checkbox item\n" +
	"* a starred item\n" +
	"\n" +
	"```\n" +
	"- inside a fence\n" +
	"```\n" +
	"\n" +
	"## Notes\n" +
	"- not an acceptance item\n"

func TestParseTaskFile(t *testing.T) {
	got, err := ParseTaskFile(sampleName, []byte(sampleFile))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "RF02" || got.Repo != "gateway" || got.Jira != "PAY-124" {
		t.Errorf("front matter: %+v", got)
	}
	if !reflect.DeepEqual(got.DependsOn, []string{"RF01"}) {
		t.Errorf("DependsOn = %v", got.DependsOn)
	}
	if got.Title != "Expose partial refunds in the public API" {
		t.Errorf("Title = %q", got.Title)
	}
	wantAcc := []string{"POST /v1/refunds takes an amount", "an amount over the payment is a 422 with the reason", "a checkbox item", "a starred item"}
	if !reflect.DeepEqual(got.Acceptance, wantAcc) {
		t.Errorf("Acceptance = %q, want %q", got.Acceptance, wantAcc)
	}
	if !strings.HasPrefix(got.Body, "\n## Goal\n") || strings.Contains(got.Body, "**Size**") {
		t.Errorf("Body should start after the generated line: %q", got.Body)
	}
}

func TestParseTaskFileEmptyAndMissingOptionalKeys(t *testing.T) {
	cases := []struct{ name, fm string }{
		{"empty list", "id: RF01\nrepo: billing\ndepends_on: []\n"},
		{"empty value", "id: RF01\nrepo: billing\ndepends_on:\n"},
		{"absent", "id: RF01\nrepo: billing\n"},
		{"blank lines", "id: RF01\n\nrepo: billing\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTaskFile("TASK-RF01-a.md", []byte("---\n"+tc.fm+"---\n# RF01 — A\n"))
			if err != nil {
				t.Fatal(err)
			}
			if got.DependsOn != nil || got.Jira != "" {
				t.Errorf("got %+v", got)
			}
		})
	}
}

func TestParseTaskFileJira(t *testing.T) {
	for _, v := range []string{"none", "PAY-124", "AB1_C-7"} {
		got, err := ParseTaskFile("TASK-RF01-a.md", []byte("---\nid: RF01\nrepo: billing\njira: "+v+"\n---\n# RF01 — A\n"))
		if err != nil || got.Jira != v {
			t.Errorf("jira %q: %+v, %v", v, got, err)
		}
	}
}

func TestParseTaskFileDependsOnList(t *testing.T) {
	got, err := ParseTaskFile("TASK-RF03-a.md", []byte("---\nid: RF03\nrepo: billing\ndepends_on: [RF01,  RF02 ]\n---\n# RF03 — A\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.DependsOn, []string{"RF01", "RF02"}) {
		t.Errorf("DependsOn = %v", got.DependsOn)
	}
}

func TestParseTaskFileErrors(t *testing.T) {
	const head = "# RF01 — A\n"
	fm := func(lines string) []byte { return []byte("---\n" + lines + "---\n" + head) }
	cases := []struct {
		name string
		file string
		data []byte
		want []string
	}{
		{"unknown key", "TASK-RF01-a.md", fm("id: RF01\nrepo: billing\nowner: me\n"), []string{`"owner"`, "TASK-RF01-a.md"}},
		{"duplicate key", "TASK-RF01-a.md", fm("id: RF01\nrepo: billing\nrepo: gateway\n"), []string{`"repo"`, "twice", "TASK-RF01-a.md"}},
		{"id missing", "TASK-RF01-a.md", fm("repo: billing\n"), []string{`"id"`, "missing", "TASK-RF01-a.md"}},
		{"id shape", "TASK-RF01-a.md", fm("id: rf01\nrepo: billing\n"), []string{`"id"`, "TASK-RF01-a.md"}},
		{"id differs from the file name", "TASK-RF02-a.md", fm("id: RF01\nrepo: billing\n"), []string{`"id"`, "RF01", "RF02", "TASK-RF02-a.md"}},
		{"repo missing", "TASK-RF01-a.md", fm("id: RF01\n"), []string{`"repo"`, "missing", "TASK-RF01-a.md"}},
		{"repo shape", "TASK-RF01-a.md", fm("id: RF01\nrepo: ../x\n"), []string{`"repo"`, "TASK-RF01-a.md"}},
		{"depends_on without brackets", "TASK-RF01-a.md", fm("id: RF01\nrepo: billing\ndepends_on: RF02\n"), []string{`"depends_on"`, "TASK-RF01-a.md"}},
		{"depends_on bad id", "TASK-RF01-a.md", fm("id: RF01\nrepo: billing\ndepends_on: [RF02, x]\n"), []string{`"depends_on"`, "TASK-RF01-a.md"}},
		{"depends_on twice", "TASK-RF01-a.md", fm("id: RF01\nrepo: billing\ndepends_on: [RF02, RF02]\n"), []string{`"depends_on"`, "RF02", "TASK-RF01-a.md"}},
		{"depends_on empty item", "TASK-RF01-a.md", fm("id: RF01\nrepo: billing\ndepends_on: [RF02,]\n"), []string{`"depends_on"`, "TASK-RF01-a.md"}},
		{"jira empty", "TASK-RF01-a.md", fm("id: RF01\nrepo: billing\njira:\n"), []string{`"jira"`, "TASK-RF01-a.md"}},
		{"jira lowercase", "TASK-RF01-a.md", fm("id: RF01\nrepo: billing\njira: pay-1\n"), []string{`"jira"`, `"none"`, "TASK-RF01-a.md"}},
		{"jira other word", "TASK-RF01-a.md", fm("id: RF01\nrepo: billing\njira: later\n"), []string{`"jira"`, "TASK-RF01-a.md"}},
		{"no front matter", "TASK-RF01-a.md", []byte(head), []string{"front matter", "TASK-RF01-a.md"}},
		{"front matter not closed", "TASK-RF01-a.md", []byte("---\nid: RF01\nrepo: billing\n"), []string{"not closed", "TASK-RF01-a.md"}},
		{"line without a colon", "TASK-RF01-a.md", fm("id: RF01\nrepo billing\n"), []string{"line 3", "TASK-RF01-a.md"}},
		{"indented line", "TASK-RF01-a.md", fm("id: RF01\n  repo: billing\n"), []string{"line 3", "TASK-RF01-a.md"}},
		{"file name", "task.md", fm("id: RF01\nrepo: billing\n"), []string{"TASK-<ID>-<slug>.md", "task.md"}},
		{"not UTF-8", "TASK-RF01-a.md", []byte("---\nid: RF01\nrepo: billing\n---\n# RF01 — A \xff\n"), []string{"UTF-8", "TASK-RF01-a.md"}},
		{"heading missing", "TASK-RF01-a.md", []byte("---\nid: RF01\nrepo: billing\n---\n\n\n"), []string{"heading", "TASK-RF01-a.md"}},
		{"heading not first", "TASK-RF01-a.md", []byte("---\nid: RF01\nrepo: billing\n---\ntext\n# RF01 — A\n"), []string{"heading", "TASK-RF01-a.md"}},
		{"heading with another id", "TASK-RF01-a.md", []byte("---\nid: RF01\nrepo: billing\n---\n# RF02 — A\n"), []string{"RF02", "RF01", "TASK-RF01-a.md"}},
		{"heading with a hyphen", "TASK-RF01-a.md", []byte("---\nid: RF01\nrepo: billing\n---\n# RF01 - A\n"), []string{"heading", "TASK-RF01-a.md"}},
		{"heading without a title", "TASK-RF01-a.md", []byte("---\nid: RF01\nrepo: billing\n---\n# RF01 — \n"), []string{"title", "TASK-RF01-a.md"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseTaskFile(tc.file, tc.data)
			if err == nil {
				t.Fatal("want an error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestParseTaskFileBodyLimit(t *testing.T) {
	const prefix = "---\nid: RF01\nrepo: billing\n---\n# RF01 — A\n"
	ok := prefix + strings.Repeat("a", MaxBodyBytes)
	got, err := ParseTaskFile("TASK-RF01-a.md", []byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Body) != MaxBodyBytes {
		t.Errorf("len(Body) = %d", len(got.Body))
	}
	_, err = ParseTaskFile("TASK-RF01-a.md", []byte(ok+"a"))
	if err == nil || !strings.Contains(err.Error(), "TASK-RF01-a.md") || !strings.Contains(err.Error(), "limit") {
		t.Errorf("one byte over the limit: %v", err)
	}
	// the As built record is Go's and is not counted
	big := prefix + strings.Repeat("a", MaxBodyBytes-1) + "\n## As built (2026-10-20)\n\n" + strings.Repeat("b", MaxBodyBytes)
	if _, err := ParseTaskFile("TASK-RF01-a.md", []byte(big)); err != nil {
		t.Errorf("As built counted against the limit: %v", err)
	}
}

func TestParseTaskFileStopsAtAsBuilt(t *testing.T) {
	data := sampleFile + "\n## As built (2026-10-20)\n\n## Acceptance\n- not a criterion\n"
	got, err := ParseTaskFile(sampleName, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Body, "As built") || len(got.Acceptance) != 4 {
		t.Errorf("Body %q, Acceptance %q", got.Body, got.Acceptance)
	}
}

func TestParseTaskFileCRLF(t *testing.T) {
	data := strings.ReplaceAll(sampleFile, "\n", "\r\n")
	got, err := ParseTaskFile(sampleName, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "RF02" || got.Title != "Expose partial refunds in the public API" || len(got.Acceptance) != 4 || got.Acceptance[1] != "an amount over the payment is a 422 with the reason" {
		t.Errorf("got %+v", got)
	}
}

func TestParseTaskFileNoTrailingNewline(t *testing.T) {
	got, err := ParseTaskFile("TASK-RF01-a.md", []byte("---\nid: RF01\nrepo: billing\n---\n# RF01 — A"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "A" || got.Body != "" {
		t.Errorf("got %+v", got)
	}
}

func TestParseTaskFileWithoutGeneratedLine(t *testing.T) {
	got, err := ParseTaskFile("TASK-RF01-a.md", []byte("---\nid: RF01\nrepo: billing\n---\n# RF01 — A\n\n## Goal\nx\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "\n## Goal\nx\n" {
		t.Errorf("Body = %q", got.Body)
	}
}

func TestRenderTaskFileRoundTrip(t *testing.T) {
	cases := []Task{
		{ID: "RF01", Repo: "billing", Title: "Refund amount", Body: "\n## Goal\nStore it.\n\n## Acceptance\n- stored\n- shown\n", Acceptance: []string{"stored", "shown"}},
		{ID: "RF02", Repo: "gateway", DependsOn: []string{"RF01"}, Jira: "PAY-124", Title: "Expose partial refunds в API", Body: "\n## Goal\nПоказать.\n\n## Acceptance\n- поле amount\n", Acceptance: []string{"поле amount"}},
		{ID: "RF03", Repo: "billing", DependsOn: []string{"RF01", "RF02"}, Jira: "none", Title: "Report", Body: ""},
	}
	for _, task := range cases {
		t.Run(task.ID, func(t *testing.T) {
			data := RenderTaskFile(task, "M")
			got, err := ParseTaskFile("TASK-"+task.ID+"-x.md", data)
			if err != nil {
				t.Fatalf("%v\n%s", err, data)
			}
			if !reflect.DeepEqual(got, task) {
				t.Errorf("parsed\n%+v\nwant\n%+v\n%s", got, task, data)
			}
			again := RenderTaskFile(got, "M")
			if string(again) != string(data) {
				t.Errorf("render is not byte-stable:\n%s\n---\n%s", again, data)
			}
			if string(RenderTaskFile(task, "M")) != string(data) {
				t.Error("render is not deterministic")
			}
		})
	}
}

func TestRenderTaskFileLayout(t *testing.T) {
	task := Task{ID: "RF02", Repo: "gateway", DependsOn: []string{"RF01"}, Title: "Expose", Body: "\n## Goal\nx\n"}
	want := "---\nid: RF02\nrepo: gateway\ndepends_on: [RF01]\n---\n# RF02 — Expose\n\n**Size** M · **Repository** gateway · **Depends on** RF01\n\n## Goal\nx\n"
	if got := string(RenderTaskFile(task, "M")); got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
	task.DependsOn = nil
	want = "---\nid: RF02\nrepo: gateway\ndepends_on: []\n---\n# RF02 — Expose\n\n**Repository** gateway · **Depends on** —\n\n## Goal\nx\n"
	if got := string(RenderTaskFile(task, "")); got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestFrozenHash(t *testing.T) {
	base := FrozenHash([]byte(sampleFile))
	if len(base) != 64 {
		t.Fatalf("hash %q", base)
	}

	sections := []string{
		"\n## As built (2026-10-20)\n\nRun `x` in gateway.\n",
		"\n## As built\n\nx\n",
		"\r\n## As built (2026-10-20)\r\n\r\nx\r\n",
	}
	for _, s := range sections {
		if got := FrozenHash([]byte(sampleFile + s)); got != base {
			t.Errorf("As built section %q changed the hash", s)
		}
	}
	two := sampleFile + sections[0] + "\n## As built (2026-10-21)\n\nAgain.\n"
	if FrozenHash([]byte(two)) != base {
		t.Error("a second As built section changed the hash")
	}

	edits := []struct{ name, from, to string }{
		{"front matter", "repo: gateway", "repo: billing"},
		{"heading", "Expose partial", "Expose all"},
		{"body", "Expose it.", "Expose it!"},
		{"acceptance", "- an amount", "- the amount"},
		{"last line", "not an acceptance item", "not an acceptance item."},
		{"indentation", "  with the reason", "with the reason"},
	}
	for _, e := range edits {
		edited := strings.Replace(sampleFile, e.from, e.to, 1)
		if edited == sampleFile {
			t.Fatalf("%s: the edit changed nothing", e.name)
		}
		if FrozenHash([]byte(edited)) == base {
			t.Errorf("an edit of the %s did not change the hash", e.name)
		}
		if FrozenHash([]byte(edited+sections[0])) == base {
			t.Errorf("an edit of the %s above the As built went unseen", e.name)
		}
	}

	if FrozenHash([]byte(sampleFile+"\nsome text\n")) == base {
		t.Error("text appended without an As built heading must count")
	}
}

func TestFrozenHashHeadingBoundary(t *testing.T) {
	notHeadings := []string{
		"## As builtin\n",
		" ## As built\n",
		"### As built\n",
		"## as built\n",
		"text ## As built\n",
	}
	base := FrozenHash([]byte(sampleFile))
	for _, s := range notHeadings {
		if FrozenHash([]byte(sampleFile+s)) == base {
			t.Errorf("%q must not end the frozen part", s)
		}
	}
}

func TestFrozenHashIgnoresTrailingLineBreaks(t *testing.T) {
	a := FrozenHash([]byte("x\ny"))
	if FrozenHash([]byte("x\ny\n")) != a || FrozenHash([]byte("x\ny\n\n")) != a || FrozenHash([]byte("x\ny\r\n")) != a {
		t.Error("trailing line breaks must not matter")
	}
	if FrozenHash([]byte("x\ny ")) == a {
		t.Error("trailing spaces are content")
	}
}

func TestSetJira(t *testing.T) {
	t.Run("adds the line last in the front matter", func(t *testing.T) {
		in := "---\nid: RF01\nrepo: billing\ndepends_on: []\n---\n# RF01 — A\n"
		got, err := SetJira([]byte(in), "PAY-7")
		if err != nil {
			t.Fatal(err)
		}
		want := "---\nid: RF01\nrepo: billing\ndepends_on: []\njira: PAY-7\n---\n# RF01 — A\n"
		if string(got) != want {
			t.Errorf("got %q", got)
		}
	})
	t.Run("replaces the value", func(t *testing.T) {
		got, err := SetJira([]byte(sampleFile), "PAY-999")
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.Replace(sampleFile, "jira: PAY-124", "jira: PAY-999", 1); string(got) != want {
			t.Errorf("got %q", got)
		}
	})
	t.Run("none", func(t *testing.T) {
		got, err := SetJira([]byte(sampleFile), "none")
		if err != nil {
			t.Fatal(err)
		}
		if want := strings.Replace(sampleFile, "jira: PAY-124", "jira: none", 1); string(got) != want {
			t.Errorf("got %q", got)
		}
	})
	t.Run("same key twice is the same bytes", func(t *testing.T) {
		once, err := SetJira([]byte("---\nid: RF01\nrepo: billing\n---\n# RF01 — A\n"), "PAY-7")
		if err != nil {
			t.Fatal(err)
		}
		twice, err := SetJira(once, "PAY-7")
		if err != nil {
			t.Fatal(err)
		}
		if string(once) != string(twice) {
			t.Errorf("%q != %q", once, twice)
		}
	})
	t.Run("keeps CRLF", func(t *testing.T) {
		in := strings.ReplaceAll("---\nid: RF01\nrepo: billing\n---\n# RF01 — A\n", "\n", "\r\n")
		got, err := SetJira([]byte(in), "PAY-7")
		if err != nil {
			t.Fatal(err)
		}
		want := strings.ReplaceAll("---\nid: RF01\nrepo: billing\njira: PAY-7\n---\n# RF01 — A\n", "\n", "\r\n")
		if string(got) != want {
			t.Errorf("got %q", got)
		}
		again, err := SetJira(got, "PAY-8")
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != strings.Replace(want, "PAY-7", "PAY-8", 1) {
			t.Errorf("replace under CRLF: %q", again)
		}
	})
	t.Run("file ends at the closing line", func(t *testing.T) {
		got, err := SetJira([]byte("---\nid: RF01\n---"), "PAY-7")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "---\nid: RF01\njira: PAY-7\n---" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("the result still parses", func(t *testing.T) {
		in := RenderTaskFile(Task{ID: "RF01", Repo: "billing", Title: "A", Body: "\n## Goal\nx\n"}, "S")
		got, err := SetJira(in, "PAY-7")
		if err != nil {
			t.Fatal(err)
		}
		task, err := ParseTaskFile("TASK-RF01-a.md", got)
		if err != nil || task.Jira != "PAY-7" || task.Body != "\n## Goal\nx\n" {
			t.Errorf("%+v, %v", task, err)
		}
	})
	t.Run("refuses a bad key", func(t *testing.T) {
		for _, key := range []string{"", "pay-1", "PAY", "PAY-", "later", "PAY-1\nid: RF09"} {
			if _, err := SetJira([]byte(sampleFile), key); err == nil {
				t.Errorf("key %q accepted", key)
			}
		}
	})
	t.Run("refuses a file without front matter", func(t *testing.T) {
		if _, err := SetJira([]byte("# RF01 — A\n"), "PAY-7"); err == nil {
			t.Error("want an error")
		}
	})
}
