package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReviewResult_MediumIssues(t *testing.T) {
	r := ReviewResult{Issues: []ReviewIssue{
		{Severity: SeverityLow, File: "a.go"},
		{Severity: SeverityMedium, File: "b.go"},
		{Severity: SeverityHigh, File: "c.go"},
		{Severity: SeverityMedium, File: "d.go"},
	}}
	got := r.MediumIssues()
	if len(got) != 2 || got[0].File != "b.go" || got[1].File != "d.go" {
		t.Errorf("MediumIssues() = %+v, want b.go and d.go", got)
	}
	if got := (ReviewResult{Issues: []ReviewIssue{{Severity: SeverityLow}}}).MediumIssues(); got != nil {
		t.Errorf("MediumIssues() = %+v, want none", got)
	}
}

// The options are written only when one is on, and read back as written.
func TestRun_OptionsJSON(t *testing.T) {
	off, err := json.Marshal(Run{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(off), `"options"`) {
		t.Errorf("a run with no option on wrote options: %s", off)
	}

	on, err := json.Marshal(Run{Options: RunOptions{ReviewFixMedium: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(on), `"options":{"review_fix_medium":true}`) {
		t.Errorf("options missing from %s", on)
	}
	var back Run
	if err := json.Unmarshal(on, &back); err != nil || !back.Options.ReviewFixMedium {
		t.Errorf("read back %+v, %v", back.Options, err)
	}
}
