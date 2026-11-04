package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kasorse/orch/internal/apperr"
)

// The write-back to the issue of a run whose push opened a merge request (specs/delivery/SPEC.md
// §10). It follows the rules of Create: the request goes only to BaseURL, no redirect is followed,
// every answer is validated strictly, only a 429 or a 503 with a Retry-After that fits one
// attempt's timeout is retried, and MayExist tells the caller whether a failed write may have
// reached Jira.

const (
	// maxTransitions bounds how many transitions one answer may list.
	maxTransitions = 200
	// maxNameLen bounds a status or transition name in an answer.
	maxNameLen = 255
)

// idPattern is the id Jira gives a comment or a transition: a number, written as a string.
var idPattern = regexp.MustCompile(`^[0-9]{1,20}$`)

// Transition is one move the issue can make from its current status.
type Transition struct {
	ID   string
	Name string
	// To is the name of the status the transition leads to; empty when the answer gave none.
	To string
}

// Comment posts wiki, already wiki markup (see MergeRequestComment), as a comment on key and
// returns the id Jira gave it, or a *apperr.JiraError (Op "comment"). A 500, a timeout or any
// answer but a refusal leaves the comment possibly posted: it is returned at once and never
// repeated (MayExist).
func (c *Client) Comment(ctx context.Context, key, wiki string) (string, error) {
	body, err := json.Marshal(map[string]string{"body": wiki})
	if err != nil {
		jerr := c.fail(key, apperr.JiraInvalidResponse, 0, "", "", fmt.Errorf("build request: %w", err))
		jerr.Op, jerr.NotSent = apperr.JiraOpComment, true
		return "", jerr
	}
	return exchange(ctx, c, apperr.JiraOpComment, key, http.MethodPost, c.issueURL(key)+"/comment", body, http.StatusCreated, commentID)
}

// Transitions lists the moves key can make now: GET .../transitions.
func (c *Client) Transitions(ctx context.Context, key string) ([]Transition, error) {
	return exchange(ctx, c, apperr.JiraOpTransitions, key, http.MethodGet, c.issueURL(key)+"/transitions", nil, http.StatusOK, parseTransitions)
}

// Transition moves key by the transition with id (one of those Transitions listed): POST
// .../transitions, answered 204. It is never repeated once it may have reached Jira.
func (c *Client) Transition(ctx context.Context, key, id string) error {
	if !idPattern.MatchString(id) {
		jerr := c.fail(key, apperr.JiraInvalidResponse, 0, "", "", fmt.Errorf("transition id %q is not a number", truncate(id, 40)))
		jerr.Op, jerr.NotSent = apperr.JiraOpTransition, true
		return jerr
	}
	body, err := json.Marshal(map[string]map[string]string{"transition": {"id": id}})
	if err != nil {
		jerr := c.fail(key, apperr.JiraInvalidResponse, 0, "", "", fmt.Errorf("build request: %w", err))
		jerr.Op, jerr.NotSent = apperr.JiraOpTransition, true
		return jerr
	}
	_, err = exchange(ctx, c, apperr.JiraOpTransition, key, http.MethodPost, c.issueURL(key)+"/transitions", body, http.StatusNoContent,
		func([]byte) (struct{}, string) { return struct{}{}, "" })
	return err
}

// Status is the name of the status key is in now: GET with fields=status. It reads nothing else
// of the issue.
func (c *Client) Status(ctx context.Context, key string) (string, error) {
	return exchange(ctx, c, apperr.JiraOpStatus, key, http.MethodGet, c.issueURL(key)+"?fields=status", nil, http.StatusOK, parseStatus)
}

// issueURL is built only from BaseURL and the key, never from anything the operator pasted.
func (c *Client) issueURL(key string) string {
	return c.opts.BaseURL + issuePath + url.PathEscape(key)
}

// exchange is one request with the retry rule of Create: parse reads the body of the answer with
// status success and returns what is in it, or the detail of what is wrong with it. Any other
// answer is an error, and only a 429 or a 503 with a usable Retry-After is retried.
func exchange[T any](ctx context.Context, c *Client, op, key, method, target string, body []byte, success int, parse func([]byte) (T, string)) (T, error) {
	var zero T
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return zero, c.opFailure(op, c.contextError(ctx, key, attempt-1), attempt-1, true)
		}
		value, retryAfter, jerr := exchangeOnce(ctx, c, op, key, method, target, body, success, parse)
		if jerr == nil {
			return value, nil
		}
		retry := (jerr.StatusCode == http.StatusTooManyRequests || jerr.StatusCode == http.StatusServiceUnavailable) &&
			retryAfter > 0 && retryAfter <= c.opts.Timeout && attempt <= c.opts.MaxRetries
		if !retry {
			return zero, c.opFailure(op, jerr, attempt, false)
		}
		if err := c.sleep(ctx, retryAfter); err != nil {
			// The attempts so far were all answered "not now": nothing was written.
			return zero, c.opFailure(op, c.contextError(ctx, key, attempt), attempt, true)
		}
	}
}

