package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// createPath is the one URL a creation is posted to, below BaseURL.
const createPath = "/rest/api/2/issue"

// Issue is one child issue to create (specs/story/SPEC.md §12.2).
type Issue struct {
	// Project is the project key the issue is created in; the answer's key must belong to it.
	Project   string
	IssueType string
	Summary   string
	// Description is wiki markup (see Wiki).
	Description string
	// Parent is the key of the issue the new one hangs under. With LinkField empty it goes in
	// the standard parent field; otherwise in the custom field LinkField names (Epic Link).
	Parent    string
	LinkField string
	Labels    []string
	// Components are component names; none when empty.
	Components []string
}

// body is the JSON request of a creation. The fields are a map, as the custom link field's id
// is a key; everything else is a fixed shape.
func (i Issue) body() ([]byte, error) {
	fields := map[string]any{
		"project":     map[string]string{"key": i.Project},
		"issuetype":   map[string]string{"name": i.IssueType},
		"summary":     i.Summary,
		"description": i.Description,
	}
	if i.LinkField == "" {
		fields["parent"] = map[string]string{"key": i.Parent}
	} else {
		fields[i.LinkField] = i.Parent
	}
	if len(i.Labels) > 0 {
		fields["labels"] = i.Labels
	}
	if len(i.Components) > 0 {
		components := make([]map[string]string, len(i.Components))
		for n, name := range i.Components {
			components[n] = map[string]string{"name": name}
		}
		fields["components"] = components
	}
	return json.Marshal(map[string]any{"fields": fields})
}

// Create makes the one POST that creates issue and returns the key Jira gave it, or a
// *apperr.JiraError (Op "create"). Only an answer that says nothing was created is retried: a
// 429 or 503 with a Retry-After that fits one attempt's timeout, up to MaxRetries times. A
// transport error, a timeout or any other answer is returned at once - the request may have
// created the issue, and another POST could create a second one; MayExist tells the caller
// which failures leave that possibility.
func (c *Client) Create(ctx context.Context, issue Issue) (string, error) {
	body, err := issue.body()
	if err != nil {
		return "", c.createFailure(issue, c.fail(issue.Parent, apperr.JiraInvalidResponse, 0, "", "", fmt.Errorf("build request: %w", err)), 1, true)
	}
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			cerr := c.contextError(ctx, issue.Parent, attempt-1)
			return "", c.createFailure(issue, cerr, attempt-1, true)
		}
		key, retryAfter, jerr := c.createOnce(ctx, issue, body)
		if jerr == nil {
			return key, nil
		}
		retry := (jerr.StatusCode == http.StatusTooManyRequests || jerr.StatusCode == http.StatusServiceUnavailable) &&
			retryAfter > 0 && retryAfter <= c.opts.Timeout && attempt <= c.opts.MaxRetries
		if !retry {
			return "", c.createFailure(issue, jerr, attempt, false)
		}
		if err := c.sleep(ctx, retryAfter); err != nil {
			// The attempts so far were all answered "not now": nothing was created.
			return "", c.createFailure(issue, c.contextError(ctx, issue.Parent, attempt), attempt, true)
		}
	}
}

func (c *Client) createFailure(issue Issue, jerr *apperr.JiraError, attempts int, notSent bool) *apperr.JiraError {
	jerr.Op = apperr.JiraOpCreate
	if attempts > 0 {
		jerr.Attempts = attempts
	}
	if notSent {
		jerr.NotSent = true
	}
	return jerr
}

// createOnce is one POST.
func (c *Client) createOnce(ctx context.Context, issue Issue, body []byte) (string, time.Duration, *apperr.JiraError) {
	res, jerr := c.do(ctx, http.MethodPost, c.opts.BaseURL+createPath, body, issue.Parent)
	if jerr != nil {
		return "", 0, jerr
	}
	switch res.status {
	case http.StatusCreated:
		key, detail := createdKey(res.raw, issue.Project)
		if detail != "" {
			return "", 0, c.fail(issue.Parent, apperr.JiraInvalidResponse, res.status, detail, "", nil)
		}
		return key, 0, nil
	case http.StatusBadRequest:
		return "", 0, c.rejected(issue.Parent, res)
	}
	return "", res.retryAfter, c.statusError(issue.Parent, res)
}

// createdKey reads the key out of a 201 answer. Jira adds fields (id, self); an unknown one
// is fine, a key that is not <PROJECT>-<n> for the project sent is not.
func createdKey(raw []byte, project string) (string, string) {
	var out struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "the answer is not a JSON object"
	}
	switch {
	case out.Key == "":
		return "", "the answer has no key"
	case !domain.ValidTicketKey(out.Key) || !strings.HasPrefix(out.Key, project+"-"):
		return "", fmt.Sprintf("the answer's key %s is not an issue key of project %s", truncate(out.Key, 40), project)
	}
	return out.Key, ""
}

// rejected is a 400: Jira's own words on what is wrong, errorMessages first and then each field
// in the errors object as "field: message" in field order. They are redacted before being cut.
func (c *Client) rejected(parent string, res response) *apperr.JiraError {
	var out struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	var parts []string
	if err := json.Unmarshal(res.raw, &out); err == nil {
		parts = append(parts, out.ErrorMessages...)
		fields := make([]string, 0, len(out.Errors))
		for f := range out.Errors {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		for _, f := range fields {
			parts = append(parts, f+": "+out.Errors[f])
		}
	}
	if len(parts) == 0 {
		return c.fail(parent, apperr.JiraRejected, res.status, "", string(res.raw), nil)
	}
	detail := truncate(c.redact(strings.Join(parts, "; ")), maxErrorBody)
	return c.fail(parent, apperr.JiraRejected, res.status, detail, "", nil)
}

// MayExist reports whether a failed Create may have created the issue anyway: the request
// reached Jira, or may have, and no answer says it was refused. A caller that retries after
// true can create it twice. It is false only for a request that was never sent and for the
// answers that refuse one (a redirect, 400, 401, 403, 404, 429, 503).
func MayExist(err error) bool {
	var jerr *apperr.JiraError
	if !errors.As(err, &jerr) {
		return true
	}
	if jerr.NotSent {
		return false
	}
	switch jerr.StatusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return false
	}
	return jerr.StatusCode < 300 || jerr.StatusCode >= 400
}
