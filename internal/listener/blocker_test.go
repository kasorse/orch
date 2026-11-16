package listener

import (
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

func TestWaiting(t *testing.T) {
	done := []string{"Done", "Closed"}
	ref := func(key, status, linkType string) domain.TicketRef {
		return domain.TicketRef{Key: key, Status: status, LinkType: linkType}
	}
	tests := []struct {
		name      string
		blockedBy []domain.TicketRef
		linkType  string
		done      []string
		want      domain.TicketRef
		waits     bool
	}{
		{"no links", nil, "Blocks", done, domain.TicketRef{}, false},
		{"finished blocker", []domain.TicketRef{ref("PAY-1", "Done", "Blocks")}, "Blocks", done, domain.TicketRef{}, false},
		{"finished, case folded", []domain.TicketRef{ref("PAY-1", "closed", "blocks")}, "Blocks", done, domain.TicketRef{}, false},
		{"unfinished blocker", []domain.TicketRef{ref("PAY-1", "Code Review", "Blocks")}, "Blocks", done, ref("PAY-1", "Code Review", "Blocks"), true},
		{"type case folded", []domain.TicketRef{ref("PAY-1", "In Progress", "BLOCKS")}, "blocks", done, ref("PAY-1", "In Progress", "BLOCKS"), true},
		{"no status is unfinished", []domain.TicketRef{ref("PAY-1", "", "Blocks")}, "Blocks", done, ref("PAY-1", "", "Blocks"), true},
		{"another link type", []domain.TicketRef{ref("PAY-1", "To Do", "Relates")}, "Blocks", done, domain.TicketRef{}, false},
		{"the first unfinished one", []domain.TicketRef{
			ref("PAY-1", "Done", "Blocks"), ref("PAY-2", "To Do", "Relates"), ref("PAY-3", "To Do", "Blocks"), ref("PAY-4", "", "Blocks"),
		}, "Blocks", done, ref("PAY-3", "To Do", "Blocks"), true},
		{"no done statuses", []domain.TicketRef{ref("PAY-1", "Done", "Blocks")}, "Blocks", nil, ref("PAY-1", "Done", "Blocks"), true},
		{"no link type", []domain.TicketRef{ref("PAY-1", "To Do", "Blocks")}, "", done, domain.TicketRef{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, waits := Waiting(domain.TicketHead{Key: "PAY-9", BlockedBy: tt.blockedBy}, tt.linkType, tt.done)
			if got != tt.want || waits != tt.waits {
				t.Errorf("Waiting = %+v, %v; want %+v, %v", got, waits, tt.want, tt.waits)
			}
		})
	}
}
