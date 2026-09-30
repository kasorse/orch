package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"orchestrator/internal/domain"
)

// issueResponse is the part of GET /rest/api/2/issue/{key} this package reads. Fields
// stay raw so each one's JSON type can be checked on its own; unknown keys (self, id,
// expand, other fields) are ignored.
type issueResponse struct {
	Key    *string                    `json:"key"`
	Fields map[string]json.RawMessage `json:"fields"`
}

type issueType struct {
	Name *string `json:"name"`
}

type parentIssue struct {
	Key    *string `json:"key"`
	Fields *struct {
		Summary *string `json:"summary"`
	} `json:"fields"`
}

// toTicket validates a 200 answer strictly (SPEC.md §8.2) and builds the ticket. On a
// violation it returns the JSON path and the problem as detail, and the ticket is unset.
// requestedKey is the upper-cased key Fetch asked for.
func toTicket(raw []byte, requestedKey string, opts Options, now time.Time) (domain.Ticket, string) {
	var resp issueResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return domain.Ticket{}, fmt.Sprintf("the body is not an issue: %v", err)
	}

	if resp.Key == nil {
		return domain.Ticket{}, "key: missing"
	}
	key := *resp.Key
	if !domain.ValidTicketKey(key) {
		return domain.Ticket{}, fmt.Sprintf("key %q is not an issue key", key)
	}
	if resp.Fields == nil {
		return domain.Ticket{}, "fields: missing"
	}

	summary, ok, err := optionalString(resp.Fields["summary"])
	switch {
	case err != nil:
		return domain.Ticket{}, "fields.summary: not a string"
	case !ok || strings.TrimSpace(summary) == "":
		return domain.Ticket{}, "fields.summary: missing or empty"
	}

	// A missing description is the same as null: the issue has none.
	description, _, err := optionalString(resp.Fields["description"])
	if err != nil {
		return domain.Ticket{}, "fields.description: not a string or null"
	}

	var it issueType
	if isNull(resp.Fields["issuetype"]) {
		return domain.Ticket{}, "fields.issuetype: missing"
	}
	if err := json.Unmarshal(resp.Fields["issuetype"], &it); err != nil {
		return domain.Ticket{}, "fields.issuetype: not an object"
	}
	if it.Name == nil || strings.TrimSpace(*it.Name) == "" {
		return domain.Ticket{}, "fields.issuetype.name: missing or empty"
	}

	var parent *domain.TicketParent
	if !isNull(resp.Fields["parent"]) {
		var p parentIssue
		if err := json.Unmarshal(resp.Fields["parent"], &p); err != nil {
			return domain.Ticket{}, "fields.parent: not an object"
		}
		if p.Key == nil || !domain.ValidTicketKey(*p.Key) {
			return domain.Ticket{}, "fields.parent.key: missing or not an issue key"
		}
		if p.Fields == nil || p.Fields.Summary == nil {
			return domain.Ticket{}, "fields.parent.fields.summary: missing or not a string"
		}
		parent = &domain.TicketParent{Key: *p.Key, Summary: normalize(*p.Fields.Summary)}
	}

	var fields []domain.TicketField
	for _, f := range opts.Fields {
		value, _, err := optionalString(resp.Fields[f.ID])
		if err != nil {
			return domain.Ticket{}, fmt.Sprintf("fields.%s: not a text field; remove it from jira.fields", f.ID)
		}
		if strings.TrimSpace(value) == "" {
			continue
		}
		fields = append(fields, domain.TicketField{ID: f.ID, Label: f.Label, Value: normalize(value)})
	}

	t := domain.Ticket{
		Key:         key,
		URL:         opts.BaseURL + "/browse/" + key,
		Type:        normalize(*it.Name),
		Summary:     normalize(summary),
		Description: normalize(description),
		Parent:      parent,
		Fields:      fields,
		FetchedAt:   now,
	}
	// A moved issue answers under its new key (SPEC.md F6): keep both.
	if key != requestedKey {
		t.RequestedKey = requestedKey
	}
	return t, ""
}

// optionalString decodes a JSON string, reporting ok=false for an absent field or null
// and an error for any other JSON type.
func optionalString(raw json.RawMessage) (s string, ok bool, err error) {
	if isNull(raw) {
		return "", false, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false, err
	}
	return s, true, nil
}

// isNull reports an absent field (nil) or a JSON null.
func isNull(raw json.RawMessage) bool {
	return raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// normalize turns \r\n into \n and changes nothing else (SPEC.md §9).
func normalize(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}
