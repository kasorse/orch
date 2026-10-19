package app

import (
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func TestCommitOffer(t *testing.T) {
	long := strings.Repeat("слово ", 20) // 120 runes, multi-byte
	cases := []struct {
		name   string
		run    domain.Run
		impl   *domain.ImplementationArtifact
		header string
		body   string
	}{
		{
			name: "summary_short is the header",
			run:  domain.Run{ID: "r1", Task: domain.Task{Description: "fix it"}},
			impl: &domain.ImplementationArtifact{SummaryShort: " Fix Add to use + ",
				Summary: "Add subtracted its operands; it now adds them.\nThe test covers negatives."},
			header: "Fix Add to use +",
			body:   "Orchestrator-Run: r1\n",
		},
		{
			name:   "no header without a summary_short",
			run:    domain.Run{ID: "r1", Task: domain.Task{Description: "fix it"}},
			impl:   &domain.ImplementationArtifact{Summary: "Fix Add to use +"},
			header: "",
			body:   "Orchestrator-Run: r1\n",
		},
		{
			name:   "no header without an implementation",
			run:    domain.Run{ID: "r1", Task: domain.Task{Description: "fix Add()"}},
			header: "",
			body:   "Orchestrator-Run: r1\n",
		},
		{
			name: "Jira key prefix",
			run: domain.Run{ID: "r1", Task: domain.Task{Description: "ticket",
				Source: &domain.TaskSource{Kind: domain.TaskSourceJira, Key: "PROJ-7"}}},
			impl:   &domain.ImplementationArtifact{SummaryShort: "Fix login", Summary: "long"},
			header: "PROJ-7: Fix login",
			body:   "Orchestrator-Run: r1\n",
		},
		{
			name: "story task with a Jira key: prefix and both trailers",
			run: domain.Run{ID: "r1", Task: domain.Task{Description: "text", Source: &domain.TaskSource{
				Kind: domain.TaskSourceStory, Story: "refunds", Task: "RF02", Attempt: 1, Jira: "PAY-124"}}},
			impl:   &domain.ImplementationArtifact{SummaryShort: "Add partial refunds", Summary: "long"},
			header: "PAY-124: Add partial refunds",
			body:   "Orchestrator-Run: r1\nOrchestrator-Story: refunds/RF02\n",
		},
		{
			name: "story task without a Jira key: no prefix, the story trailer",
			run: domain.Run{ID: "r1", Task: domain.Task{Description: "text", Source: &domain.TaskSource{
				Kind: domain.TaskSourceStory, Story: "refunds", Task: "RF02", Attempt: 2}}},
			impl:   &domain.ImplementationArtifact{SummaryShort: "Add partial refunds"},
			header: "Add partial refunds",
			body:   "Orchestrator-Run: r1\nOrchestrator-Story: refunds/RF02\n",
		},
		{
			name:   "long summary_short is cut and kept in the body",
			run:    domain.Run{ID: "r1"},
			impl:   &domain.ImplementationArtifact{SummaryShort: long},
			header: strings.TrimSpace(string([]rune(long)[:69])) + "...",
			body:   strings.TrimSpace(long) + "\n\nOrchestrator-Run: r1\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := commitOffer(&c.run, c.impl)
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
			{Action: domain.ActionReview, ReviewResult: &domain.ReviewResult{Summary: "a long account", SummaryShort: "still broken",
				Issues: []domain.ReviewIssue{{Severity: domain.SeverityHigh}}}},
		},
		SecretFindings: []domain.SecretFinding{{File: "cfg.go", Line: 3, Rule: "aws-access-key"}},
	}
	got := commitOffer(&run, nil).Warnings
	want := []string{"the review did not pass: still broken", "a likely secret was added at cfg.go:3 (aws-access-key)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("warnings = %q, want %q", got, want)
	}

	run.Executions[1].ReviewResult.Issues = nil
	run.SecretFindings = nil
	if got := commitOffer(&run, nil).Warnings; len(got) != 0 {
		t.Errorf("a passing last review warns: %q", got)
	}
}
