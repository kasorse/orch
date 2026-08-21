package domain

import "testing"

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
