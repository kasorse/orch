package listener

import (
	"slices"
	"strings"

	"github.com/kasorse/orch/internal/domain"
)

// Waiting reports whether the ticket head waits for a blocker (specs/listener-story/SPEC.md §4):
// the first of its links of the type linkType (case-insensitive) whose blocker's status is not one
// of doneStatuses (case-insensitive). A blocker without a status is unfinished. Links of other
// types are ignored; an empty linkType names none.
func Waiting(head domain.TicketHead, linkType string, doneStatuses []string) (blocker domain.TicketRef, waits bool) {
	linkType = strings.TrimSpace(linkType)
	if linkType == "" {
		return domain.TicketRef{}, false
	}
	for _, b := range head.BlockedBy {
		if !strings.EqualFold(strings.TrimSpace(b.LinkType), linkType) {
			continue
		}
		status := strings.TrimSpace(b.Status)
		if status != "" && slices.ContainsFunc(doneStatuses, func(d string) bool { return strings.EqualFold(strings.TrimSpace(d), status) }) {
			continue
		}
		return b, true
	}
	return domain.TicketRef{}, false
}
