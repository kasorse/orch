package app

import (
	"testing"

	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
)

// Labels are rendered into the task, but adding or removing one (restart, ai-run) is not an edit.
func TestTicketHash_IgnoresLabels(t *testing.T) {
	r := &listenRunner{cfg: config.Config{Jira: &config.JiraConfig{MaxBytes: 32768}}}
	base := domain.Ticket{Key: "PAY-1", URL: "https://h/browse/PAY-1", Type: "Task", Summary: "S"}
	labelled := base
	labelled.Labels = []string{"ai-run", "restart"}
	if r.ticketHash(base) == "" || r.ticketHash(base) != r.ticketHash(labelled) {
		t.Fatal("a label changed the ticket hash")
	}
	edited := base
	edited.Summary = "S2"
	if r.ticketHash(base) == r.ticketHash(edited) {
		t.Fatal("an edit of the summary kept the ticket hash")
	}
}
