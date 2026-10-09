package domain

import (
	"fmt"
	"strings"
	"time"
)

type TestResult struct {
	Command  string        `json:"command"`
	ExitCode int           `json:"exit_code"`
	Success  bool          `json:"success"`
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	Duration time.Duration `json:"duration"`
	// Sandboxed is true when the command ran under test.sandbox (bwrap).
	Sandboxed bool `json:"sandboxed,omitempty"`
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

// MaxCriterionEvidenceBytes bounds the evidence of one CriterionVerdict.
const MaxCriterionEvidenceBytes = 500

// CriterionVerdict is the review's verdict on one acceptance criterion of the plan
// (specs/quality/SPEC.md §7.1). Index counts from 1; the model echoes only the index and
// Go fills Criterion from the plan after parsing.
type CriterionVerdict struct {
	Index     int    `json:"index"`
	Criterion string `json:"criterion"`
	Met       bool   `json:"met"`
	Evidence  string `json:"evidence"`
}

type ReviewResult struct {
	// Status is the raw self-reported value from the LLM - informational only. Decide()
	// is the authoritative pass/fail computation based on issue severities and criteria
	// verdicts, since the orchestrator never trusts an LLM's self-assessment of its own
	// review.
	Status       ReviewStatus  `json:"status"`
	Summary      string        `json:"summary"`
	SummaryShort string        `json:"summary_short,omitempty"`
	Issues       []ReviewIssue `json:"issues"`
	// Criteria is one verdict per acceptance criterion of the plan; absent when the plan
	// had none, and in a result written before the field existed.
	Criteria []CriterionVerdict `json:"criteria,omitempty"`
	// Diff records what the reviewer was shown (specs/hardening/SPEC.md §5.3). The
	// orchestrator sets it after parsing; whatever the model wrote there is replaced.
	Diff *ReviewDiffSummary `json:"diff,omitempty"`
}

// ReviewDiffSummary counts the files of the review diff by how they were shown. Files
// counts every changed file; Truncated is true when any text file's diff was left out for
// the budget or the file was not even listed.
type ReviewDiffSummary struct {
	Bytes      int  `json:"bytes"`
	Files      int  `json:"files"`
	Shown      int  `json:"shown"`
	Summarized int  `json:"summarized"`
	Withheld   int  `json:"withheld"`
	Truncated  bool `json:"truncated"`
}

// Decide computes the authoritative pass/fail outcome: critical or high severity issues
// fail the review, and so does an unmet acceptance criterion, which counts as a high
// issue; medium/low issues pass with warning.
func (r ReviewResult) Decide() ReviewStatus {
	for _, i := range r.Issues {
		if i.Severity == SeverityCritical || i.Severity == SeverityHigh {
			return ReviewFail
		}
	}
	for _, c := range r.Criteria {
		if !c.Met {
			return ReviewFail
		}
	}
	return ReviewPass
}

// UnmetCriteria returns the verdicts with Met false, in order.
func (r ReviewResult) UnmetCriteria() []CriterionVerdict {
	var unmet []CriterionVerdict
	for _, c := range r.Criteria {
		if !c.Met {
			unmet = append(unmet, c)
		}
	}
	return unmet
}

// ValidateCriteria checks the shape of the verdicts without the plan: indexes within
// 1..len(Criteria), each once, and evidence of 1..MaxCriterionEvidenceBytes bytes. The
// count against the plan is checked where the review is parsed.
func (r ReviewResult) ValidateCriteria() error {
	seen := make(map[int]bool, len(r.Criteria))
	for _, c := range r.Criteria {
		if c.Index < 1 || c.Index > len(r.Criteria) {
			return fmt.Errorf("criterion index %d is out of range 1..%d", c.Index, len(r.Criteria))
		}
		if seen[c.Index] {
			return fmt.Errorf("criterion index %d appears more than once", c.Index)
		}
		seen[c.Index] = true
		if strings.TrimSpace(c.Evidence) == "" {
			return fmt.Errorf("criterion %d has no evidence", c.Index)
		}
		if len(c.Evidence) > MaxCriterionEvidenceBytes {
			return fmt.Errorf("criterion %d evidence is %d bytes, at most %d", c.Index, len(c.Evidence), MaxCriterionEvidenceBytes)
		}
	}
	return nil
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
	if err := r.ValidateCriteria(); err != nil {
		return err
	}
	if d := r.Diff; d != nil {
		if d.Bytes < 0 || d.Files < 0 || d.Shown < 0 || d.Summarized < 0 || d.Withheld < 0 ||
			d.Shown+d.Summarized+d.Withheld > d.Files {
			return fmt.Errorf("inconsistent review diff counts %+v", *d)
		}
	}
	return nil
}

// ResearchArtifact is the output of the read-only research stage: external knowledge
// (approaches, libraries, alternatives) gathered before touching the code.
type ResearchArtifact struct {
	Summary        string   `json:"summary"`
	SummaryShort   string   `json:"summary_short,omitempty"`
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
	SummaryShort   string   `json:"summary_short,omitempty"`
	RootCause      string   `json:"root_cause"`
	AffectedFiles  []string `json:"affected_files"`
	Recommendation string   `json:"recommendation"`
	Risks          []string `json:"risks"`
	// Answer: see ResearchArtifact.Answer.
	Answer string `json:"answer,omitempty"`
}

type PlanArtifact struct {
	Summary       string   `json:"summary"`
	SummaryShort  string   `json:"summary_short,omitempty"`
	Steps         []string `json:"steps"`
	AffectedFiles []string `json:"affected_files"`
	Risks         []string `json:"risks"`
	// AcceptanceCriteria are the checkable statements about the result the plan commits to,
	// at most 8 of 300 bytes (specs/quality/SPEC.md §4.2). An artifact written before the
	// field existed has none.
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	// Answer: see ResearchArtifact.Answer.
	Answer string `json:"answer,omitempty"`
}

type ImplementationArtifact struct {
	Summary string `json:"summary"`
	// SummaryShort is the model's one-line version of Summary, for a reader skimming the
	// run (every stage's artifact and the review result carry one); implement's becomes
	// the commit message (MVP.md §42.2). Empty when the model left it out.
	SummaryShort string   `json:"summary_short,omitempty"`
	ChangedFiles []string `json:"changed_files"`
	TestsAdded   []string `json:"tests_added"`
}
