package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/kasorse/orch/internal/apperr"
)

// The calls of orchestrator revise (specs/revise/SPEC.md §4, §8): an issue's web links, where the
// GitLab integration puts its merge requests, and the read and write of its description. They
// follow the write-back's rules (exchange): only BaseURL, no redirect, strict answers, only a 429
// or a 503 with a usable Retry-After retried, never a write repeated once it may have reached Jira.

const (
	// maxRemoteLinks bounds how many web links one answer may list.
	maxRemoteLinks = 500
	// MaxDescription is Jira's own limit on a text field, in characters.
	MaxDescription = 32767
	// maxUpdate bounds the requirement appended to a description, in bytes.
	maxUpdate = 8 << 10
)

// RemoteLink is one web link of an issue. URL is whatever the link points at: the caller decides
// what it accepts, and nothing here follows it.
type RemoteLink struct {
	URL          string
	Relationship string
	Title        string
}

// RemoteLinks lists key's web links: GET .../remotelink.
func (c *Client) RemoteLinks(ctx context.Context, key string) ([]RemoteLink, error) {
	return exchange(ctx, c, apperr.JiraOpLinks, key, http.MethodGet, c.issueURL(key)+"/remotelink", nil, http.StatusOK, parseRemoteLinks)
}

// Description reads key's description, "" for none: GET with fields=description.
func (c *Client) Description(ctx context.Context, key string) (string, error) {
	return exchange(ctx, c, apperr.JiraOpDescription, key, http.MethodGet, c.issueURL(key)+"?fields=description", nil, http.StatusOK, parseDescription)
}

// SetDescription replaces key's description with wiki: PUT on the issue, answered 204. A failure
// that may have reached Jira is returned at once and never repeated.
func (c *Client) SetDescription(ctx context.Context, key, wiki string) error {
	if n := utf8.RuneCountInString(wiki); n > MaxDescription {
		jerr := c.fail(key, apperr.JiraInvalidResponse, 0, "", "", fmt.Errorf("the description would be %d characters, Jira allows %d", n, MaxDescription))
		jerr.Op, jerr.NotSent = apperr.JiraOpDescription, true
		return jerr
	}
	body, err := json.Marshal(map[string]map[string]string{"fields": {"description": wiki}})
	if err != nil {
		jerr := c.fail(key, apperr.JiraInvalidResponse, 0, "", "", fmt.Errorf("build request: %w", err))
		jerr.Op, jerr.NotSent = apperr.JiraOpDescription, true
		return jerr
	}
	_, err = exchange(ctx, c, apperr.JiraOpDescription, key, http.MethodPut, c.issueURL(key), body, http.StatusNoContent,
		func([]byte) (struct{}, string) { return struct{}{}, "" })
	return err
}

// AppendUpdate is old followed by an empty line and "Update <date>: <text>" (SPEC.md §8). text is
// the operator's own requirement, written into their ticket as they typed it: line endings are
// normalized and it is cut at maxUpdate bytes on a rune with "…", nothing is escaped.
func AppendUpdate(old, date, text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	if len(text) > maxUpdate {
		cut := maxUpdate
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	update := "Update " + date + ": " + text
	if old = strings.TrimRight(old, " \t\r\n"); old == "" {
		return update
	}
	return old + "\n\n" + update
}

func parseRemoteLinks(raw []byte) ([]RemoteLink, string) {
	var out []struct {
		Relationship string `json:"relationship"`
		Object       *struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"object"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return nil, "the answer is not a JSON list of links"
	}
	if len(out) > maxRemoteLinks {
		return nil, fmt.Sprintf("the answer lists more than %d links", maxRemoteLinks)
	}
	links := make([]RemoteLink, 0, len(out))
	for i, l := range out {
		if l.Object == nil || l.Object.URL == "" || len(l.Object.URL) > 2048 {
			return nil, fmt.Sprintf("links[%d].object.url is missing or too long", i)
		}
		links = append(links, RemoteLink{URL: l.Object.URL, Relationship: l.Relationship, Title: l.Object.Title})
	}
	return links, ""
}

func parseDescription(raw []byte) (string, string) {
	var out struct {
		Fields *struct {
			Description *json.RawMessage `json:"description"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Fields == nil {
		return "", "the answer is not an issue with fields"
	}
	if out.Fields.Description == nil || string(*out.Fields.Description) == "null" {
		return "", ""
	}
	var s string
	if err := json.Unmarshal(*out.Fields.Description, &s); err != nil {
		return "", "fields.description is not a string (an ADF document? the client speaks REST v2)"
	}
	return s, ""
}
