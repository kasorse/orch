package jira

import (
	"strings"
	"testing"
)

func TestBuildJQL(t *testing.T) {
	got := BuildJQL([]string{"PAY", "OPS"}, "ai-run", []string{"To Do"}, []string{"5b10a2844c20165700ede21g", "jdoe"})
	want := `project in ("PAY", "OPS") AND labels = "ai-run" AND status in ("To Do") ` +
		`AND reporter in ("5b10a2844c20165700ede21g", "jdoe") ORDER BY priority DESC, created ASC`
	if got != want {
		t.Errorf("BuildJQL =\n%s\nwant\n%s", got, want)
	}

	one := BuildJQL([]string{"PAY"}, "ai-run", []string{"To Do"}, []string{"x"})
	if want := `project in ("PAY") AND labels = "ai-run" AND status in ("To Do") AND reporter in ("x") ORDER BY priority DESC, created ASC`; one != want {
		t.Errorf("BuildJQL =\n%s\nwant\n%s", one, want)
	}
}

func TestBuildJQL_PanicsOnAValueThatCouldEscapeItsString(t *testing.T) {
	for _, bad := range []string{`a"b`, `a\b`, "a\nb", "a\rb", "a\tb", "a\x00b", "a\x7fb"} {
		positions := map[string]func(){
			"project":  func() { BuildJQL([]string{bad}, "l", []string{"s"}, []string{"r"}) },
			"label":    func() { BuildJQL([]string{"P"}, bad, []string{"s"}, []string{"r"}) },
			"status":   func() { BuildJQL([]string{"P"}, "l", []string{"s", bad}, []string{"r"}) },
			"reporter": func() { BuildJQL([]string{"P"}, "l", []string{"s"}, []string{bad}) },
		}
		for name, call := range positions {
			func() {
				defer func() {
					r := recover()
					if r == nil {
						t.Errorf("%s %q: no panic", name, bad)
						return
					}
					if msg, _ := r.(string); !strings.Contains(msg, "config validation") {
						t.Errorf("%s %q: panic = %v", name, bad, r)
					}
				}()
				call()
			}()
		}
	}
}

func TestBuildJQL_AcceptsOrdinaryValues(t *testing.T) {
	// Spaces, dashes, dots, non-ASCII text and single quotes are fine inside double quotes.
	got := BuildJQL([]string{"P_1"}, "ai-run", []string{"In Review", "Прочее", "it's"}, []string{"a.b@c"})
	if !strings.Contains(got, `status in ("In Review", "Прочее", "it's")`) {
		t.Errorf("BuildJQL = %s", got)
	}
}
