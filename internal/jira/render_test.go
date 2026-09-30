package jira

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden from the current output")

func fullTicket() domain.Ticket {
	return domain.Ticket{
		Key:     "PMC-2789",
		URL:     "https://yourcompany.atlassian.net/browse/PMC-2789",
		Type:    "Bug",
		Summary: "FE[catalog]: Tags/Labels disable reason not shown on hover in the editor",
		Description: "\n\nPrecondition:  \nWhile implementing PMC-2766 (locking {{Tags}}/{{Labels}} to read-only)\n\n" +
			"*Impact*: editors cannot tell why the field is locked.\t\n\n" +
			"Steps to reproduce:\n# Open a product\n# Hover {{Tags}}\n\n" +
			"Actual result:\nNothing is shown.\n\nExpected result:\nThe disable reason is shown.\n\n\n",
		Parent: &domain.TicketParent{Key: "PMC-2663", Summary: "Tags & Labels"},
		Fields: []domain.TicketField{
			{ID: "customfield_10035", Label: "Acceptance criteria", Value: "* Reason shown on hover\n* No layout shift"},
			{ID: "customfield_10036", Label: "Design", Value: "[Figma|https://figma.example/f/1]"},
		},
		FetchedAt: time.Date(2026, 9, 30, 10, 12, 0, 0, time.UTC),
	}
}

func minimalTicket() domain.Ticket {
	return domain.Ticket{
		Key: "PMC-1", URL: "https://h.atlassian.net/browse/PMC-1", Type: "Task", Summary: "Bump the Go version",
	}
}

func TestRender_Golden(t *testing.T) {
	crlf := minimalTicket()
	crlf.Description = "Line one\r\nLine two  \r\n\r\nLine four\r\n"
	crlf.Fields = []domain.TicketField{{ID: "customfield_1", Label: "Acceptance criteria", Value: "a\r\nb\r\n"}}

	noDescription := fullTicket()
	noDescription.Description = "  \n\n"

	cases := []struct {
		name   string
		ticket domain.Ticket
		notes  string
	}{
		{"full", fullTicket(), ""},
		{"minimal", minimalTicket(), ""},
		{"no_description", noDescription, ""},
		{"notes", fullTicket(), "\n  only the FE part; leave pim-qa alone  \n\n"},
		{"crlf", crlf, "check\r\nboth"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(tc.ticket, tc.notes, 32768)
			if err != nil {
				t.Fatal(err)
			}
			again, _ := Render(tc.ticket, tc.notes, 32768)
			if again != got {
				t.Error("rendering the same input twice gave different output")
			}
			assertRenderShape(t, got)

			path := filepath.Join("testdata", tc.name+".golden")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (go test ./internal/jira -update): %v", err)
			}
			if got != string(want) {
				t.Errorf("output differs from %s:\n--- got\n%s\n--- want\n%s", path, got, want)
			}
		})
	}
}

// assertRenderShape checks the invariants every rendering keeps: one final newline, no
// \r, and no trailing whitespace on any line.
func assertRenderShape(t *testing.T, out string) {
	t.Helper()
	if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		t.Errorf("output must end with exactly one newline: %q", out)
	}
	if strings.Contains(out, "\r") {
		t.Errorf("output contains \\r: %q", out)
	}
	for i, line := range strings.Split(out, "\n") {
		if strings.TrimRight(line, " \t") != line {
			t.Errorf("line %d has trailing whitespace: %q", i+1, line)
		}
	}
}

func TestRender_OmitsEmptySections(t *testing.T) {
	tk := minimalTicket()
	tk.Parent = &domain.TicketParent{Key: "PMC-9", Summary: ""}
	tk.Fields = []domain.TicketField{{ID: "customfield_1", Label: "Acceptance criteria", Value: " \n "}}
	got, err := Render(tk, "   ", 32768)
	if err != nil {
		t.Fatal(err)
	}
	want := "PMC-1: Bump the Go version\nType: Task\nParent: PMC-9\nSource: https://h.atlassian.net/browse/PMC-1\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestRender_MaxBytes(t *testing.T) {
	tk := fullTicket()
	out, err := Render(tk, "notes", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	n := len(out)

	if _, err := Render(tk, "notes", n); err != nil {
		t.Errorf("exactly at the limit (%d bytes) must pass: %v", n, err)
	}
	_, err = Render(tk, "notes", n-1)
	var cerr *apperr.ConfigError
	if !errors.As(err, &cerr) {
		t.Fatalf("one byte over the limit: want a ConfigError, got %v", err)
	}
	want := "PMC-2789 renders to " + strconv.Itoa(n) + " bytes, more than jira.max_bytes (" + strconv.Itoa(n-1) + "); raise the limit or trim the ticket"
	if err.Error() != want {
		t.Errorf("error =\n%q\nwant\n%q", err, want)
	}
}
