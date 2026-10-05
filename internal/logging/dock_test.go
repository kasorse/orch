package logging

import (
	"bytes"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

// Every card is exactly dockWidth cells wide, so the sidebar is one straight column.
func TestDockCardsHaveFixedWidth(t *testing.T) {
	r := &ConsoleReporter{Pretty: true, MaxFixIterations: 2}
	r.run = &domain.Run{FixIterations: 1, Workflow: &domain.Workflow{Steps: []domain.Step{{Action: "analyze"}, {Action: "implement"}, {Action: "test"}}},
		Executions: []domain.StepExecution{{Action: "analyze", Status: domain.StepCompleted}}}
	r.action, r.inStep = "implement", true
	r.ProviderUsage(domain.Usage{InputTokens: 10, CacheReadTokens: 9000, OutputTokens: 400, CostUSD: 0.02, Turns: 3,
		Limits: &domain.RateLimits{FiveHour: &domain.LimitWindow{Utilization: 0.1}, Weekly: &domain.LimitWindow{Utilization: 0.42}}})
	r.repo = &RepoInfo{Branch: "feature/a-very-long-branch-name-that-goes-on", Commits: []CommitInfo{{Hash: "abc1234", Subject: strings.Repeat("long subject ", 10)}}, MoreCommits: 3}
	lines := r.dockLines()
	if len(lines) < 12 {
		t.Fatalf("expected four cards, got %d lines", len(lines))
	}
	for i, l := range lines {
		if w := visibleWidth(l); w != dockWidth {
			t.Errorf("line %d is %d wide, want %d: %q", i, w, dockWidth, ansiRE.ReplaceAllString(l, ""))
		}
	}
	plain := ansiRE.ReplaceAllString(strings.Join(lines, "\n"), "")
	for _, want := range []string{"Workflow 1/3", "✓ analyze", "✻ implement  running", "○ test", "↻ fixes 1/2", "$0.020", "5-hour", "Repo · feature/"} {
		if !strings.Contains(plain, want) {
			t.Errorf("expected %q in sidebar:\n%s", want, plain)
		}
	}
}

// A write with a newline erases the sidebar first and redraws it after, so no copy of it
// can scroll into the scrollback; a write without one (the spinner) passes through as is.
func TestDockWriteErasesAroundScrollingWrites(t *testing.T) {
	var term bytes.Buffer
	d := &dock{out: &term, visible: true, col: 81, lines: []string{"x"}, drawn: 1}
	var main bytes.Buffer
	d.write(&term, []byte("\rspinner frame"))
	if got := term.String(); got != "\rspinner frame" {
		t.Errorf("spinner write was altered: %q", got)
	}
	term.Reset()
	d.write(&term, []byte("a line\n"))
	out := term.String()
	erase := strings.Index(out, "\x1b[1;81H\x1b[K")
	text := strings.Index(out, "a line\n")
	if erase < 0 || text < 0 || erase > text {
		t.Errorf("the sidebar must be erased before the scrolling write: %q", out)
	}
	_ = main
}
