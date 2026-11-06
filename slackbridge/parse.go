package main

import (
	"errors"
	"regexp"
	"strings"
)

var mentionRE = regexp.MustCompile(`<@[A-Z0-9]+>`)

// projectPrefixRE matches "name: task" at the start of a message.
var projectPrefixRE = regexp.MustCompile(`^\*?([A-Za-z0-9][A-Za-z0-9._-]*)\*?\s*:\s*`)

// Request is what one Slack message asks for.
type Request struct {
	Project string
	// Task is the message without the ticket: with Jira, extra instructions and context that
	// go along with the ticket (may be empty).
	Task string
	Jira string // an issue key; the orchestrator reads the ticket
	// Force: "Run anyway" - go on even though the orchestrator rated the task unclear.
	Force bool
	// ticketText is the ticket's summary and description, read by the bridge (for api_spec).
	ticketText string
	// apiDone: the API contract was already added to Task (a reply or Run anyway re-plans).
	apiDone bool
	// projectSet: the message (or its channel) named the project, so the ticket does not.
	projectSet bool
}

// ticketRE finds a ticket anywhere in the text: a /browse/KEY URL or a KEY-123.
var ticketRE = regexp.MustCompile(`(?:https://[^\s/>|]+/browse/)?\b([A-Z][A-Z0-9]+-[0-9]+)\b`)

// shortTicketRE is a bare number at the start, "15230" or "#15230", expanded with
// jira.default_project_key.
var shortTicketRE = regexp.MustCompile(`^#?([0-9]{2,7})(?:[\s,.:;!?]|$)`)

// Parse turns a message into a project, a ticket and a task. The project is, in order: a
// known "name:" prefix, the channel's project, the default project; Handle may still choose
// it from the ticket when the message did not name one.
func (c *Config) Parse(text, channel string) (Request, error) {
	t := strings.TrimSpace(mentionRE.ReplaceAllString(text, ""))
	t = unescapeSlack(t)
	var req Request
	if m := projectPrefixRE.FindStringSubmatch(t); m != nil && c.Projects[strings.ToLower(m[1])] != nil {
		req.Project = strings.ToLower(m[1])
		req.projectSet = true
		t = t[len(m[0]):]
	} else if p := c.Channels[channel]; p != "" {
		req.Project = p
		req.projectSet = true
	} else {
		req.Project = c.DefaultProject
	}
	t = strings.TrimSpace(t)
	if loc := ticketRE.FindStringSubmatchIndex(t); loc != nil {
		req.Jira = t[loc[2]:loc[3]]
		t = t[:loc[0]] + " " + t[loc[1]:]
	} else if m := shortTicketRE.FindStringSubmatch(t); m != nil && c.Jira != nil && c.Jira.DefaultProjectKey != "" {
		req.Jira = c.Jira.DefaultProjectKey + "-" + m[1]
		t = t[len(m[0]):]
	}
	req.Task = strings.Trim(strings.Join(strings.Fields(t), " "), ",.:;!?- ")
	if req.Task == "" && req.Jira == "" {
		return req, errors.New("the task is empty")
	}
	if req.Project == "" && req.Jira == "" {
		return req, errors.New("no project: start the message with `<project>: ` (" + strings.Join(c.ProjectNames(), ", ") + ")")
	}
	return req, nil
}

func (c *Config) ProjectNames() []string {
	var n []string
	for k := range c.Projects {
		n = append(n, k)
	}
	sortStrings(n)
	return n
}

// unescapeSlack undoes Slack's message escaping and turns <url|label> links into the url.
func unescapeSlack(s string) string {
	s = regexp.MustCompile(`<((?:https?|mailto):[^|>]+)(?:\|[^>]*)?>`).ReplaceAllString(s, "$1")
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(s)
}