func (c *Client) opFailure(op string, jerr *apperr.JiraError, attempts int, notSent bool) *apperr.JiraError {
	jerr.Op = op
	if attempts > 0 {
		jerr.Attempts = attempts
	}
	if notSent {
		jerr.NotSent = true
	}
	return jerr
}

func exchangeOnce[T any](ctx context.Context, c *Client, op, key, method, target string, body []byte, success int, parse func([]byte) (T, string)) (T, time.Duration, *apperr.JiraError) {
	var zero T
	res, jerr := c.do(ctx, method, target, body, key)
	if jerr != nil {
		return zero, 0, jerr
	}
	switch res.status {
	case success:
		value, detail := parse(res.raw)
		if detail != "" {
			return zero, 0, c.fail(key, apperr.JiraInvalidResponse, res.status, detail, "", nil)
		}
		return value, 0, nil
	case http.StatusBadRequest:
		return zero, 0, c.rejected(key, res)
	}
	return zero, res.retryAfter, c.statusError(key, res)
}

// commentID reads the id out of a 201 answer. Jira adds fields (self, author, body); an unknown
// one is fine, an id that is not a number is not.
func commentID(raw []byte) (string, string) {
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "the answer is not a JSON object with a string id"
	}
	switch {
	case out.ID == "":
		return "", "the answer has no id"
	case !idPattern.MatchString(out.ID):
		return "", fmt.Sprintf("the answer's id %s is not a comment id", truncate(out.ID, 40))
	}
	return out.ID, ""
}

// parseTransitions reads the answer of GET .../transitions.
func parseTransitions(raw []byte) ([]Transition, string) {
	var out struct {
		Transitions *[]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			To   *struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "the answer is not a JSON object with string ids and names"
	}
	if out.Transitions == nil {
		return nil, "the answer has no transitions list"
	}
	if len(*out.Transitions) > maxTransitions {
		return nil, fmt.Sprintf("the answer lists more than %d transitions", maxTransitions)
	}
	list := make([]Transition, 0, len(*out.Transitions))
	for i, t := range *out.Transitions {
		switch {
		case !idPattern.MatchString(t.ID):
			return nil, fmt.Sprintf("transitions[%d].id is not a number", i)
		case !validName(t.Name):
			return nil, fmt.Sprintf("transitions[%d].name is empty, too long or not one line", i)
		}
		to := ""
		if t.To != nil {
			if t.To.Name != "" && !validName(t.To.Name) {
				return nil, fmt.Sprintf("transitions[%d].to.name is too long or not one line", i)
			}
			to = t.To.Name
		}
		list = append(list, Transition{ID: t.ID, Name: t.Name, To: to})
	}
	return list, ""
}

// parseStatus reads the status name out of an issue answered with fields=status.
func parseStatus(raw []byte) (string, string) {
	var out struct {
		Fields struct {
			Status struct {
				Name string `json:"name"`
			} `json:"status"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "the answer is not a JSON object"
	}
	if !validName(out.Fields.Status.Name) {
		return "", "fields.status.name is missing, too long or not one line"
	}
	return out.Fields.Status.Name, ""
}

// validName accepts a status or transition name: one line of text, not empty, of bounded size.
func validName(s string) bool {
	return s != "" && len(s) <= maxNameLen && !strings.ContainsFunc(s, func(r rune) bool { return r < ' ' || r == 0x7f })
}

// MergeRequestComment is what the comment on an issue says about the merge request a run opened
// (specs/delivery/SPEC.md §10). Every text is written to Jira as plain text, never as markup.
type MergeRequestComment struct {
	IID int
	// URL is the merge request's https address, as run.json recorded it.
	URL string
	// Title is the commit header, which is the merge request's title.
	Title string
	// Review, Tests and Cost are left out when empty.
	Review, Tests, Cost string
	RunID               string
}

// linkEscaper percent-encodes what would end or split a [text|url] link early.
var linkEscaper = strings.NewReplacer("|", "%7C", "[", "%5B", "]", "%5D", "{", "%7B", "}", "%7D", "\\", "%5C")

// Wiki renders the comment as Jira wiki markup, two lines: the merge request as a link, with its
// title, and what the run found. The same input always gives the same bytes.
func (m MergeRequestComment) Wiki() string {
	first := fmt.Sprintf("Merge request [!%d|%s]", m.IID, linkEscaper.Replace(m.URL))
	if title := strings.Join(strings.Fields(m.Title), " "); title != "" {
		first += ": " + escape(title)
	}
	var facts []string
	for _, f := range []struct{ label, value string }{{"Review", m.Review}, {"Tests", m.Tests}, {"Cost", m.Cost}} {
		if f.value != "" {
			facts = append(facts, f.label+": "+escape(f.value))
		}
	}
	if m.RunID != "" {
		facts = append(facts, "Run "+escape(m.RunID))
	}
	if len(facts) == 0 {
		return first
	}
	return first + "\n" + strings.Join(facts, " · ")
}
