package persistence

import (
	"encoding/json"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func TestValidateRun_TreeAfter(t *testing.T) {
	run := func(action domain.ActionType, tree string) *domain.Run {
		return &domain.Run{ID: "r", Status: domain.RunFailed, Executions: []domain.StepExecution{
			{Index: 0, Action: action, Status: domain.StepCompleted, TreeAfter: tree},
		}}
	}
	for _, tc := range []struct {
		name   string
		action domain.ActionType
		tree   string
		want   string // an error containing it, or empty for a valid run
	}{
		{"sha-1 tree", domain.ActionImplement, strings.Repeat("a1", 20), ""},
		{"sha-256 tree", domain.ActionImplement, strings.Repeat("0f", 32), ""},
		{"none, as in a run written before the field", domain.ActionImplement, "", ""},
		{"none on a test", domain.ActionTest, "", ""},
		{"on a test step", domain.ActionTest, strings.Repeat("a", 40), "tree_after recorded on a test step"},
		{"on a review step", domain.ActionReview, strings.Repeat("a", 40), "tree_after recorded on a review step"},
		{"too short", domain.ActionImplement, strings.Repeat("a", 39), "not a git tree id"},
		{"between the two lengths", domain.ActionImplement, strings.Repeat("a", 41), "not a git tree id"},
		{"upper case", domain.ActionImplement, strings.Repeat("A", 40), "not a git tree id"},
		{"not hex", domain.ActionImplement, strings.Repeat("g", 40), "not a git tree id"},
		{"a revision, not an id", domain.ActionImplement, "HEAD^{tree}" + strings.Repeat("a", 29), "not a git tree id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRun(run(tc.action, tc.tree), "r")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateRun() = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateRun() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// A run.json written before tree_after existed decodes with none, and the field is left out
// of the JSON when empty.
func TestStepExecution_TreeAfterIsOptional(t *testing.T) {
	var exec domain.StepExecution
	if err := json.Unmarshal([]byte(`{"index":0,"action":"implement","status":"completed"}`), &exec); err != nil {
		t.Fatal(err)
	}
	if exec.TreeAfter != "" {
		t.Errorf("tree_after = %q", exec.TreeAfter)
	}
	data, err := json.Marshal(exec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tree_after") {
		t.Errorf("an empty tree_after was written: %s", data)
	}
	exec.TreeAfter = strings.Repeat("b", 40)
	if data, _ = json.Marshal(exec); !strings.Contains(string(data), `"tree_after":"`+exec.TreeAfter+`"`) {
		t.Errorf("tree_after missing from %s", data)
	}
}
