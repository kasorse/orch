package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// Ticket is what the bridge reads of a Jira issue to choose a project.
type Ticket struct {
	Key, Summary, Description string
	Components, Labels        []string
}

// TicketReader reads a ticket; a fake in tests.
type TicketReader func(ctx context.Context, key string) (Ticket, error)

func jiraReader(j *JiraConfig) TicketReader {
	hc := &http.Client{Timeout: 15 * time.Second}
	return func(ctx context.Context, key string) (Ticket, error) {
		env := os.Environ()
		if j.EnvCommand != "" {
			extra, err := envFromCommand(ctx, j.EnvCommand)
			if err != nil {
				return Ticket{}, err
			}
			env = mergeEnv(env, extra)
		}
		get := func(name string) string {
			for i := len(env) - 1; i >= 0; i-- {
				if k, v, _ := strings.Cut(env[i], "="); k == name {
					return v
				}
			}
			return ""
		}
		u := strings.TrimRight(j.BaseURL, "/") + "/rest/api/2/issue/" + url.PathEscape(key) + "?fields=summary,description,components,labels"
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.SetBasicAuth(get(j.EmailEnv), get(j.TokenEnv))
		resp, err := hc.Do(req)
		if err != nil {
			return Ticket{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return Ticket{}, fmt.Errorf("jira %s: HTTP %d", key, resp.StatusCode)
		}
		var body struct {
			Key    string `json:"key"`
			Fields struct {
				Summary     string   `json:"summary"`
				Description string   `json:"description"`
				Labels      []string `json:"labels"`
				Components  []struct {
					Name string `json:"name"`
				} `json:"components"`
			} `json:"fields"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return Ticket{}, err
		}
		t := Ticket{Key: body.Key, Summary: body.Fields.Summary, Description: body.Fields.Description, Labels: body.Fields.Labels}
		for _, c := range body.Fields.Components {
			t.Components = append(t.Components, c.Name)
		}
		return t, nil
	}
}

var summaryTagRE = regexp.MustCompile(`\[([^\]]+)\]`)

// ProjectFor picks the project whose jira_match names one of the ticket's components, labels
// or [tags] in the summary. Components win over labels, labels over tags; "" when nothing
// matches or two projects tie.
func (c *Config) ProjectFor(t Ticket) string {
	var tags []string
	for _, m := range summaryTagRE.FindAllStringSubmatch(t.Summary, -1) {
		tags = append(tags, m[1])
	}
	for _, values := range [][]string{t.Components, t.Labels, tags} {
		found := ""
		for _, name := range c.ProjectNames() {
			if matchesAny(c.Projects[name].JiraMatch, values) {
				if found != "" {
					return ""
				}
				found = name
			}
		}
		if found != "" {
			return found
		}
	}
	return ""
}

func matchesAny(want, values []string) bool {
	for _, w := range want {
		for _, v := range values {
			if strings.EqualFold(strings.TrimSpace(v), w) {
				return true
			}
		}
	}
	return false
}
