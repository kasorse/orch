package persistence

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

func implementWithFiles(files []domain.FileStat) domain.StepExecution {
	return domain.StepExecution{Index: 0, Action: domain.ActionImplement, Status: domain.StepCompleted,
		Diff: &domain.StepDiff{Step: domain.DiffStat{Files: len(files)}, Run: &domain.DiffStat{Files: len(files), Insertions: 3, Deletions: 1, Binary: 1}, RunFiles: files}}
}

// run.json keeps the files of a run's net change and reads them back.
func TestLoadRun_RunFilesRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	files := []domain.FileStat{{Path: "calc/add.go", Insertions: 3, Deletions: 1}, {Path: "logo.png", Binary: true}}
	run.Executions = []domain.StepExecution{implementWithFiles(files)}
	if err := store.SaveRun(run); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRun(run.ID)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if got := loaded.Executions[0].Diff.RunFiles; !reflect.DeepEqual(got, files) {
		t.Errorf("run_files = %+v, want %+v", got, files)
	}
	data, err := os.ReadFile(store.runDir(run.ID) + "/run.json")
	if err != nil || !strings.Contains(string(data), `"run_files"`) {
		t.Errorf("run.json lacks run_files: %v", err)
	}
}

func TestValidateRun_RunFiles(t *testing.T) {
	many := make([]domain.FileStat, domain.MaxRunFiles+1)
	for i := range many {
		many[i] = domain.FileStat{Path: "f.go"}
	}
	good := []domain.FileStat{{Path: "a/b.go", Insertions: 1}, {Path: "x.bin", Binary: true}}
	if err := validateRun(&domain.Run{ID: "r", Status: domain.RunFailed, Executions: []domain.StepExecution{implementWithFiles(good)}}, "r"); err != nil {
		t.Fatalf("valid run_files: %v", err)
	}
	for _, tc := range []struct {
		name  string
		files []domain.FileStat
	}{
		{"parent path", []domain.FileStat{{Path: "../etc/passwd"}}},
		{"absolute path", []domain.FileStat{{Path: "/etc/passwd"}}},
		{"unclean path", []domain.FileStat{{Path: "a//b.go"}}},
		{"empty path", []domain.FileStat{{}}},
		{"control character", []domain.FileStat{{Path: "a\x1b.go"}}},
		{"negative insertions", []domain.FileStat{{Path: "a.go", Insertions: -1}}},
		{"negative deletions", []domain.FileStat{{Path: "a.go", Deletions: -1}}},
		{"binary with counts", []domain.FileStat{{Path: "a.bin", Binary: true, Insertions: 1}}},
		{"too many", many},
	} {
		run := &domain.Run{ID: "r", Status: domain.RunFailed, Executions: []domain.StepExecution{implementWithFiles(tc.files)}}
		if err := validateRun(run, "r"); err == nil || !strings.Contains(err.Error(), "run_files") {
			t.Errorf("%s: validateRun() = %v, want a run_files error", tc.name, err)
		}
	}

	noRun := implementWithFiles(good)
	noRun.Diff.Run = nil
	if err := validateRun(&domain.Run{ID: "r", Status: domain.RunFailed, Executions: []domain.StepExecution{noRun}}, "r"); err == nil {
		t.Error("run_files without run must be refused")
	}
}

func TestStore_SaveReport(t *testing.T) {
	store := NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveReport(run.ID, "## report\n"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(store.ReportPath(run.ID)); err != nil || string(data) != "## report\n" {
		t.Errorf("report.md = %q, %v", data, err)
	}
	if err := store.SaveReport("../x", "boom"); err == nil {
		t.Error("a report for something that is not a run ID must be refused")
	}
}
