package jira

import (
	"fmt"
	"strings"
	"testing"
)

const wikiTrailer = "\n\n----\nRepository: r · Spec: p · Depends on: —\n"

// wikiOf renders body for the repository r and the spec path p, without dependencies.
func wikiOf(body string) string { return Wiki(WikiTask{Body: body, Repo: "r", SpecPath: "p"}) }

func TestWiki_Structure(t *testing.T) {
	cases := []struct {
		name, md, want string
	}{
		{"headings", "\n## Goal\n\nExpose them.\n\n### Detail\n\ntext\n",
			"h2. Goal\n\nExpose them.\n\nh3. Detail\n\ntext"},
		{"bullets and numbers", "- one\n- two\n  - nested\n    - deeper\n- three\n\n1. a\n2. b\n   1. c\n",
			"* one\n* two\n** nested\n*** deeper\n* three\n\n# a\n# b\n## c"},
		{"star and plus markers", "* a\n+ b\n", "* a\n* b"},
		{"tab indent", "- a\n\t- b\n", "* a\n** b"},
		{"mixed nesting", "1. a\n   - b\n", "# a\n#* b"},
		{"code block", "Run:\n\n```go\nx := a[1] * b\n```\nDone.\n",
			"Run:\n\n{code:go}\nx := a[1] * b\n{code}\nDone."},
		{"code block without language", "```\nplain\n```\n", "{code}\nplain\n{code}"},
		{"unsafe language", "```go{x}\nplain\n```\n", "{code}\nplain\n{code}"},
		{"unclosed code block", "```\nabc", "{code}\nabc\n{code}"},
		{"code cannot end its block early", "```\n{code}\n{color:red}\n```\n", "{code}\n{ code}\n{color:red}\n{code}"},
		{"blank lines inside code are kept", "```\na\n\n\nb\n```\n", "{code}\na\n\n\nb\n{code}"},
		{"inline code", "Call `Refund()` now.", "Call {{Refund()}} now."},
		{"inline code is escaped", "a `x}}{{y*` b", `a {{x\}\}\{\{y\*}} b`},
		{"double backticks", "``a`b``", "{{a`b}}"},
		{"unmatched backtick", "a ` b", "a ` b"},
		{"bold", "**strong** and **a*b** text", `*strong* and *a\*b* text`},
		{"unmatched bold", "**strong", `\*\*strong`},
		{"rule", "a\n\n---\n\nb", "a\n\n----\n\nb"},
		{"quote", "> note *x*", `bq. note \*x\*`},
		{"heading text is escaped", "## Use *x* [here]", `h2. Use \*x\* \[here\]`},
		{"CRLF", "## A\r\n\r\n- b\r\n", "h2. A\n\n* b"},
		{"collapsed blank lines", "a\n\n\n\nb\n", "a\n\nb"},
		{"list item text is escaped", "- a_b_ *c*", `* a\_b\_ \*c\*`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wikiOf(tc.md); got != tc.want+wikiTrailer {
				t.Errorf("Wiki(%q) =\n%q\nwant\n%q", tc.md, got, tc.want+wikiTrailer)
			}
		})
	}
}

