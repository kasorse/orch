package domain

import (
	"regexp"
	"time"
)

// ticketKeyPattern is the one definition of a Jira issue key: a project key of at least
// two characters, a dash, and a number without a leading zero.
var ticketKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]+-[1-9][0-9]*$`)

// ValidTicketKey reports whether s is an upper-case Jira issue key such as PMC-2789.
func ValidTicketKey(s string) bool { return ticketKeyPattern.MatchString(s) }

// Ticket is one validated Jira issue, as fetched for run --jira (specs/jira/SPEC.md §8.3).
// It is rendered into Task.Description and kept in ticket.json as an audit record.
type Ticket struct {
	// Key is the key the issue answered under; RequestedKey the one asked for, set only
	// when the two differ (a moved issue).
	Key          string `json:"key"`
	RequestedKey string `json:"requested_key,omitempty"`
	// URL is {jira.base_url}/browse/{Key}.
	URL     string `json:"url"`
	Type    string `json:"type"`
	Summary string `json:"summary"`
	// Description is the wiki markup, verbatim; "" when the issue has none.
	Description string        `json:"description"`
	Parent      *TicketParent `json:"parent,omitempty"`
	// Components are the names of the issue's components, in Jira's order and without
	// duplicates; story plan finds the services a ticket names among them
	// (specs/story/SPEC.md §7.1). They are not part of the rendered task text.
	Components []string `json:"components,omitempty"`
	// Fields are the configured jira.fields with a non-empty value, in config order.
	Fields    []TicketField `json:"fields,omitempty"`
	FetchedAt time.Time     `json:"fetched_at"`
	// Status, Labels and Reporter are what the Jira listener re-checks (specs/jira-listener/SPEC.md
	// §3.3). Only Labels is rendered into the task text, as the header's Labels line. Reporter is the accountId on Jira
	// Cloud and the username on Data Center; "" when the issue has none.
	Status   string   `json:"status,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	Reporter string   `json:"reporter,omitempty"`
}

// TicketHead is one issue of a search result: what the Jira listener needs to pick a ticket
// and to re-check it in Go (specs/jira-listener/SPEC.md §3).
type TicketHead struct {
	Key     string
	Summary string
	// Status and Type are the names of the issue's status and issue type.
	Status string
	Type   string
	Labels []string
	// Components are the names of the issue's components, in Jira's order.
	Components []string
	// Reporter is the accountId on Jira Cloud and the username on Data Center.
	Reporter string
}

// TicketComment is one comment of an issue.
type TicketComment struct {
	ID string
	// Author is the accountId on Jira Cloud and the username on Data Center, comparable with
	// what GET /myself answers.
	Author  string
	Created time.Time
	// Body is the wiki markup, verbatim.
	Body string
}

// TicketParent is a sub-task's parent or, on Jira Cloud, the epic.
type TicketParent struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
}

// TicketField is one configured text custom field and its value.
type TicketField struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value string `json:"value"`
}
