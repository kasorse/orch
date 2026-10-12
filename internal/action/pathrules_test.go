package action

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

var migrationsNeedTesting = domain.PathRule{Match: "migrations/**", RequireStages: []domain.Stage{domain.StageTesting}}

func pathRun(head string, actions []domain.ActionType, rules ...domain.PathRule) *domain.Run {
	wf := domain.Workflow{}
	for _, a := range actions {
		wf.Steps = append(wf.Steps, domain.Step{Action: a})
	}
	return &domain.Run{ID: "run-1", Task: domain.Task{Description: "t"}, PreRunRef: head, Workflow: &wf,
		Options: domain.RunOptions{PathRules: rules}}
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))
}

// A rule needs a stage the workflow lacks: the step fails with the message and records the
// match, with the missing stage.
func TestExecuteImplement_PathRuleMissingStage(t *testing.T) {
	repo, dir := guardRepo(t)
	d := newDispatcher(&editingClient{edit: func() { put(t, dir, "migrations/a.sql", "select 1;\n") }, output: `{"summary":"s"}`})
	d.Repo = repo
	run := pathRun(headOf(t, dir), []domain.ActionType{domain.ActionImplement, domain.ActionReview}, migrationsNeedTesting)

	outcome, err := d.Execute(context.Background(), domain.ActionImplement, run)
	want := `policy.paths "migrations/**" requires testing, which this workflow does not run (changed: migrations/a.sql)`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
	wantMatch := []domain.PathRuleMatch{{Match: "migrations/**", Files: []string{"migrations/a.sql"},
		RequireStages: []domain.Stage{domain.StageTesting}, Missing: []domain.Stage{domain.StageTesting}}}
	if !reflect.DeepEqual(outcome.PathRules, wantMatch) {
		t.Errorf("path rules = %+v, want %+v", outcome.PathRules, wantMatch)
	}
}

func TestExecuteImplement_PathRuleStagePresent(t *testing.T) {
	repo, dir := guardRepo(t)
	d := newDispatcher(&editingClient{edit: func() { put(t, dir, "migrations/a.sql", "select 1;\n") }, output: `{"summary":"s"}`})
	d.Repo = repo
	run := pathRun(headOf(t, dir), []domain.ActionType{domain.ActionImplement, domain.ActionTest}, migrationsNeedTesting)

	outcome, err := d.Execute(context.Background(), domain.ActionImplement, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantMatch := []domain.PathRuleMatch{{Match: "migrations/**", Files: []string{"migrations/a.sql"},
		RequireStages: []domain.Stage{domain.StageTesting}, Missing: []domain.Stage{}}}
	if !reflect.DeepEqual(outcome.PathRules, wantMatch) {
		t.Errorf("path rules = %+v, want %+v", outcome.PathRules, wantMatch)
	}
}

// A change that matches no rule records nothing; a run without rules records nothing either.
func TestExecuteImplement_PathRuleNoMatch(t *testing.T) {
	repo, dir := guardRepo(t)
	d := newDispatcher(&editingClient{edit: func() { put(t, dir, "src/a.go", "package src\n") }, output: `{"summary":"s"}`})
	d.Repo = repo
	run := pathRun(headOf(t, dir), []domain.ActionType{domain.ActionImplement}, migrationsNeedTesting)
	outcome, err := d.Execute(context.Background(), domain.ActionImplement, run)
	if err != nil || outcome.PathRules != nil {
		t.Errorf("no match: error %v, path rules %+v", err, outcome.PathRules)
	}
}

// The check reads the run's net diff: a file one attempt changed and a later attempt
// reverted is not in it.
func TestExecuteImplement_PathRuleNetDiff(t *testing.T) {
	repo, dir := guardRepo(t)
	attempt := 0
	d := newDispatcher(&editingClient{edit: func() {
		attempt++
		if attempt == 1 {
			put(t, dir, "migrations/a.sql", "select 1;\n")
			return
		}
		if err := os.Remove(filepath.Join(dir, "migrations", "a.sql")); err != nil {
			t.Fatal(err)
		}
	}, output: `{"summary":"s"}`})
	d.Repo = repo
	run := pathRun(headOf(t, dir), []domain.ActionType{domain.ActionImplement, domain.ActionReview}, migrationsNeedTesting)

	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err == nil {
		t.Fatal("the first attempt touches migrations/ without testing in the workflow, and must fail")
	}
	outcome, err := d.Execute(context.Background(), domain.ActionImplement, run)
	if err != nil || outcome.PathRules != nil {
		t.Errorf("after the revert: error %v, path rules %+v", err, outcome.PathRules)
	}
}

// Without a restore point the step's own diff is what is checked.
func TestExecuteImplement_PathRuleWithoutRestorePoint(t *testing.T) {
	repo, dir := guardRepo(t)
	d := newDispatcher(&editingClient{edit: func() { put(t, dir, "migrations/a.sql", "select 1;\n") }, output: `{"summary":"s"}`})
	d.Repo = repo
	run := pathRun("", []domain.ActionType{domain.ActionImplement}, migrationsNeedTesting)
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err == nil ||
		!strings.Contains(err.Error(), "requires testing") {
		t.Errorf("error = %v, want the path rule failure", err)
	}
}

// Rules that cannot be checked because git gives no file list fail the step.
func TestExecuteImplement_PathRuleFailsClosed(t *testing.T) {
	d := newDispatcher(&editingClient{edit: func() {}, output: `{"summary":"s"}`})
	run := pathRun("", []domain.ActionType{domain.ActionImplement, domain.ActionTest}, migrationsNeedTesting)
	_, err := d.Execute(context.Background(), domain.ActionImplement, run)
	if err == nil || !strings.Contains(err.Error(), "policy.paths cannot be checked: git could not list the changed files") {
		t.Errorf("error = %v, want the cannot-be-checked failure", err)
	}

	// With no rules the same step passes: a diff stays a best-effort record.
	d = newDispatcher(&editingClient{edit: func() {}, output: `{"summary":"s"}`})
	if _, err := d.Execute(context.Background(), domain.ActionImplement, pathRun("", nil)); err != nil {
		t.Errorf("without rules: %v", err)
	}
}
