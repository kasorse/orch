package jira

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

// Render turns a validated ticket and the operator's notes into the task text
// (SPEC.md §9). The same input always gives the same bytes: values are verbatim except
// that \r\n becomes \n, trailing whitespace is removed from every line and blank lines at
// both ends of each section are dropped. Wiki markup is not converted. Empty sections are
// left out, and the notes come last so the operator's own words are the final thing each
// stage reads. A text longer than maxBytes is refused, never truncated. At most one
// RenderOptions may follow; without it the output has no discussion.
func Render(t domain.Ticket, notes string, maxBytes int, opts ...RenderOptions) (string, error) {
	var opt RenderOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	header := []string{clean(t.Key + ": " + t.Summary)}
	if typ := clean(t.Type); typ != "" {
		header = append(header, "Type: "+typ)
	}
	if t.Parent != nil {
		parent := "Parent: " + t.Parent.Key
		if summary := clean(t.Parent.Summary); summary != "" {
			parent += " — " + summary
		}
		header = append(header, clean(parent))
	}
	if len(t.Labels) > 0 {
		// the listener picks the service and the GitLab project by label; the models see them here
		header = append(header, clean("Labels: "+strings.Join(t.Labels, ", ")))
	}
	header = append(header, "Source: "+t.URL)

	var b strings.Builder
	b.WriteString(strings.Join(header, "\n"))
	section := func(label, body string) {
		if body = clean(body); body != "" {
			fmt.Fprintf(&b, "\n\n%s:\n%s", label, body)
		}
	}
	section("Description", t.Description)
	section(discussionLabel, opt.discussion())
	for _, f := range t.Fields {
		section(f.Label, f.Value)
	}
	section("Operator notes", strings.TrimSpace(notes))
	b.WriteString("\n")

	out := b.String()
	if len(out) > maxBytes {
		return "", apperr.NewConfigError(fmt.Sprintf(
			"%s renders to %d bytes, more than jira.max_bytes (%d); raise the limit or trim the ticket",
			t.Key, len(out), maxBytes), nil)
	}
	return out, nil
}

const (
	discussionLabel = "Discussion since the orchestrator's questions"
	// BotMarker starts the first line of every comment the orchestrator posts
	// (specs/jira-listener/SPEC.md §8).
	BotMarker = "[orchestrator]"
)

// RenderOptions add the comments of the issue to the task text, for the Jira listener's
// tasks only (specs/jira-listener/SPEC.md §7.1).
type RenderOptions struct {
	// Comments are the issue's comments, oldest first.
	Comments []domain.TicketComment
	// Bot is the account the credentials belong to (Client.Myself); the comments it wrote
	// carry BotMarker on their first line. Without it there is no discussion.
	Bot string
}

// discussion is the section's body: the bot's last comment that starts with BotMarker and every
// comment after it, each under a "--- who, when" line. Authors are not named. With no such
// comment it is empty, and the section is left out.
func (o RenderOptions) discussion() string {
	if o.Bot == "" {
		return ""
	}
	last := -1
	for i, c := range o.Comments {
		if c.Author == o.Bot && strings.HasPrefix(clean(c.Body), BotMarker) {
			last = i
		}
	}
	if last < 0 {
		return ""
	}
	var parts []string
	for _, c := range o.Comments[last:] {
		body := clean(c.Body)
		if body == "" {
			continue
		}
		who := "comment"
		if c.Author == o.Bot {
			who = "orchestrator"
		}
		parts = append(parts, fmt.Sprintf("--- %s, %s\n%s", who, c.Created.UTC().Format("2006-01-02 15:04"), body))
	}
	return strings.Join(parts, "\n")
}

// clean normalises line endings, strips each line's trailing whitespace and drops blank
// lines at both ends.
func clean(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
