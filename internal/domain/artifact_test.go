package domain

import (
	"strings"
	"testing"
)

func TestSplitFailedTests(t *testing.T) {
	pre := &TestResult{FailedTests: []string{"a", "b"}, ReportFormat: "gotext"}
	old, fresh, ok := SplitFailedTests(pre, &TestResult{FailedTests: []string{"a", "c"}, ReportFormat: "gotext"})
	if !ok || strings.Join(old, ",") != "a" || strings.Join(fresh, ",") != "c" {
		t.Errorf("split = %v, %v, %v", old, fresh, ok)
	}
	for name, cur := range map[string]*TestResult{
		"nil":              nil,
		"other format":     {FailedTests: []string{"a"}, ReportFormat: "pytest"},
		"no names":         {ReportFormat: "gotext"},
		"no format either": {FailedTests: []string{"a"}},
	} {
		if _, _, ok := SplitFailedTests(pre, cur); ok {
			t.Errorf("%s: split made", name)
		}
	}
	if _, _, ok := SplitFailedTests(nil, pre); ok {
		t.Error("split made without a pre-run test")
	}
	if _, _, ok := SplitFailedTests(&TestResult{FailedTests: []string{"a"}}, &TestResult{FailedTests: []string{"a"}}); ok {
		t.Error("split made without a format")
	}
}

func TestReviewResult_Decide(t *testing.T) {
	tests := []struct {
		name   string
		issues []ReviewIssue
		want   ReviewStatus
	}{
		{name: "no issues passes", issues: nil, want: ReviewPass},
		{name: "critical issue fails", issues: []ReviewIssue{{Severity: SeverityCritical}}, want: ReviewFail},
		{name: "high issue fails", issues: []ReviewIssue{{Severity: SeverityHigh}}, want: ReviewFail},
		{name: "medium issue passes with warning", issues: []ReviewIssue{{Severity: SeverityMedium}}, want: ReviewPass},
		{name: "low issue passes with warning", issues: []ReviewIssue{{Severity: SeverityLow}}, want: ReviewPass},
		{
			name:   "medium and low together still pass",
			issues: []ReviewIssue{{Severity: SeverityMedium}, {Severity: SeverityLow}},
			want:   ReviewPass,
		},
		{
			name:   "low plus critical fails",
			issues: []ReviewIssue{{Severity: SeverityLow}, {Severity: SeverityCritical}},
			want:   ReviewFail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ReviewResult{Issues: tt.issues}
			if got := r.Decide(); got != tt.want {
				t.Errorf("Decide() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestReviewResult_Decide_Criteria(t *testing.T) {
	met := func(index int, ok bool) CriterionVerdict {
		return CriterionVerdict{Index: index, Met: ok, Evidence: "e"}
	}
	unmet := ReviewResult{Status: ReviewPass, Criteria: []CriterionVerdict{met(1, true), met(2, false), met(3, true)}}
	if got := unmet.Decide(); got != ReviewFail {
		t.Errorf("an unmet criterion and no issues: Decide() = %s, want fail", got)
	}
	if got := unmet.UnmetCriteria(); len(got) != 1 || got[0].Index != 2 {
		t.Errorf("UnmetCriteria() = %+v, want only index 2", got)
	}
	allMet := ReviewResult{Status: ReviewFail, Issues: []ReviewIssue{{Severity: SeverityLow}},
		Criteria: []CriterionVerdict{met(1, true), met(2, true), met(3, true)}}
	if got := allMet.Decide(); got != ReviewPass {
		t.Errorf("all criteria met and a low issue: Decide() = %s, want pass", got)
	}
	if got := allMet.UnmetCriteria(); got != nil {
		t.Errorf("UnmetCriteria() = %+v, want none", got)
	}
}

func TestReviewResult_Validate_Criteria(t *testing.T) {
	v := func(index int, evidence string) CriterionVerdict {
		return CriterionVerdict{Index: index, Met: true, Evidence: evidence}
	}
	tests := []struct {
		name     string
		criteria []CriterionVerdict
		wantErr  bool
	}{
		{"none", nil, false},
		{"valid, any order", []CriterionVerdict{v(3, "c"), v(1, "a"), v(2, "b")}, false},
		{"index zero", []CriterionVerdict{v(0, "a"), v(1, "b")}, true},
		{"index past the end", []CriterionVerdict{v(1, "a"), v(3, "b")}, true},
		{"duplicate", []CriterionVerdict{v(1, "a"), v(1, "b")}, true},
		{"empty evidence", []CriterionVerdict{v(1, "")}, true},
		{"blank evidence", []CriterionVerdict{v(1, "  \n")}, true},
		{"500 bytes", []CriterionVerdict{v(1, strings.Repeat("x", MaxCriterionEvidenceBytes))}, false},
		{"501 bytes", []CriterionVerdict{v(1, strings.Repeat("x", MaxCriterionEvidenceBytes+1))}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ReviewResult{Status: ReviewPass, Criteria: tt.criteria}
			if err := r.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate() = %v, want error: %v", err, tt.wantErr)
			}
			if err := r.ValidateCriteria(); (err != nil) != tt.wantErr {
				t.Errorf("ValidateCriteria() = %v, want error: %v", err, tt.wantErr)
			}
		})
	}
}

func TestReviewResult_Validate(t *testing.T) {
	valid := ReviewResult{Status: ReviewPass, Issues: []ReviewIssue{{Severity: SeverityMedium}}}
	if err := valid.Validate(); err != nil {
		t.Errorf("expected valid review result to pass, got %v", err)
	}

	badStatus := ReviewResult{Status: "unsure"}
	if err := badStatus.Validate(); err == nil {
		t.Error("expected error for unsupported status")
	}

	badSeverity := ReviewResult{Status: ReviewFail, Issues: []ReviewIssue{{Severity: "extreme"}}}
	if err := badSeverity.Validate(); err == nil {
		t.Error("expected error for unsupported severity")
	}
}
