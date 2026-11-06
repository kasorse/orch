package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// GitLabRunner triggers a pipeline that runs the orchestrator in CI (specs/slack-gitlab
// T2), then follows it until it ends. The job itself opens the merge request.
type GitLabRunner struct {
	cfg   *GitLab
	http  *http.Client
	Slack SlackRef
	poll  time.Duration
}

// SlackRef lets the CI job post to the same thread itself (optional).
type SlackRef struct{ Channel, TS string }

type pipeline struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
}

func (r *GitLabRunner) base() string {
	if r.cfg.BaseURL != "" {
		return strings.TrimRight(r.cfg.BaseURL, "/")
	}
	return "https://gitlab.com"
}

func (r *GitLabRunner) Run(ctx context.Context, in Request, progress func(string)) Outcome {
	task := in.Task
	timeout := 90 * time.Minute
	if d, err := time.ParseDuration(r.cfg.Timeout); err == nil {
		timeout = d
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	hc := r.http
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}

	trig := os.Getenv(r.cfg.TriggerTokenEnv)
	if trig == "" {
		return Outcome{Text: r.cfg.TriggerTokenEnv + " is not set"}
	}
	form := url.Values{}
	form.Set("token", trig)
	form.Set("ref", r.cfg.Ref)
	form.Set("variables[ORCH_TASK]", task)
	if in.Jira != "" {
		form.Set("variables[ORCH_JIRA]", in.Jira) // the job passes it as --jira
	}
	form.Set("variables[ORCH_SLACK_CHANNEL]", r.Slack.Channel)
	form.Set("variables[ORCH_SLACK_TS]", r.Slack.TS)
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/trigger/pipeline", r.base(), url.PathEscape(r.cfg.ProjectID))
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var p pipeline
	if err := doJSON(hc, req, &p); err != nil {
		return Outcome{Text: "could not trigger the pipeline: " + err.Error()}
	}
	progress(fmt.Sprintf(":rocket: pipeline <%s|#%d> started", p.WebURL, p.ID))

	apiTok := os.Getenv(r.cfg.APITokenEnv)
	if r.cfg.APITokenEnv == "" || apiTok == "" {
		return Outcome{OK: true, Text: "pipeline " + p.WebURL + " started; no api_token_env, so it is not followed"}
	}
	poll := r.poll
	if poll == 0 {
		poll = 30 * time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return Outcome{Text: "stopped following " + p.WebURL + ": " + ctx.Err().Error()}
		case <-time.After(poll):
		}
		u := fmt.Sprintf("%s/api/v4/projects/%s/pipelines/%d", r.base(), url.PathEscape(r.cfg.ProjectID), p.ID)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("PRIVATE-TOKEN", apiTok)
		var cur pipeline
		if err := doJSON(hc, req, &cur); err != nil {
			continue
		}
		switch cur.Status {
		case "success":
			return Outcome{OK: true, Text: ":white_check_mark: pipeline succeeded: " + p.WebURL}
		case "failed", "canceled", "skipped":
			return Outcome{Text: fmt.Sprintf(":x: pipeline %s: %s", cur.Status, p.WebURL)}
		}
	}
}

func doJSON(hc *http.Client, req *http.Request, v any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, v)
}
