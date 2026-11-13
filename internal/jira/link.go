package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

// linkPath is the one URL an issue link is posted to, below BaseURL.
const linkPath = "/rest/api/2/issueLink"

// maxChildren bounds how many children of one parent Children returns.
const maxChildren = 200

// customFieldPattern is the id of a custom field, the form the Epic Link field is configured in.
var customFieldPattern = regexp.MustCompile(`^customfield_([0-9]{1,10})$`)

// Link makes the one POST that links two issues (specs/listener-story/SPEC.md §8.3): linkType is
// the link type's name ("Blocks"), inward the issue on the inward side ("is blocked by": the
// one that waits) and outward the one on the outward side ("blocks"). Jira answers 201 without
// a body. The rules are Create's: the request is never sent twice in one call - only an answer
// that says nothing was written (a 429 or 503 with a Retry-After that fits one attempt's
// timeout) is retried - and MayExist tells the caller whether a failure may have written the
// link anyway. A failure is a *apperr.JiraError (Op "link") for the inward issue.
func (c *Client) Link(ctx context.Context, linkType, inward, outward string) error {
	notSent := func(detail string) error {
		jerr := c.fail(inward, apperr.JiraInvalidResponse, 0, detail, "", nil)
		jerr.Op, jerr.NotSent = apperr.JiraOpLink, true
		return jerr
	}
	switch {
	case !validName(linkType):
		return notSent("the link type's name is empty, too long or not one line")
	case !domain.ValidTicketKey(inward) || !domain.ValidTicketKey(outward):
		return notSent(fmt.Sprintf("%s and %s must both be issue keys", truncate(inward, 40), truncate(outward, 40)))
	case inward == outward:
		return notSent("an issue cannot be linked to itself")
	}
	body, err := json.Marshal(map[string]any{
		"type":         map[string]string{"name": linkType},
		"inwardIssue":  map[string]string{"key": inward},
		"outwardIssue": map[string]string{"key": outward},
	})
	if err != nil {
		return notSent("build request: " + err.Error())
	}
	_, err = exchange(ctx, c, apperr.JiraOpLink, inward, http.MethodPost, c.opts.BaseURL+linkPath, body, http.StatusCreated,
		func([]byte) (struct{}, string) { return struct{}{}, "" })
	return err
}

// Children returns the keys of the issues under parent that reporter reported, over the search
// (specs/listener-story/SPEC.md §9): with field empty the standard parent link (parent = KEY),
// else the custom field of jira.create.link: field (cf[<id>] = KEY). reporter is the accountId
// on Jira Cloud and the username on Data Center. Nothing but the keys is kept.
func (c *Client) Children(ctx context.Context, parent, field, reporter string) ([]string, error) {
	fail := func(detail string) error {
		jerr := c.fail(parent, apperr.JiraInvalidResponse, 0, detail, "", nil)
		jerr.NotSent = true
		return jerr
	}
	if !domain.ValidTicketKey(parent) {
		return nil, fail("the parent is not an issue key")
	}
	if reporter == "" || strings.ContainsFunc(reporter, func(r rune) bool { return r == '"' || r == '\\' || r < ' ' || r == 0x7f }) {
		return nil, fail("the reporter is empty or holds a quote, a backslash or a control character")
	}
	clause := "parent = " + parent
	if field != "" {
		m := customFieldPattern.FindStringSubmatch(field)
		if m == nil {
			return nil, fail(fmt.Sprintf("the link field %s is not a custom field id (customfield_<n>)", truncate(field, 40)))
		}
		clause = "cf[" + m[1] + "] = " + parent
	}
	heads, err := c.Search(ctx, clause+" AND reporter = "+jqlString(reporter), maxChildren)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(heads))
	for _, h := range heads {
		keys = append(keys, h.Key)
	}
	return keys, nil
}
