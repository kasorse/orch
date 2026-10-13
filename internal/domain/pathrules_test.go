package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func wfOf(actions ...ActionType) Workflow {
	var wf Workflow
	for _, a := range actions {
		wf.Steps = append(wf.Steps, Step{Action: a})
	}
	return wf
}

func TestMatchPathRules(t *testing.T) {
	migrations := PathRule{Match: "migrations/**", RequireStages: []Stage{StageReview, StageTesting}, Approval: PathApprovalManual}
	sql := PathRule{Match: "**/*.sql", Approval: PathApprovalManual}
	docs := PathRule{Match: "docs/**", RequireStages: []Stage{StageReview}}
	full := wfOf(ActionImplement, ActionTest, ActionReview)

	many := make([]string, MaxDiffPaths+7)
	for i := range many {
		many[i] = fmt.Sprintf("migrations/%03d.sql", i)
	}

	for _, tc := range []struct {
		name  string
		rules []PathRule
		files []string
		wf    Workflow
		want  []PathRuleMatch
	}{
		{"no rules", nil, []string{"a.go"}, full, nil},
		{"no match", []PathRule{migrations}, []string{"main.go", "docs/a.md"}, full, nil},
		{"no files", []PathRule{migrations}, nil, full, nil},
		{
			"one match, every stage present",
			[]PathRule{migrations, docs}, []string{"main.go", "migrations/a.sql", "migrations/b.sql"}, full,
			[]PathRuleMatch{{Match: "migrations/**", Files: []string{"migrations/a.sql", "migrations/b.sql"},
				RequireStages: []Stage{StageReview, StageTesting}, Missing: []Stage{}, Approval: PathApprovalManual}},
		},
		{
			"a stage missing",
			[]PathRule{migrations}, []string{"migrations/a.sql"}, wfOf(ActionImplement, ActionReview),
			[]PathRuleMatch{{Match: "migrations/**", Files: []string{"migrations/a.sql"},
				RequireStages: []Stage{StageReview, StageTesting}, Missing: []Stage{StageTesting}, Approval: PathApprovalManual}},
		},
		{
			"missing keeps the rule's order",
			[]PathRule{migrations}, []string{"migrations/a.sql"}, wfOf(ActionImplement),
			[]PathRuleMatch{{Match: "migrations/**", Files: []string{"migrations/a.sql"},
				RequireStages: []Stage{StageReview, StageTesting}, Missing: []Stage{StageReview, StageTesting}, Approval: PathApprovalManual}},
		},
		{
			"two rules on one file, in file order",
			[]PathRule{sql, migrations}, []string{"migrations/a.sql"}, wfOf(ActionImplement, ActionTest),
			[]PathRuleMatch{
				{Match: "**/*.sql", Files: []string{"migrations/a.sql"}, Missing: []Stage{}, Approval: PathApprovalManual},
				{Match: "migrations/**", Files: []string{"migrations/a.sql"},
					RequireStages: []Stage{StageReview, StageTesting}, Missing: []Stage{StageReview}, Approval: PathApprovalManual},
			},
		},
		{
			"files cut at MaxDiffPaths, in git's order",
			[]PathRule{migrations}, many, full,
			[]PathRuleMatch{{Match: "migrations/**", Files: many[:MaxDiffPaths],
				RequireStages: []Stage{StageReview, StageTesting}, Missing: []Stage{}, Approval: PathApprovalManual}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchPathRules(tc.rules, tc.files, tc.wf)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("MatchPathRules =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

// A stage counts through its action, not through the stage name a step carries: a workflow
// whose steps name no stage still runs testing.
func TestMissingStages_GoesThroughTheAction(t *testing.T) {
	rule := PathRule{Match: "a/**", RequireStages: []Stage{StageTesting, StageResearch, "bogus"}}
	got := MissingStages(rule, wfOf(ActionImplement, ActionTest))
	if want := []Stage{StageResearch, "bogus"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MissingStages = %v, want %v", got, want)
	}
	if got := MissingStages(PathRule{Match: "a/**", Approval: "manual"}, Workflow{}); got == nil || len(got) != 0 {
		t.Errorf("no required stage: %#v, want an empty non-nil slice", got)
	}
}

func TestPathRuleFailure(t *testing.T) {
	m := PathRuleMatch{Match: "migrations/**", Files: []string{"migrations/a.sql", "migrations/b.sql"},
		RequireStages: []Stage{StageReview, StageTesting}, Missing: []Stage{StageTesting}}
	want := `policy.paths "migrations/**" requires testing, which this workflow does not run (changed: migrations/a.sql, migrations/b.sql)`
	if got := PathRuleFailure(m); got != want {
		t.Errorf("PathRuleFailure =\n%s\nwant\n%s", got, want)
	}
}

// Missing is written as [] and never as null.
func TestPathRuleMatch_JSON(t *testing.T) {
	got := MatchPathRules([]PathRule{{Match: "a/**", Approval: "manual"}}, []string{"a/b.go"}, Workflow{})
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"match":"a/**","files":["a/b.go"],"missing":[],"approval":"manual"}]`
	if string(data) != want {
		t.Errorf("json = %s, want %s", data, want)
	}
}

func TestPathRule_Validate(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule PathRule
		want string // "" is valid
	}{
		{"stages and approval", PathRule{Match: "a/**", RequireStages: []Stage{StageReview}, Approval: "manual"}, ""},
		{"approval only", PathRule{Match: "**/*.sql", Approval: "manual"}, ""},
		{"empty", PathRule{Match: "a/**"}, "needs require_stages"},
		{"unknown stage", PathRule{Match: "a/**", RequireStages: []Stage{"qa"}}, "unsupported stage"},
		{"duplicate stage", PathRule{Match: "a/**", RequireStages: []Stage{StageReview, StageReview}}, "duplicate stage"},
		{"auto approval", PathRule{Match: "a/**", Approval: "auto"}, `approval "auto"`},
		{"absolute pattern", PathRule{Match: "/a", Approval: "manual"}, "relative to the repository root"},
		{"bad glob", PathRule{Match: "[", Approval: "manual"}, "match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.rule.Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