func TestWiki_EscapesWhatWikiWouldReadAsMarkup(t *testing.T) {
	cases := []struct {
		name, md, want string
	}{
		{"emphasis", "a *b* _c_ +d+ -e- ^f^ ~g~ ??h??", `a \*b\* \_c\_ \+d\+ \-e\- \^f\^ \~g\~ \?\?h\?\?`},
		{"mention", "[~admin] and [~accountid:5b10]", `\[\~admin\] and \[\~accountid:5b10\]`},
		{"link", "[click|http://evil.example]", `\[click\|http://evil.example\]`},
		{"macro", "{noformat}{panel:title=x}{color:red}", `\{noformat\}\{panel:title=x\}\{color:red\}`},
		{"table", "| a | b |\n|---|---|", `\| a \| b \|` + "\n" + `\|\-\-\-\|\-\-\-\|`},
		{"image", "!x.png!", `\!x.png\!`},
		{"heading in text", "h1. Title", zeroWidth + "h1. Title"},
		{"heading in text, other case", "H3. Title", zeroWidth + "H3. Title"},
		{"quote in text", "bq. quoted", zeroWidth + "bq. quoted"},
		{"list marker in text", "#hash", `\#hash`},
		{"backslash", `a\b`, `a\` + zeroWidth + `b`},
		{"backslash before a special", `\*x*`, `\` + zeroWidth + `\*x\*`},
		{"double backslash", `a\\b`, `a\` + zeroWidth + `\` + zeroWidth + `b`},
		{"plain text is untouched", "Expose partial refunds (v2) in the API: 100% & more.", "Expose partial refunds (v2) in the API: 100% & more."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wikiOf(tc.md); got != tc.want+wikiTrailer {
				t.Errorf("Wiki(%q) =\n%q\nwant\n%q", tc.md, got, tc.want+wikiTrailer)
			}
		})
	}
}

func TestWiki_NoLiveMarkupFromHostileText(t *testing.T) {
	hostile := "[~admin] {code} {panel} *x* _y_ [a|http://x] !i.png! | -z- ##\\\\ h2. q"
	out := wikiOf(hostile)
	body, _, _ := strings.Cut(out, wikiTrailer[:6])
	for i, r := range body {
		if !strings.ContainsRune(`{}[]*_+-^~|!?`, r) {
			continue
		}
		if i == 0 || body[i-1] != '\\' {
			t.Errorf("%q at %d is not escaped in\n%q", r, i, body)
		}
	}
}

func TestWiki_Trailer(t *testing.T) {
	got := Wiki(WikiTask{
		Body: "## Goal\n\nx\n", Repo: "billing", SpecPath: "specs/refunds/tasks/TASK-RF02-expose.md",
		DependencyKeys: []string{"PAY-123", "PAY-124"},
	})
	want := "h2. Goal\n\nx\n\n----\n" +
		`Repository: billing · Spec: specs/refunds/tasks/TASK\-RF02\-expose.md · Depends on: PAY\-123, PAY\-124` + "\n"
	if got != want {
		t.Errorf("Wiki =\n%q\nwant\n%q", got, want)
	}
}

func TestWiki_EmptyBody(t *testing.T) {
	if got, want := wikiOf("  \n\n"), "Repository: r · Spec: p · Depends on: —\n"; got != want {
		t.Errorf("Wiki = %q, want %q", got, want)
	}
}

// listenerTask is the description of a child of an Epic's plan (specs/listener-story/SPEC.md §8.2).
func listenerTask() WikiTask {
	return WikiTask{
		Body:        "## Goal\n\nDo it.",
		Epic:        "PMC-1",
		EpicSummary: "Big [thing]",
		Service:     "sku-master",
		ServicePath: "g/pim/sku-master",
		References:  []string{"https://gitlab.example/g/pim/product-master"},
		Needs:       []WikiNeed{{Key: "PMC-3", Service: "sku-fe"}},
		Approach:    "Use the dictionary.",
		Contracts:   "GET /x",
	}
}

func TestWiki_ListenerChild(t *testing.T) {
	want := "h2. Goal\n\nDo it.\n\n----\n" +
		`Story: PMC-1 — Big \[thing\]` + "\n" +
		`Service: sku\-master (g/pim/sku\-master)` + "\n\n" +
		"References:\n* [https://gitlab.example/g/pim/product-master]\n\n" +
		`Needs:` + "\n" + `* PMC-3 (sku\-fe), not linked` + "\n\n" +
		"h3. From the plan\nh4. Approach\n\nUse the dictionary.\n\nh4. Contracts\n\nGET /x\n"
	if got := Wiki(listenerTask()); got != want {
		t.Errorf("Wiki =\n%q\nwant\n%q", got, want)
	}
}

func TestWiki_ListenerChildWithoutReferencesNeedsOrPlan(t *testing.T) {
	got := Wiki(WikiTask{Body: "## Goal\n\nx", Epic: "PMC-1", Service: "svc"})
	want := "h2. Goal\n\nx\n\n----\nStory: PMC-1\nService: svc\n"
	if got != want {
		t.Errorf("Wiki =\n%q\nwant\n%q", got, want)
	}
}

func TestWiki_ListenerChildEscapesAReferenceThatIsNotASafeURL(t *testing.T) {
	task := listenerTask()
	task.References = []string{"https://x.example/a|b]{c}"}
	got := Wiki(task)
	if strings.Contains(got, "[https://x.example/a|b]") || !strings.Contains(got, `* https://x.example/a\|b\]\{c\}`) {
		t.Errorf("an unsafe URL is a live link:\n%s", got)
	}
}

// The story's requirements stand between the child's lines and the plan, escaped (listener-requirements §5).
func TestWiki_ListenerChildListsTheRequirementsBeforeThePlan(t *testing.T) {
	task := listenerTask()
	task.Requirements = []string{"The dictionary is [per country].", "  The editor\n shows it.  "}
	want := "h2. Goal\n\nDo it.\n\n----\n" +
		`Story: PMC-1 — Big \[thing\]` + "\n" +
		`Service: sku\-master (g/pim/sku\-master)` + "\n\n" +
		"References:\n* [https://gitlab.example/g/pim/product-master]\n\n" +
		`Needs:` + "\n" + `* PMC-3 (sku\-fe), not linked` + "\n\n" +
		"h3. Requirements of the story\n" + `* The dictionary is \[per country\].` + "\n* The editor shows it.\n\n" +
		"h3. From the plan\nh4. Approach\n\nUse the dictionary.\n\nh4. Contracts\n\nGET /x\n"
	if got := Wiki(task); got != want {
		t.Errorf("Wiki =\n%q\nwant\n%q", got, want)
	}
}

// A description too long loses its requirements first, from the end, and the plan only after them.
func TestWiki_ListenerChildCutsTheRequirementsFirst(t *testing.T) {
	task := listenerTask()
	for i := 0; i < 10; i++ {
		task.Requirements = append(task.Requirements, fmt.Sprintf("Requirement %d, which goes on for a while to take some room.", i))
	}
	whole := Wiki(task)
	task.MaxBytes = len(whole) - 1
	got := Wiki(task)
	if len(got) > task.MaxBytes || !strings.Contains(got, "Requirement 8,") || strings.Contains(got, "Requirement 9,") ||
		!strings.Contains(got, "\n* …\n\nh3. From the plan\n") || !strings.HasSuffix(got, "GET /x\n") {
		t.Errorf("one byte too many does not cut the last requirement only:\n%s", got)
	}

	// Room for the heading of the requirements and its cut, and for the plan's cut, but not for the plan.
	task.Requirements, task.MaxBytes = nil, 0
	task.MaxBytes = len(Wiki(task)) + 30
	task.Requirements = []string{"Requirement 0, which goes on for a while to take some room."}
	got = Wiki(task)
	if len(got) > task.MaxBytes || strings.Contains(got, "Requirement 0") || !strings.Contains(got, "h3. Requirements of the story\n* …\n") ||
		!strings.HasSuffix(got, "… (cut; the whole plan is in PMC-1 comment)\n") {
		t.Errorf("a description with no room left keeps a requirement or does not cut the plan:\n%s", got)
	}
}

func TestWiki_ListenerChildIsCutToMaxBytesAndNeverLeavesACodeBlockOpen(t *testing.T) {
	var approach strings.Builder
	for i := 0; i < 40; i++ {
		approach.WriteString("A line of the approach that goes on for a while.\n\n")
	}
	approach.WriteString("```go\n")
	for i := 0; i < 40; i++ {
		approach.WriteString("code line of the contract\n")
	}
	approach.WriteString("```\n")
	task := listenerTask()
	task.Approach = approach.String()

	whole := Wiki(task)
	if strings.Contains(whole, "(cut;") {
		t.Fatal("a description without a limit is cut")
	}
	cut := false
	for limit := 400; limit < len(whole); limit += 13 {
		task.MaxBytes = limit
		got := Wiki(task)
		if len(got) > limit {
			t.Fatalf("MaxBytes %d: the description is %d bytes", limit, len(got))
		}
		if !strings.HasSuffix(got, "… (cut; the whole plan is in PMC-1 comment)\n") {
			t.Fatalf("MaxBytes %d: a cut description does not say so:\n%s", limit, got)
		}
		cut = true
		opens := 0
		for _, l := range strings.Split(got, "\n") {
			if strings.HasPrefix(l, "{code") {
				opens++
			}
		}
		if opens%2 != 0 {
			t.Fatalf("MaxBytes %d: a code block is left open:\n%s", limit, got)
		}
	}
	if !cut {
		t.Fatal("no description was cut")
	}
	task.MaxBytes = len(whole)
	if got := Wiki(task); got != whole {
		t.Errorf("a description that fits is changed:\n%s", got)
	}
}

func TestWiki_IsDeterministic(t *testing.T) {
	md := "## Goal\n\n- a `b` **c**\n\n```\nx\n```\n"
	if wikiOf(md) != wikiOf(md) {
		t.Error("two renderings differ")
	}
}
