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
	// maxComments bounds how many comments of an issue are read: the discussion of a ticket
	// the listener works on is short, and a longer one is not an answer to put in a prompt.
	maxComments      = 100
	commentsPageSize = 50
	maxCommentBytes  = maxResponseBody

	myselfPath = "/rest/api/2/myself"
)

// commentTimeLayouts are the timestamp forms Jira writes: its own "+0000" offset, and RFC 3339.
var commentTimeLayouts = []string{"2006-01-02T15:04:05.000-0700", time.RFC3339Nano}

// Comments lists the comments of key, oldest first, at most 100: GET .../comment?orderBy=created,
// paged by startAt. It follows the rules of Fetch (no redirect, 1 MiB per page, retries,
// redaction) and returns a *apperr.JiraError.
func (c *Client) Comments(ctx context.Context, key string) ([]domain.TicketComment, error) {
	key = strings.ToUpper(key)
	var comments []domain.TicketComment
	for len(comments) < maxComments {
		target := c.commentsURL(key, len(comments))
		page, err := retrying(ctx, c, key, func() (commentsResult, time.Duration, *apperr.JiraError) {
			return c.commentsAttempt(ctx, key, target)
		})
		if err != nil {
			return nil, err
		}
		for _, cm := range page.comments {
			if len(comments) == maxComments {
				break
			}
			comments = append(comments, cm)
		}
		if len(page.comments) == 0 || (page.total >= 0 && len(comments) >= page.total) {
			break
		}
	}
	return comments, nil
}

type commentsResult struct {
	comments []domain.TicketComment
	// total is the number of comments the issue has, -1 when the answer gave none.
	total int
}

func (c *Client) commentsURL(key string, startAt int) string {
	q := url.Values{}
	q.Set("orderBy", "created")
	q.Set("startAt", strconv.Itoa(startAt))
	q.Set("maxResults", strconv.Itoa(commentsPageSize))
	return c.issueURL(key) + "/comment?" + q.Encode()
}

func (c *Client) commentsAttempt(ctx context.Context, key, target string) (commentsResult, time.Duration, *apperr.JiraError) {
	res, jerr := c.do(ctx, http.MethodGet, target, nil, key)
	if jerr != nil {
		return commentsResult{}, 0, jerr
	}
	if res.status != http.StatusOK {
		return commentsResult{}, res.retryAfter, c.statusError(key, res)
	}
	page, detail := toComments(res.raw, c.opts.Auth)
	if detail != "" {
		return commentsResult{}, 0, c.fail(key, apperr.JiraInvalidResponse, res.status, detail, "", nil)
	}
	return page, 0, nil
}

// toComments validates the answer of GET .../comment strictly. On a violation it returns the
// JSON path and the problem as detail.
func toComments(raw []byte, auth string) (commentsResult, string) {
	var p struct {
		Comments *[]json.RawMessage `json:"comments"`
		Total    *int               `json:"total"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return commentsResult{}, fmt.Sprintf("the body is not a list of comments: %v", err)
	}
	if p.Comments == nil {
		return commentsResult{}, "comments: missing"
	}
	if len(*p.Comments) > 1000 {
		return commentsResult{}, "comments: more than 1000 entries"
	}
	out := commentsResult{total: -1}
	if p.Total != nil {
		out.total = *p.Total
	}
	for i, rawComment := range *p.Comments {
		cm, detail := toComment(rawComment, auth)
		if detail != "" {
			return commentsResult{}, fmt.Sprintf("comments[%d].%s", i, detail)
		}
		out.comments = append(out.comments, cm)
	}
	return out, ""
}

func toComment(raw json.RawMessage, auth string) (domain.TicketComment, string) {
	var c struct {
		ID      json.RawMessage `json:"id"`
		Author  json.RawMessage `json:"author"`
		Created json.RawMessage `json:"created"`
		Body    json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return domain.TicketComment{}, fmt.Sprintf("not a comment: %v", err)
	}
	id, ok, err := optionalString(c.ID)
	if err != nil || !ok || !idPattern.MatchString(id) {
		return domain.TicketComment{}, "id: missing or not a number"
	}
	if isNull(c.Author) {
		return domain.TicketComment{}, "author: missing"
	}
	author, detail := toUser(c.Author, "author", auth)
	if detail != "" {
		return domain.TicketComment{}, detail
	}
	created, ok, err := optionalString(c.Created)
	if err != nil || !ok {
		return domain.TicketComment{}, "created: missing or not a string"
	}
	when, ok := parseCommentTime(created)
	if !ok {
		return domain.TicketComment{}, "created: not a timestamp"
	}
	body, ok, err := optionalString(c.Body)
	if err != nil || !ok {
		return domain.TicketComment{}, "body: missing or not a string"
	}
	if len(body) > maxCommentBytes {
		return domain.TicketComment{}, "body: too long"
	}
	return domain.TicketComment{ID: id, Author: author, Created: when, Body: normalize(body)}, ""
}

func parseCommentTime(s string) (time.Time, bool) {
	for _, layout := range commentTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// Myself answers who the credentials belong to: GET /rest/api/2/myself, the accountId on Jira
// Cloud (auth basic) and the username on Data Center (auth bearer). The listener tells its own
// comments by it. Failures are *apperr.JiraError, as for Fetch.
func (c *Client) Myself(ctx context.Context) (string, error) {
	return retrying(ctx, c, "", func() (string, time.Duration, *apperr.JiraError) {
		res, jerr := c.do(ctx, http.MethodGet, c.opts.BaseURL+myselfPath, nil, "")
		if jerr != nil {
			return "", 0, jerr
		}
		if res.status != http.StatusOK {
			return "", res.retryAfter, c.statusError("", res)
		}
		id, detail := toUser(res.raw, "myself", c.opts.Auth)
		if detail != "" {
			return "", 0, c.fail("", apperr.JiraInvalidResponse, res.status, detail, "", nil)
		}
		return id, 0, nil
	})
}
