package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
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

	components, detail := toComponents(resp.Fields["components"])
	if detail != "" {
		return domain.Ticket{}, detail
	}

	// status, labels and reporter are for the listener's re-check; an answer without them
	// (an older fixture, a proxy) still gives a ticket.
	status, detail := toStatus(resp.Fields["status"], "fields", false)
	if detail != "" {
		return domain.Ticket{}, detail
	}
	labels, detail := toLabels(resp.Fields["labels"], "fields")
	if detail != "" {
		return domain.Ticket{}, detail
	}
	reporter, detail := toReporter(resp.Fields["reporter"], "fields", opts.Auth)
	if detail != "" {
		return domain.Ticket{}, detail
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
		Components:  components,
		Fields:      fields,
		FetchedAt:   now,
		Status:      status,
		Labels:      labels,
		Reporter:    reporter,
	}
	// A moved issue answers under its new key (SPEC.md F6): keep both.
	if key != requestedKey {
		t.RequestedKey = requestedKey
	}
	return t, ""
}

const (
	// maxComponents and maxComponentBytes bound fields.components: a story names its
	// services from them (specs/story/SPEC.md §7.1), and Jira's own limit on a name is 255.
	maxComponents     = 50
	maxComponentBytes = 255
)

// toComponents reads fields.components into the components' names, in Jira's order and
// without exact duplicates. Absent, null and [] give none; only "name" is read from an
// entry, and every other key is ignored. On a violation it returns the JSON path and the
// problem as detail.
func toComponents(raw json.RawMessage) ([]string, string) {
	if isNull(raw) {
		return nil, ""
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, "fields.components: not an array"
	}
	if len(entries) > maxComponents {
		return nil, fmt.Sprintf("fields.components: more than %d entries", maxComponents)
	}
	var names []string
	for i, e := range entries {
		var c map[string]json.RawMessage
		if err := json.Unmarshal(e, &c); err != nil || c == nil {
			return nil, fmt.Sprintf("fields.components[%d]: not an object", i)
		}
		name, ok, err := optionalString(c["name"])
		switch {
		case err != nil || !ok || strings.TrimSpace(name) == "":
			return nil, fmt.Sprintf("fields.components[%d].name: missing or empty", i)
		case strings.ContainsAny(name, "\r\n"):
			return nil, fmt.Sprintf("fields.components[%d].name: not one line", i)
		case len(name) > maxComponentBytes:
			return nil, fmt.Sprintf("fields.components[%d].name: longer than %d bytes", i, maxComponentBytes)
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names, ""
}

const (
	// maxLabels and maxLabelBytes bound fields.labels; Jira's own limit on a label is 255.
	maxLabels     = 100
	maxLabelBytes = 255
)

// toStatus reads fields.status.name. path is the JSON path of the fields object, for the
// detail. A missing status is an error only when required.
func toStatus(raw json.RawMessage, path string, required bool) (string, string) {
	if isNull(raw) {
		if required {
			return "", path + ".status: missing"
		}
		return "", ""
	}
	var s struct {
		Name *string `json:"name"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", path + ".status: not an object"
	}
	if s.Name == nil || !validName(*s.Name) {
		return "", path + ".status.name: missing, too long or not one line"
	}
	return *s.Name, ""
}

// toLabels reads fields.labels: absent, null and [] give none; every entry must be a string.
func toLabels(raw json.RawMessage, path string) ([]string, string) {
	if isNull(raw) {
		return nil, ""
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, path + ".labels: not an array"
	}
	if len(entries) > maxLabels {
		return nil, fmt.Sprintf("%s.labels: more than %d entries", path, maxLabels)
	}
	var labels []string
	for i, e := range entries {
		var label string
		if err := json.Unmarshal(e, &label); err != nil || isNull(e) {
			return nil, fmt.Sprintf("%s.labels[%d]: not a string", path, i)
		}
		if len(label) > maxLabelBytes {
			return nil, fmt.Sprintf("%s.labels[%d]: longer than %d bytes", path, i, maxLabelBytes)
		}
		labels = append(labels, label)
	}
	return labels, ""
}

// reporterKey is the key that identifies a user: accountId on Jira Cloud (auth basic), name on
// Data Center (auth bearer).
func reporterKey(auth string) string {
	if auth == AuthBearer {
		return "name"
	}
	return "accountId"
}

// toReporter reads the identity out of fields.reporter. An issue without a reporter gives "".
func toReporter(raw json.RawMessage, path, auth string) (string, string) {
	if isNull(raw) {
		return "", ""
	}
	return toUser(raw, path+".reporter", auth)
}

// toUser reads the identity of the user object in raw: accountId on Cloud, name on Data Center.
func toUser(raw json.RawMessage, path, auth string) (string, string) {
	var u map[string]json.RawMessage
	if err := json.Unmarshal(raw, &u); err != nil || u == nil {
		return "", path + ": not an object"
	}
	key := reporterKey(auth)
	id, ok, err := optionalString(u[key])
	switch {
	case err != nil || !ok || strings.TrimSpace(id) == "":
		return "", fmt.Sprintf("%s.%s: missing or empty", path, key)
	case !validName(id):
		return "", fmt.Sprintf("%s.%s: too long or not one line", path, key)
	}
	return id, ""
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
