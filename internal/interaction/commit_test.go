package interaction

import (
	"bytes"
	"strings"
	"testing"

	"orchestrator/internal/domain"
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

// The push and the merge request questions default to no: an empty answer is a no, a typo asks
// again, and a closed stdin is an error the caller reads as no.
func TestAskPushAndMergeRequest(t *testing.T) {
	asks := map[string]func(p Prompter, out *bytes.Buffer) (bool, error){
		"push": func(p Prompter, out *bytes.Buffer) (bool, error) { return AskPush(p, out, "orchestrator/r1", "origin") },
		"merge request": func(p Prompter, out *bytes.Buffer) (bool, error) {
			return AskMergeRequest(p, out, "orchestrator/r1", "main")
		},
	}
	cases := []struct {
		answers []string
		want    bool
	}{
		{[]string{"y"}, true}, {[]string{"YES"}, true}, {[]string{"n"}, false}, {[]string{"no"}, false},
		{[]string{""}, false}, {[]string{"maybe", "y"}, true}, {[]string{"maybe", ""}, false},
	}
	for name, ask := range asks {
		for _, c := range cases {
			p := &fakePrompter{answers: c.answers}
			var out bytes.Buffer
			got, err := ask(p, &out)
			if err != nil || got != c.want || p.idx != len(c.answers) {
				t.Errorf("%s %q: got %v, %v after %d answers; want %v after %d", name, c.answers, got, err, p.idx, c.want, len(c.answers))
			}
		}
		if got, err := ask(&fakePrompter{}, new(bytes.Buffer)); err == nil || got {
			t.Errorf("%s with no answer = %v, %v; want false and an error", name, got, err)
		}
	}
}

func TestAskPushAndMergeRequest_Prompts(t *testing.T) {
	var prompts []string
	record := promptFunc(func(prompt string) (string, error) { prompts = append(prompts, prompt); return "", nil })
	var out bytes.Buffer
	if _, err := AskPush(record, &out, "orchestrator/r1", "origin"); err != nil {
		t.Fatal(err)
	}
	if _, err := AskMergeRequest(record, &out, "orchestrator/r1", "main"); err != nil {
		t.Fatal(err)
	}
	want := []string{"Push orchestrator/r1 to origin? [y]es / [N]o: ", "Open a merge request orchestrator/r1 → main? [y]es / [N]o: "}
	if strings.Join(prompts, "|") != strings.Join(want, "|") {
		t.Errorf("prompts = %q, want %q", prompts, want)
	}
}

type promptFunc func(string) (string, error)

func (f promptFunc) Ask(prompt string) (string, error) { return f(prompt) }

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

// The offer lists the rules that need a person's yes, with their files, before the question.
func TestAskCommit_PathRules(t *testing.T) {
	p := &fakePrompter{answers: []string{"y"}}
	var out bytes.Buffer
	offer := CommitOffer{Branch: "main", Changes: " M migrations/a.sql", Header: "Fix a", Body: "Orchestrator-Run: x\n",
		PathRules: []domain.PathRuleMatch{
			{Match: "migrations/**", Files: []string{"migrations/a.sql", "migrations/b.sql"}, Approval: domain.PathApprovalManual},
			{Match: "**/*.sql", Files: []string{"migrations/a.sql"}, Approval: domain.PathApprovalManual},
		}}
	if _, ok, err := AskCommit(p, &out, offer); err != nil || !ok {
		t.Fatalf("got ok=%v err=%v", ok, err)
	}
	want := "\nChanges:\n   M migrations/a.sql\n" +
		"\npolicy.paths requires your approval for:\n" +
		"  migrations/**\n    migrations/a.sql\n    migrations/b.sql\n" +
		"  **/*.sql\n    migrations/a.sql\n" +
		"\nCommit message:\n  Fix a\n\n  Orchestrator-Run: x\n"
	if out.String() != want {
		t.Errorf("output:\n%q\nwant:\n%q", out.String(), want)
	}

	out.Reset()
	offer.PathRules = nil
	p = &fakePrompter{answers: []string{"n"}}
	if _, _, err := AskCommit(p, &out, offer); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "policy.paths") {
		t.Errorf("an offer without rules mentions them:\n%s", out.String())
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
