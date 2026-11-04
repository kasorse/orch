package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

const (
	// Jira Cloud pages by an opaque token, Data Center by an offset
	// (specs/jira-listener/SPEC.md §3.1).
	cloudSearchPath = "/rest/api/2/search/jql"
	dcSearchPath    = "/rest/api/2/search"

	searchPageSize = 50
	searchFields   = "summary,status,labels,components,reporter,issuetype"
)

// searchPage is one answer of the search endpoints. Issues stay raw so each issue and each of
// its fields is checked on its own; unknown keys are ignored.
type searchPage struct {
	Issues        *[]json.RawMessage `json:"issues"`
	NextPageToken *string            `json:"nextPageToken"`
	IsLast        *bool              `json:"isLast"`
	Total         *int               `json:"total"`
}

// Search runs jql and returns at most max issues, in the order Jira sorted them. Cloud
// (auth basic) is paged by nextPageToken, Data Center (auth bearer) by startAt. Each page is
// requested with the rules of Fetch: no redirect, 1 MiB, retries of transient failures, and the
// credentials redacted from every error. A *apperr.JiraError carries no issue key.
func (c *Client) Search(ctx context.Context, jql string, max int) ([]domain.TicketHead, error) {
	var heads []domain.TicketHead
	cloud := c.opts.Auth != AuthBearer
	token, startAt := "", 0
	for len(heads) < max {
		target := c.searchURL(jql, cloud, token, startAt)
		page, err := retrying(ctx, c, "", func() (searchResult, time.Duration, *apperr.JiraError) {
			return c.searchAttempt(ctx, target, cloud)
		})
		if err != nil {
			return nil, err
		}
		for _, h := range page.heads {
			if len(heads) == max {
				break
			}
			heads = append(heads, h)
		}
		if len(page.heads) == 0 || page.last {
			break
		}
		if cloud {
			token = page.next
		} else {
			startAt += len(page.heads)
			if page.total >= 0 && startAt >= page.total {
				break
			}
		}
	}
	return heads, nil
}

// searchResult is one validated page: the issues, whether it is known to be the last, the
// Cloud token of the next one and the Data Center total (-1 when the answer gave none).
type searchResult struct {
	heads []domain.TicketHead
	last  bool
	next  string
	total int
}

func (c *Client) searchURL(jql string, cloud bool, token string, startAt int) string {
	q := url.Values{}
	q.Set("jql", jql)
	q.Set("fields", searchFields)
	q.Set("maxResults", strconv.Itoa(searchPageSize))
	path := dcSearchPath
	if cloud {
		path = cloudSearchPath
		if token != "" {
			q.Set("nextPageToken", token)
		}
	} else {
		q.Set("startAt", strconv.Itoa(startAt))
	}
	return c.opts.BaseURL + path + "?" + q.Encode()
}

func (c *Client) searchAttempt(ctx context.Context, target string, cloud bool) (searchResult, time.Duration, *apperr.JiraError) {
	res, jerr := c.do(ctx, http.MethodGet, target, nil, "")
	if jerr != nil {
		return searchResult{}, 0, jerr
	}
	if res.status != http.StatusOK {
		return searchResult{}, res.retryAfter, c.statusError("", res)
	}
	page, detail := toSearchResult(res.raw, c.opts.Auth, cloud)
	if detail != "" {
		return searchResult{}, 0, c.fail("", apperr.JiraInvalidResponse, res.status, detail, "", nil)
	}
	return page, 0, nil
}

// toSearchResult validates a 200 answer strictly (specs/jira-listener/SPEC.md §3.1). On a
// violation it returns the JSON path and the problem as detail.
func toSearchResult(raw []byte, auth string, cloud bool) (searchResult, string) {
	var p searchPage
	if err := json.Unmarshal(raw, &p); err != nil {
		return searchResult{}, fmt.Sprintf("the body is not a search result: %v", err)
	}
	if p.Issues == nil {
		return searchResult{}, "issues: missing"
	}
	if len(*p.Issues) > 1000 {
		return searchResult{}, "issues: more than 1000 entries"
	}
	out := searchResult{total: -1}
	for i, rawIssue := range *p.Issues {
		head, detail := toTicketHead(rawIssue, auth)
		if detail != "" {
			return searchResult{}, fmt.Sprintf("issues[%d].%s", i, detail)
		}
		out.heads = append(out.heads, head)
	}
	if cloud {
		if p.NextPageToken != nil {
			out.next = *p.NextPageToken
		}
		out.last = out.next == "" || (p.IsLast != nil && *p.IsLast)
	} else if p.Total != nil {
		out.total = *p.Total
	}
	return out, ""
}

// toTicketHead validates one issue of a search answer. The details start at "key" or "fields".
func toTicketHead(raw json.RawMessage, auth string) (domain.TicketHead, string) {
	var issue issueResponse
	if err := json.Unmarshal(raw, &issue); err != nil {
		return domain.TicketHead{}, fmt.Sprintf("not an issue: %v", err)
	}
	if issue.Key == nil {
		return domain.TicketHead{}, "key: missing"
	}
	if !domain.ValidTicketKey(*issue.Key) {
		return domain.TicketHead{}, fmt.Sprintf("key %q is not an issue key", *issue.Key)
	}
	if issue.Fields == nil {
		return domain.TicketHead{}, "fields: missing"
	}

	summary, ok, err := optionalString(issue.Fields["summary"])
	switch {
	case err != nil:
		return domain.TicketHead{}, "fields.summary: not a string"
	case !ok || strings.TrimSpace(summary) == "":
		return domain.TicketHead{}, "fields.summary: missing or empty"
	}

	if isNull(issue.Fields["issuetype"]) {
		return domain.TicketHead{}, "fields.issuetype: missing"
	}
	var it issueType
	if err := json.Unmarshal(issue.Fields["issuetype"], &it); err != nil {
		return domain.TicketHead{}, "fields.issuetype: not an object"
	}
	if it.Name == nil || strings.TrimSpace(*it.Name) == "" {
		return domain.TicketHead{}, "fields.issuetype.name: missing or empty"
	}

	status, detail := toStatus(issue.Fields["status"], "fields", true)
	if detail != "" {
		return domain.TicketHead{}, detail
	}
	labels, detail := toLabels(issue.Fields["labels"], "fields")
	if detail != "" {
		return domain.TicketHead{}, detail
	}
	components, detail := toComponents(issue.Fields["components"])
	if detail != "" {
		return domain.TicketHead{}, detail
	}
	reporter, detail := toReporter(issue.Fields["reporter"], "fields", auth)
	if detail != "" {
		return domain.TicketHead{}, detail
	}
	return domain.TicketHead{
		Key:        *issue.Key,
		Summary:    normalize(summary),
		Status:     status,
		Type:       normalize(*it.Name),
		Labels:     labels,
		Components: components,
		Reporter:   reporter,
	}, ""
}
