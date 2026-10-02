package logging

import (
	"fmt"
	"io"
	"time"

	"orchestrator/internal/domain"
)

// The ticket lines of run --jira (specs/jira/SPEC.md §13), printed between the
// configuration check and the run header. The rendered task is always printed in full,
// in every mode and whatever --verbose says: the ticket's author wrote this text, and
// the operator must be able to see - also in a CI log - what drove the run.

// FetchingTicket announces the one request, naming the host it goes to.
func (r *ConsoleReporter) FetchingTicket(key, host string) {
	if r.Pretty {
		fmt.Fprintln(r.Out)
		r.startSpin("Fetching "+key, host)
		return
	}
	fmt.Fprintf(r.Out, "\nFetching %s from %s...\n", key, host)
}

// TicketFetched confirms the fetch.
func (r *ConsoleReporter) TicketFetched(t domain.Ticket, elapsed time.Duration) {
	if r.Pretty {
		r.stopSpin()
		st := r.st()
		r.bullet(st.blue, "Jira "+t.Key, fmt.Sprintf("%s · fetched in %s", t.Type, formatDuration(elapsed)))
		return
	}
	fmt.Fprintf(r.Out, "✓ %s (%s) fetched in %s\n", t.Key, t.Type, formatDuration(elapsed))
}

// TicketMoved warns that the issue answered under another key (a moved issue), which is
// the one the run uses.
func (r *ConsoleReporter) TicketMoved(requested, key string) {
	if r.Pretty {
		r.stopSpin()
		fmt.Fprintln(r.Out, r.st().yellow(fmt.Sprintf("⚠ %s has moved to %s; using %s", requested, key, key)))
		return
	}
	fmt.Fprintf(r.Out, "Warning: %s has moved to %s; using %s\n", requested, key, key)
}

// TaskBlock prints the whole task text the run will use, every line prefixed "  | ".
func (r *ConsoleReporter) TaskBlock(key, text string) {
	if r.Pretty {
		st := r.st()
		fmt.Fprintf(r.Out, "  %s  %s\n", st.gray("⎿"), st.gray("task"))
		pw := NewPrefixWriter(r.Out, "     "+st.gray("│")+" ")
		io.WriteString(pw, text)
		if len(text) > 0 && text[len(text)-1] != '\n' {
			io.WriteString(pw, "\n")
		}
		fmt.Fprintln(r.Out)
		return
	}
	fmt.Fprintf(r.Out, "\nTask (from %s):\n", key)
	pw := NewPrefixWriter(r.Out, "  | ")
	io.WriteString(pw, text)
	if len(text) > 0 && text[len(text)-1] != '\n' {
		io.WriteString(pw, "\n")
	}
	fmt.Fprintln(r.Out)
}

// taskSourceLine is the resume header's reminder that a Jira task is the one persisted
// when the run started: resume never contacts Jira (SPEC.md §11.3).
func taskSourceLine(src *domain.TaskSource) string {
	return fmt.Sprintf("Task from %s, fetched %s (not re-fetched)", src.Key, src.FetchedAt.Local().Format("2006-01-02 15:04"))
}
