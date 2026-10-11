package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReviewResult_ValidateReviewers(t *testing.T) {
	ok := ReviewerAnswer{Model: "opus", Result: ReviewResult{Status: ReviewPass}}
	nested := ReviewerAnswer{Model: "opus", Result: ReviewResult{Status: ReviewPass, Reviewers: []ReviewerAnswer{ok}}}
	for name, tc := range map[string]struct {
		reviewers []ReviewerAnswer
		want      string
	}{
		"none":             {nil, ""},
		"two":              {[]ReviewerAnswer{{Model: "sonnet", Result: ReviewResult{Status: ReviewFail}}, ok}, ""},
		"no model":         {[]ReviewerAnswer{{Result: ReviewResult{Status: ReviewPass}}}, "reviewers[0].model"},
		"invalid result":   {[]ReviewerAnswer{{Model: "opus", Result: ReviewResult{Status: "maybe"}}}, "reviewers[0]"},
		"nested reviewers": {[]ReviewerAnswer{nested}, "has reviewers of its own"},
		"too many":         {make([]ReviewerAnswer, MaxReviewers+1), "at most"},
	} {
		t.Run(name, func(t *testing.T) {
			err := ReviewResult{Status: ReviewPass, Reviewers: tc.reviewers}.Validate()
			if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("Validate = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReviewResult_ReviewersJSON(t *testing.T) {
	plain, err := json.Marshal(ReviewResult{Status: ReviewPass})
	if err != nil || strings.Contains(string(plain), "reviewers") {
		t.Errorf("a single review writes no reviewers: %s %v", plain, err)
	}
	merged := ReviewResult{Status: ReviewPass, Reviewers: []ReviewerAnswer{{Model: "sonnet", Result: ReviewResult{Status: ReviewPass}}}}
	data, err := json.Marshal(merged)
	if err != nil || !strings.Contains(string(data), `"reviewers":[{"model":"sonnet","result":{`) {
		t.Fatalf("json = %s %v", data, err)
	}
}

func TestCallReport_ExtraCalls(t *testing.T) {
	good := CallReport{ExtraCalls: []ExtraCall{{Model: "opus", APIMS: 10, InputTokens: 1, OutputTokens: 2}}}
	if err := good.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	for name, c := range map[string]CallReport{
		"no model":  {ExtraCalls: []ExtraCall{{}}},
		"negative":  {ExtraCalls: []ExtraCall{{Model: "opus", OutputTokens: -1}}},
		"too many":  {ExtraCalls: make([]ExtraCall, maxReportNames+1)},
		"api time":  {ExtraCalls: []ExtraCall{{Model: "opus", APIMS: -5}}},
		"bad model": {ExtraCalls: []ExtraCall{{Model: "op\x00us"}}},
	} {
		if c.Validate() == nil {
			t.Errorf("%s: Validate accepted %+v", name, c)
		}
	}
}

func TestComputeOutcome_CountsExtraReviewerCalls(t *testing.T) {
	run := Run{Executions: []StepExecution{{
		Action: ActionReview, Status: StepCompleted,
		Usage: &Usage{InputTokens: 5},
		Call:  &CallReport{ExtraCalls: []ExtraCall{{Model: "opus"}, {Model: "haiku"}}},
	}}}
	if got := ComputeOutcome(run, nil).Usage.Calls; got != 3 {
		t.Errorf("calls = %d, want 3: the base reviewer and two extra ones", got)
	}
}
