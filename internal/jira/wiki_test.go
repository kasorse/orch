package jira

import (
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

func TestWiki_IsDeterministic(t *testing.T) {
	md := "## Goal\n\n- a `b` **c**\n\n```\nx\n```\n"
	if wikiOf(md) != wikiOf(md) {
		t.Error("two renderings differ")
	}
}
