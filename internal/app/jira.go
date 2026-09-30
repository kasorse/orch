package app

import (
	"context"
	"net/url"
	"os"
	"time"

	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/jira"
	"orchestrator/internal/logging"
)

// taskFromJira fetches the issue key names and renders it, with the operator's notes,
// into the task text (specs/jira/SPEC.md §10). The whole text is printed before anything
// is decided, so the operator sees what the ticket's author wrote (F2). This is the only
// place the credential values are read from the environment.
func taskFromJira(ctx context.Context, cfg config.Config, key, notes string, reporter *logging.ConsoleReporter) (domain.Ticket, string, error) {
	j := cfg.Jira
	creds := jira.NewCredentials(j.Auth, os.Getenv(j.EmailEnv), os.Getenv(j.TokenEnv))
	fields := make([]jira.Field, len(j.Fields))
	for i, f := range j.Fields {
		fields[i] = jira.Field{ID: f.ID, Label: f.Label}
	}
	client := jira.NewHTTPClient(jira.Options{
		BaseURL: j.BaseURL, Auth: j.Auth, EmailEnv: j.EmailEnv, TokenEnv: j.TokenEnv,
		Timeout: j.Timeout, MaxRetries: j.MaxRetries, Fields: fields,
		UserAgent: "orchestrator/" + Version,
	}, creds)

	host := j.BaseURL
	if u, err := url.Parse(j.BaseURL); err == nil {
		host = u.Host
	}
	reporter.FetchingTicket(key, host)
	started := time.Now()
	ticket, err := client.Fetch(ctx, key)
	if err != nil {
		return domain.Ticket{}, "", err
	}
	reporter.TicketFetched(ticket, time.Since(started))
	if ticket.RequestedKey != "" {
		reporter.TicketMoved(ticket.RequestedKey, ticket.Key)
	}

	text, err := jira.Render(ticket, notes, j.MaxBytes)
	if err != nil {
		return domain.Ticket{}, "", err
	}
	reporter.TaskBlock(ticket.Key, text)
	return ticket, text, nil
}
