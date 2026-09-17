package persistence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/internal/domain"
)

func TestNewRun_CreatesDirectoryStructureAndRunJSON(t *testing.T) {
	store := NewStore(t.TempDir())

	run, err := store.NewRun(domain.Task{Description: "fix the bug"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.ID == "" {
		t.Fatal("expected a non-empty run ID")
	}
	if run.Status != domain.RunPending {
		t.Errorf("expected pending status, got %s", run.Status)
	}

	dir := store.runDir(run.ID)
	for _, sub := range []string{"artifacts", "logs"} {
		info, err := os.Stat(filepath.Join(dir, sub))
		if err != nil {
			t.Fatalf("expected %s to exist: %v", sub, err)
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", sub)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		t.Fatalf("expected run.json to exist: %v", err)
	}
	var saved domain.Run
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("run.json is not valid JSON: %v", err)
	}
	if saved.ID != run.ID || saved.Task.Description != "fix the bug" {
		t.Errorf("run.json contents do not match: %+v", saved)
	}
}

func TestNewRun_UniqueIDs(t *testing.T) {
	store := NewStore(t.TempDir())

	run1, err := store.NewRun(domain.Task{Description: "task 1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	run2, err := store.NewRun(domain.Task{Description: "task 2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run1.ID == run2.ID {
		t.Error("expected distinct run IDs")
	}
}

func TestSaveRun_WritesClassificationAndWorkflowOnceSet(t *testing.T) {
	store := NewStore(t.TempDir())

	run, err := store.NewRun(domain.Task{Description: "add feature"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dir := store.runDir(run.ID)
	assertMissing := func(name string) {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("expected %s not to exist yet", name)
		}
	}
	assertMissing("classification.json")
	assertMissing("workflow.json")

	run.Classification = &domain.Classification{Type: domain.TaskFeature, Complexity: domain.ComplexityLow}
	run.Workflow = &domain.Workflow{Steps: []domain.Step{{Action: domain.ActionPlan}, {Action: domain.ActionImplement}}}

	if err := store.SaveRun(run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var classification domain.Classification
	data, err := os.ReadFile(filepath.Join(dir, "classification.json"))
	if err != nil {
		t.Fatalf("expected classification.json to exist: %v", err)
	}
	if err := json.Unmarshal(data, &classification); err != nil {
		t.Fatalf("classification.json is not valid JSON: %v", err)
	}
	if classification.Type != domain.TaskFeature {
		t.Errorf("expected type feature, got %s", classification.Type)
	}

	var workflow domain.Workflow
	data, err = os.ReadFile(filepath.Join(dir, "workflow.json"))
	if err != nil {
		t.Fatalf("expected workflow.json to exist: %v", err)
	}
	if err := json.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("workflow.json is not valid JSON: %v", err)
	}
	if len(workflow.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(workflow.Steps))
	}
}

func TestSaveArtifact_WritesFileAndReturnsRelativePath(t *testing.T) {
	store := NewStore(t.TempDir())

	run, err := store.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	artifact := domain.AnalysisArtifact{Summary: "root cause found"}
	relPath, err := store.SaveArtifact(run.ID, 1, domain.ActionAnalyze, artifact)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantRel := filepath.Join("artifacts", "001-analyze.json")
	if relPath != wantRel {
		t.Errorf("expected relative path %q, got %q", wantRel, relPath)
	}

	data, err := os.ReadFile(filepath.Join(store.runDir(run.ID), relPath))
	if err != nil {
		t.Fatalf("expected artifact file to exist: %v", err)
	}
	var saved domain.AnalysisArtifact
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("artifact file is not valid JSON: %v", err)
	}
	if saved.Summary != "root cause found" {
		t.Errorf("expected summary to round-trip, got %q", saved.Summary)
	}
}

func TestAppendLog_CreatesAndAppendsAcrossCalls(t *testing.T) {
	store := NewStore(t.TempDir())

	run, err := store.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := store.AppendLog(run.ID, 2, domain.ActionImplement, "stdout", "line one\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := store.AppendLog(run.ID, 2, domain.ActionImplement, "stdout", "line two\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	relPath := filepath.Join("logs", "002-implement.stdout.log")
	data, err := os.ReadFile(filepath.Join(store.runDir(run.ID), relPath))
	if err != nil {
		t.Fatalf("expected log file to exist: %v", err)
	}
	want := "line one\nline two\n"
	if string(data) != want {
		t.Errorf("expected log contents %q, got %q", want, string(data))
	}
}

func TestAppendClassifierLog_CreatesAndAppendsAcrossCalls_UnindexedPath(t *testing.T) {
	store := NewStore(t.TempDir())

	run, err := store.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := store.AppendClassifierLog(run.ID, "stdout", "chunk one\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := store.AppendClassifierLog(run.ID, "stdout", "chunk two\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	relPath := filepath.Join("logs", "classify.stdout.log")
	data, err := os.ReadFile(filepath.Join(store.runDir(run.ID), relPath))
	if err != nil {
		t.Fatalf("expected log file to exist: %v", err)
	}
	want := "chunk one\nchunk two\n"
	if string(data) != want {
		t.Errorf("expected log contents %q, got %q", want, string(data))
	}
}
