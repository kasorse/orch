package domain

import (
	"fmt"
	"time"
)

type TestResult struct {
	Command  string        `json:"command"`
	ExitCode int           `json:"exit_code"`
	Success  bool          `json:"success"`
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	Duration time.Duration `json:"duration"`
}

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

var validSeverities = map[Severity]bool{
	SeverityCritical: true,
	SeverityHigh:     true,
	SeverityMedium:   true,
	SeverityLow:      true,
}

type ReviewStatus string

const (
	ReviewPass ReviewStatus = "pass"
	ReviewFail ReviewStatus = "fail"
)

type ReviewIssue struct {
	Severity    Severity `json:"severity"`
	File        string   `json:"file"`
	Description string   `json:"description"`
}

type ReviewResult struct {
	// Status is the raw self-reported value from the LLM - informational only. Decide()
	// is the authoritative pass/fail computation based on issue severities, since the
	// orchestrator never trusts an LLM's self-assessment of its own review.
	Status  ReviewStatus  `json:"status"`
	Summary string        `json:"summary"`
	Issues  []ReviewIssue `json:"issues"`
}

// Decide computes the authoritative pass/fail outcome: critical or high severity issues
// fail the review; medium/low issues pass with warning.
func (r ReviewResult) Decide() ReviewStatus {
	for _, i := range r.Issues {
		if i.Severity == SeverityCritical || i.Severity == SeverityHigh {
			return ReviewFail
		}
	}
	return ReviewPass
}

func (r ReviewResult) Validate() error {
	if r.Status != ReviewPass && r.Status != ReviewFail {
		return fmt.Errorf("unsupported review status %q", r.Status)
	}
	for _, issue := range r.Issues {
		if !validSeverities[issue.Severity] {
			return fmt.Errorf("unsupported review issue severity %q", issue.Severity)
		}
	}
	return nil
}

// ResearchArtifact is the output of the read-only research stage: external knowledge
// (approaches, libraries, alternatives) gathered before touching the code.
type ResearchArtifact struct {
	Summary        string   `json:"summary"`
	Findings       []string `json:"findings"`
	Options        []string `json:"options"`
	Recommendation string   `json:"recommendation"`
	References     []string `json:"references"`
	// Answer is set when research is the workflow's final read-only step and no
	// development follows: it is the reply to the user's request (TASKS.md В4).
	Answer string `json:"answer,omitempty"`
}

type AnalysisArtifact struct {
	Summary        string   `json:"summary"`
	RootCause      string   `json:"root_cause"`
	AffectedFiles  []string `json:"affected_files"`
	Recommendation string   `json:"recommendation"`
	Risks          []string `json:"risks"`
	// Answer: see ResearchArtifact.Answer.
	Answer string `json:"answer,omitempty"`
}

type PlanArtifact struct {
	Summary       string   `json:"summary"`
	Steps         []string `json:"steps"`
	AffectedFiles []string `json:"affected_files"`
	Risks         []string `json:"risks"`
	// Answer: see ResearchArtifact.Answer.
	Answer string `json:"answer,omitempty"`
}

type ImplementationArtifact struct {
	Summary      string   `json:"summary"`
	ChangedFiles []string `json:"changed_files"`
	TestsAdded   []string `json:"tests_added"`
}

type AnswerArtifact struct {
	Answer string `json:"answer"`
}
