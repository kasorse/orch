package interaction

import (
	"bytes"
	"strings"
	"testing"
)

func TestAskCommit(t *testing.T) {
	offer := CommitOffer{Branch: "main", Changes: " M a.go", Header: "Fix a", Body: "Orchestrator-Run: x\n"}
	cases := []struct {
		name    string
		answers []string
		header  string
		ok      bool
	}{
		{"yes", []string{"y"}, "Fix a", true},
		{"default is no", []string{""}, "", false},
		{"no", []string{"n"}, "", false},
		{"edit then yes", []string{"e", "Better header", "yes"}, "Better header", true},
		{"empty edit keeps header", []string{"e", "  ", "y"}, "Fix a", true},
		{"unrecognized asks again", []string{"maybe", "y"}, "Fix a", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &fakePrompter{answers: c.answers}
			var out bytes.Buffer
			header, ok, err := AskCommit(p, &out, offer)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if header != c.header || ok != c.ok {
				t.Errorf("got (%q, %v), want (%q, %v)", header, ok, c.header, c.ok)
			}
			if p.idx != len(c.answers) {
				t.Errorf("consumed %d answers, want %d", p.idx, len(c.answers))
			}
		})
	}
}

func TestAskCommit_ShowsChangesWarningsAndMessage(t *testing.T) {
	p := &fakePrompter{answers: []string{"n"}}
	var out bytes.Buffer
	offer := CommitOffer{Changes: " M a.go\n?? b.go", Warnings: []string{"review did not pass"},
		Header: "Fix a", Body: "Orchestrator-Run: x\n"}
	if _, _, err := AskCommit(p, &out, offer); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"   M a.go\n  ?? b.go", "Warning: review did not pass", "  Fix a\n\n  Orchestrator-Run: x"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// Without a proposed header the operator writes it; an empty one commits nothing, and a
// written one still needs the explicit yes.
func TestAskCommit_NoProposedHeader(t *testing.T) {
	offer := CommitOffer{Branch: "main", Changes: " M a.go", Body: "Orchestrator-Run: x\n"}
	cases := []struct {
		name    string
		answers []string
		header  string
		ok      bool
	}{
		{"written then yes", []string{" Fix a ", "y"}, "Fix a", true},
		{"written then default no", []string{"Fix a", ""}, "", false},
		{"empty skips", []string{"  "}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &fakePrompter{answers: c.answers}
			var out bytes.Buffer
			header, ok, err := AskCommit(p, &out, offer)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if header != c.header || ok != c.ok {
				t.Errorf("got (%q, %v), want (%q, %v)", header, ok, c.header, c.ok)
			}
			if p.idx != len(c.answers) {
				t.Errorf("consumed %d answers, want %d", p.idx, len(c.answers))
			}
			if !strings.Contains(out.String(), "proposed no commit message") {
				t.Errorf("output does not say why it asks:\n%s", out.String())
			}
		})
	}
	if _, ok, err := AskCommit(&fakePrompter{}, &bytes.Buffer{}, offer); err == nil || ok {
		t.Errorf("closed input: got ok=%v err=%v, want an error", ok, err)
	}
}

func TestAskCommit_InputClosed(t *testing.T) {
	_, ok, err := AskCommit(&fakePrompter{}, &bytes.Buffer{}, CommitOffer{Header: "x"})
	if err == nil || ok {
		t.Errorf("got ok=%v err=%v, want an error", ok, err)
	}
}
