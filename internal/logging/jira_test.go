package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/internal/domain"
)

const renderedTask = `PMC-2789: FE[catalog]: Tags/Labels disable reason not shown on hover in the editor
Type: Bug
Parent: PMC-2663 — Tags & Labels
Source: https://yourcompany.atlassian.net/browse/PMC-2789

Description:
Steps to reproduce:
# Hover {{Tags}}

Operator notes:
only the FE part; leave pim-qa alone
`

// TestTicketLines_Golden prints the ticket lines in the order taskFromJira does.
func TestTicketLines_Golden(t *testing.T) {
	ticket := domain.Ticket{Key: "PMC-2789", Type: "Bug", Summary: "FE[catalog]: ..."}
	moved := ticket
	moved.RequestedKey = "PMC-1"

	cases := []struct {
		name   string
		ticket domain.Ticket
	}{
		{"task_block", ticket},
		{"task_block_moved", moved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			render := func(verbose bool) string {
				var buf bytes.Buffer
				r := NewConsoleReporter(&buf)
				r.Verbose = verbose
				asked := tc.ticket.Key
				if tc.ticket.RequestedKey != "" {
					asked = tc.ticket.RequestedKey
				}
				r.FetchingTicket(asked, "yourcompany.atlassian.net")
				r.TicketFetched(tc.ticket, 320*time.Millisecond)
				if tc.ticket.RequestedKey != "" {
					r.TicketMoved(tc.ticket.RequestedKey, tc.ticket.Key)
				}
				r.TaskBlock(tc.ticket.Key, renderedTask)
				return buf.String()
			}
			got := render(false)
			if verbose := render(true); verbose != got {
				t.Errorf("--verbose changed the ticket lines:\n%s", verbose)
			}

			path := filepath.Join("testdata", tc.name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (go test ./internal/logging -update): %v", err)
			}
			if got != string(want) {
				t.Errorf("output differs from %s:\n%s", path, got)
			}
		})
	}
}

// A text without a final newline still ends its last line.
func TestTaskBlock_NoFinalNewline(t *testing.T) {
	var buf bytes.Buffer
	NewConsoleReporter(&buf).TaskBlock("PMC-1", "one\ntwo")
	if want := "\nTask (from PMC-1):\n  | one\n  | two\n\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}
