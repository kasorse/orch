package persistence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

func goodMatch() domain.PathRuleMatch {
	return domain.PathRuleMatch{
		Match:         "migrations/**",
		Files:         []string{"migrations/a.sql"},
		RequireStages: []domain.Stage{domain.StageReview, domain.StageTesting},
		Missing:       []domain.Stage{domain.StageTesting},
		Approval:      domain.PathApprovalManual,
	}
}

// The rules a run starts with, and the matches of an implement, survive a save and a load.
func TestLoadRun_PathRulesRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "x"})
	if err != nil {
		t.Fatal(err)
	}
	run.Options.PathRules = []domain.PathRule{
		{Match: "migrations/**", RequireStages: []domain.Stage{domain.StageReview, domain.StageTesting}, Approval: "manual"},
		{Match: "**/*.sql", Approval: "manual"},
	}
	run.Executions = []domain.StepExecution{{Index: 0, Action: domain.ActionImplement, Status: domain.StepFailed,
		Error: "policy.paths", PathRules: []domain.PathRuleMatch{goodMatch()}}}
	run.Status = domain.RunFailed
	if err := store.SaveRun(run); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRun(run.ID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if !reflect.DeepEqual(loaded.Options.PathRules, run.Options.PathRules) ||
		!reflect.DeepEqual(loaded.Executions[0].PathRules, run.Executions[0].PathRules) {
		t.Errorf("options %+v, path rules %+v", loaded.Options, loaded.Executions[0].PathRules)
	}
}

// A run.json written before path rules has neither field and loads as it did; a run without
// rules writes neither.
func TestLoadRun_WithoutPathRules(t *testing.T) {
	store := NewStore(t.TempDir())
	id := "0f8fad5b-d9cb-469f-a165-70867728950e"
	dir := store.runDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "id": "` + id + `",
  "task": {"description": "fix the bug", "created_at": "2026-09-01T10:00:00Z"},
  "options": {"review_fix_medium": true},
  "status": "completed",
  "executions": [{"index": 0, "action": "implement", "status": "completed",
    "started_at": "2026-09-01T10:00:00Z", "finished_at": "2026-09-01T10:01:00Z"}],
  "created_at": "2026-09-01T10:00:00Z",
  "updated_at": "2026-09-01T10:05:00Z"
}`
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	run, err := store.LoadRun(id)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if run.Options.PathRules != nil || run.Executions[0].PathRules != nil || !run.Options.ReviewFixMedium {
		t.Errorf("options %+v, path rules %+v", run.Options, run.Executions[0].PathRules)
	}

	data, err := json.Marshal(domain.Run{Options: domain.RunOptions{}})
	if err != nil || strings.Contains(string(data), "options") || strings.Contains(string(data), "path_rules") {
		t.Errorf("a run without rules wrote %s (%v)", data, err)
	}
}

func TestValidateRun_PathRules(t *testing.T) {
	run := func(action domain.ActionType, ms ...domain.PathRuleMatch) *domain.Run {
		return &domain.Run{ID: "r", Status: domain.RunFailed, Executions: []domain.StepExecution{
			{Index: 0, Action: action, Status: domain.StepFailed, PathRules: ms},
		}}
	}
	if err := validateRun(run(domain.ActionImplement, goodMatch()), "r"); err != nil {
		t.Fatalf("a valid match: %v", err)
	}

	with := func(edit func(m *domain.PathRuleMatch)) domain.PathRuleMatch {
		m := goodMatch()
		edit(&m)
		return m
	}
	tooMany := make([]string, domain.MaxDiffPaths+1)
	for i := range tooMany {
		tooMany[i] = "migrations/" + strings.Repeat("x", i+1) + ".sql"
	}
	for _, tc := range []struct {
		name string
		run  *domain.Run
		want string
	}{
		{"on a test step", run(domain.ActionTest, goodMatch()), "path_rules recorded on a test step"},
		{"unknown stage", run(domain.ActionImplement, with(func(m *domain.PathRuleMatch) {
			m.RequireStages = []domain.Stage{"qa"}
			m.Missing = nil
		})), "executions[0]: path_rules[0]: require_stages"},
		{"missing not in require_stages", run(domain.ActionImplement, with(func(m *domain.PathRuleMatch) {
			m.Missing = []domain.Stage{domain.StageResearch}
		})), "path_rules[0]: missing"},
		{"too many files", run(domain.ActionImplement, with(func(m *domain.PathRuleMatch) { m.Files = tooMany })),
			"path_rules[0]: files: 51 paths"},
		{"path outside the repository", run(domain.ActionImplement, with(func(m *domain.PathRuleMatch) {
			m.Files = []string{"../etc/passwd"}
		})), "path_rules[0]: files"},
		{"bad pattern", run(domain.ActionImplement, with(func(m *domain.PathRuleMatch) { m.Match = "//abs" })), "path_rules[0]: match"},
		{"bad approval", run(domain.ActionImplement, with(func(m *domain.PathRuleMatch) { m.Approval = "auto" })), "path_rules[0]: approval"},
		{"empty rule in options", &domain.Run{ID: "r", Status: domain.RunFailed,
			Options: domain.RunOptions{PathRules: []domain.PathRule{{Match: "a/**"}}}}, "options.path_rules[0]"},
		{"unknown stage in options", &domain.Run{ID: "r", Status: domain.RunFailed,
			Options: domain.RunOptions{PathRules: []domain.PathRule{{Match: "a/**", RequireStages: []domain.Stage{"qa"}}}}},
			"options.path_rules[0]: require_stages"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRun(tc.run, "r")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
