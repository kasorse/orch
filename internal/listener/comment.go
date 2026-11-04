package listener

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kasorse/orch/internal/domain"
)

// The comments the listener writes are Go's wiki templates (specs/jira-listener/SPEC.md §8): the
// first line is Marker, the last is "Run <id>", and every text a model wrote is escaped with the
// Escape the caller gives (jira.Escape, the escaping of specs/delivery/SPEC.md §39.12) so it
// cannot start a macro, a link or a mention.

const (
	// Marker starts the first line of every comment the listener writes; it is how the listener
	// tells its own comments from a person's. It equals jira.BotMarker.
	Marker = "[orchestrator]"
	// MaxCommentBytes bounds a comment.
	MaxCommentBytes = 8 << 10
	// Withheld replaces a point or an issue that looked like a secret.
	Withheld = "(withheld: looked like a secret)"

	shortened = "(shortened)"
	// maxLineBytes bounds one model-written item in a list.
	maxLineBytes = 600
)

// Escape writes text so that wiki markup reads it as plain text.
type Escape func(string) string

// Compose is the comment: Marker, the body lines, and "Run <id>" (left out when runID is empty,
// as for a ticket blocked before any run). A body that would make the comment longer than
// MaxCommentBytes is cut at a character boundary and marked.
func Compose(body []string, runID string) string {
	last := ""
	if runID != "" {
		last = "Run " + runID
	}
	fixed := len(Marker) + 2
	if last != "" {
		fixed += len(last) + 1
	}
	text := strings.Join(body, "\n")
	if room := MaxCommentBytes - fixed; len(text) > room {
		keep := room - len(shortened) - 1
		for keep > 0 && !utf8.RuneStart(text[keep]) {
			keep--
		}
		text = strings.TrimRight(text[:max(keep, 0)], "\n ") + "\n" + shortened
	}
	out := Marker + "\n" + text
	if last != "" {
		out += "\n" + last
	}
	return out
}

// runLine is the run id as the last line of a comment prints it: plain when it has the shape of one.
func runLine(esc Escape, runID string) string {
	if ValidRunID(runID) {
		return runID
	}
	return esc(runID)
}

// ComposeFor is Compose with the run id made safe to print.
func ComposeFor(esc Escape, body []string, runID string) string {
	if runID != "" {
		runID = runLine(esc, runID)
	}
	return Compose(body, runID)
}

// oneLine collapses s to one line and cuts it to maxLineBytes at a character boundary.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxLineBytes {
		return s
	}
	cut := maxLineBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// text is a model-written string made safe: one line, bounded, escaped.
func text(esc Escape, s string) string { return esc(oneLine(s)) }

// NeedsInput is the comment of a task that stayed unclear (SPEC §8): the open points, numbered,
// and where to move the ticket after answering. A point the caller found to look like a secret
// is passed as Withheld.
func NeedsInput(esc Escape, points []string, backTo string) []string {
	body := []string{"The task is not clear enough to start. Open points:"}
	if len(points) == 0 {
		body = append(body, "No single point was left open, but the task is still rated unclear.")
	}
	for _, p := range points {
		body = append(body, "# "+text(esc, p))
	}
	body = append(body, "", "Answer in a comment and move the ticket back to "+esc(oneLine(backTo))+".")
	return body
}

// Failure is the comment of a run that did not deliver (SPEC §8): the reason and, when there is one,
// the last test or check that failed.
func Failure(esc Escape, reason, failed string) []string {
	if strings.TrimSpace(reason) == "" {
		reason = "the run ended without a merge request"
	}
	body := []string{"The run did not deliver: " + text(esc, reason)}
	if strings.TrimSpace(failed) != "" {
		body = append(body, "Last failure: "+text(esc, failed))
	}
	return body
}

// Review adds a review verdict and its issues to a failure comment.
func Review(esc Escape, verdict string, issues []string) []string {
	var body []string
	if verdict != "" {
		body = append(body, "Review: "+text(esc, verdict))
	}
	for _, i := range issues {
		body = append(body, "# "+text(esc, i))
	}
	return body
}

// Error is the comment of a child that ended with an error: the message as it printed it.
func Error(esc Escape, msg string) []string {
	if strings.TrimSpace(msg) == "" {
		msg = "the run ended with an error that was not reported"
	}
	return []string{"The run ended with an error: " + text(esc, msg)}
}

// Blocked is the comment of a ticket that cannot be run as it is, before any run (SPEC §5, §7.1):
// why, and what to change.
func Blocked(esc Escape, reason string) []string {
	return []string{text(esc, reason)}
}

// Spent is the comment of a ticket whose attempts are used up (SPEC §8).
func Spent(esc Escape, key string, attempts int) []string {
	// A ticket key (PAY-12) is plain text in wiki markup; one that is not shaped like a key is escaped.
	if !domain.ValidTicketKey(key) {
		key = esc(key)
	}
	return []string{
		strconv.Itoa(attempts) + " attempts spent; remove " + key + " from .orchestrator/listener/state.json to try again.",
	}
}

// Moved is the line added to a comment whose transition was not made, because the ticket was no
// longer where the listener put it (SPEC §8, D13).
func Moved(esc Escape, now, want string) string {
	return fmt.Sprintf("The ticket was moved to %s during the run, so the orchestrator did not move it to %s.", esc(oneLine(now)), esc(oneLine(want)))
}
