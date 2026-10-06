package app

import (
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func TestCommitOffer(t *testing.T) {
	long := strings.Repeat("слово ", 20) // 120 runes, multi-byte
	cases := []struct {
		name    string
		run     domain.Run
		summary string
		header  string
		body    string
	}{
		{
			name:    "summary is the header",
			run:     domain.Run{ID: "r1", Task: domain.Task{Description: "fix it"}},
			summary: "Fix Add to use +",
			header:  "Fix Add to use +",
			body:    "Orchestrator-Run: r1\n",
		},
		{
			name:   "task when there is no summary",
			run:    domain.Run{ID: "r1", Task: domain.Task{Description: "  fix Add()\nmore detail"}},
			header: "fix Add()",
			body:   "fix Add()\nmore detail\n\nOrchestrator-Run: r1\n",
		},
		{
			name: "Jira key prefix",
			run: domain.Run{ID: "r1", Task: domain.Task{Description: "ticket",
				Source: &domain.TaskSource{Kind: domain.TaskSourceJira, Key: "PROJ-7"}}},
			summary: "Fix login",
			header:  "PROJ-7: Fix login",
			body:    "Orchestrator-Run: r1\n",
		},
		{
			name:    "long summary is cut and kept in the body",
			run:     domain.Run{ID: "r1"},
			summary: long,
			header:  strings.TrimSpace(string([]rune(long)[:69])) + "...",
			body:    strings.TrimSpace(long) + "\n\nOrchestrator-Run: r1\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := commitOffer(&c.run, c.summary)
			if o.Header != c.header {
				t.Errorf("header = %q, want %q", o.Header, c.header)
			}
			if o.Body != c.body {
				t.Errorf("body = %q, want %q", o.Body, c.body)
			}
			if n := len([]rune(o.Header)); n > commitHeaderMax {
				t.Errorf("header has %d runes", n)
			}
		})
	}
}

func TestCommitOffer_Warnings(t *testing.T) {
	run := domain.Run{ID: "r1",
		Executions: []domain.StepExecution{
			{Action: domain.ActionReview, ReviewResult: &domain.ReviewResult{Summary: "old",
				Issues: []domain.ReviewIssue{{Severity: domain.SeverityCritical}}}},
			{Action: domain.ActionReview, ReviewResult: &domain.ReviewResult{Summary: "still broken",
				Issues: []domain.ReviewIssue{{Severity: domain.SeverityHigh}}}},
		},
		SecretFindings: []domain.SecretFinding{{File: "cfg.go", Line: 3, Rule: "aws-access-key"}},
	}
	got := commitOffer(&run, "x").Warnings
	want := []string{"the review did not pass: still broken", "a likely secret was added at cfg.go:3 (aws-access-key)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("warnings = %q, want %q", got, want)
	}

	run.Executions[1].ReviewResult.Issues = nil
	run.SecretFindings = nil
	if got := commitOffer(&run, "x").Warnings; len(got) != 0 {
		t.Errorf("a passing last review warns: %q", got)
	}
}
