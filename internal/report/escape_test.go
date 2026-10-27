package report

import (
	"strings"
	"testing"
)

func TestMdText(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"html", "<img src=x>", `\<img src=x\>`},
		{"entity", "&lt;b&gt;", `\&lt;b\&gt;`},
		{"link", "[link](http://x)", `\[link\](http://x)`},
		{"image", "![i](x)", `\!\[i\](x)`},
		{"mention", "@all", `\@all`},
		{"issue", "#1", `\#1`},
		{"merge request", "!2", `\!2`},
		{"label", "~bug", `\~bug`},
		{"milestone", "%v1", `\%v1`},
		{"percent before a space", "100 % sure", "100 % sure"},
		{"code", "`x`", "\\`x\\`"},
		{"quick action", "/merge", " /merge"},
		{"quick action on a later line", "a\n/close\nb", "a\n /close\nb"},
		{"slash inside a line", "a/b", "a/b"},
		{"a backslash cannot cancel an escape", `\@all`, `\\\@all`},
		{"fence", "```go\ncode\n```", "\\`\\`\\`go\ncode\n\\`\\`\\`"},
		{"tilde fence", "~~~", `\~\~\~`},
		{"controls", "a\rb\x00c‮d", "abcd"},
		{"plain", "Add returns the difference (PAY-12)", "Add returns the difference (PAY-12)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := mdText(c.in); got != c.want {
				t.Errorf("mdText(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMdCellAndCode(t *testing.T) {
	if got, want := mdCell("a | b\nc @d"), `a \| b c \@d`; got != want {
		t.Errorf("mdCell = %q, want %q", got, want)
	}
	if got, want := mdCode("a`b|c"), "`a'b\\|c`"; got != want {
		t.Errorf("mdCode = %q, want %q", got, want)
	}
	if got := mdCode(" "); got != noValue {
		t.Errorf("mdCode of nothing = %q", got)
	}
}

// Nothing a task or a model wrote may become markup once it is escaped: every character that
// starts one is behind a backslash, and no line starts with a quick action.
func TestMdText_NothingSurvives(t *testing.T) {
	in := "<script>alert(1)</script> [x](javascript:y) ![i](z) @a #1 !2 ~l %m /merge\n/close\n```\n~~~"
	got := mdText(in)
	for i, r := range got {
		if !strings.ContainsRune("<>&[]!`@#~%", r) {
			continue
		}
		if i == 0 || got[i-1] != '\\' {
			t.Errorf("%q at %d is not escaped in %q", r, i, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "/") {
			t.Errorf("line %q starts with /", line)
		}
	}
}
