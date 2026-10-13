package app

import (
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/persistence"
	"orchestrator/internal/report"
)

func TestDecisionsCalibrateArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		dir     string
		minRuns int
		wantErr bool
	}{
		{"defaults", []string{"calibrate"}, runsBaseDir, 10, false},
		{"runs separate", []string{"calibrate", "--runs", "bench/runs"}, "bench/runs", 10, false},
		{"runs equals", []string{"calibrate", "--runs=bench/runs"}, "bench/runs", 10, false},
		{"min-runs separate", []string{"calibrate", "--min-runs", "3"}, runsBaseDir, 3, false},
		{"min-runs equals", []string{"calibrate", "--min-runs=25"}, runsBaseDir, 25, false},
		{"both", []string{"calibrate", "--min-runs", "1", "--runs=r"}, "r", 1, false},
		{"no subcommand", nil, "", 0, true},
		{"report is another command", []string{"report"}, "", 0, true},
		{"unknown subcommand", []string{"list"}, "", 0, true},
		{"unknown flag", []string{"calibrate", "--json"}, "", 0, true},
		{"the runs flag of runs report", []string{"calibrate", "--dir", "a"}, "", 0, true},
		{"runs twice", []string{"calibrate", "--runs", "a", "--runs=b"}, "", 0, true},
		{"min-runs twice", []string{"calibrate", "--min-runs", "2", "--min-runs", "3"}, "", 0, true},
		{"runs without value", []string{"calibrate", "--runs"}, "", 0, true},
		{"runs empty", []string{"calibrate", "--runs="}, "", 0, true},
		{"min-runs without value", []string{"calibrate", "--min-runs"}, "", 0, true},
		{"min-runs zero", []string{"calibrate", "--min-runs", "0"}, "", 0, true},
		{"min-runs negative", []string{"calibrate", "--min-runs=-2"}, "", 0, true},
		{"min-runs not a number", []string{"calibrate", "--min-runs", "many"}, "", 0, true},
		{"extra argument", []string{"calibrate", "bench/runs"}, "", 0, true},
		{"size-target not three", []string{"calibrate", "--size-target", "50/50"}, "", 0, true},
		{"size-target not 100", []string{"calibrate", "--size-target=50/35/10"}, "", 0, true},
		{"size-target with a zero", []string{"calibrate", "--size-target", "60/40/0"}, "", 0, true},
		{"size-target twice", []string{"calibrate", "--size-target", "40/40/20", "--size-target", "40/40/20"}, "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, minRuns, target, err := decisionsCalibrateArgs(tc.args)
			if tc.wantErr {
				var ce *apperr.ConfigError
				if !errors.As(err, &ce) || !strings.Contains(ce.Msg, "usage: orchestrator decisions report|calibrate [--runs <dir>] [--min-runs N]") {
					t.Fatalf("decisionsCalibrateArgs(%q) error = %v, want a ConfigError with the usage", tc.args, err)
				}
				return
			}
			if err != nil || dir != tc.dir || minRuns != tc.minRuns || target != report.DefaultSizeTarget {
				t.Fatalf("decisionsCalibrateArgs(%q) = %q, %d, %v, %v; want %q, %d", tc.args, dir, minRuns, target, err, tc.dir, tc.minRuns)
			}
		})
	}

	_, _, target, err := decisionsCalibrateArgs([]string{"calibrate", "--size-target", "40/40/20"})
	if err != nil || target != (report.SizeTarget{40, 40, 20}) {
		t.Errorf("--size-target 40/40/20 = %v, %v", target, err)
	}
}

func TestDecisionsCalibrate_RejectsBadUsage(t *testing.T) {
	for _, args := range [][]string{
		{"decisions"},
		{"decisions", "calibrate", "--min-runs", "0"},
		{"decisions", "report", "extra"},
		{"decisions", "calibrate", "stray"},
	} {
		_, err := runCLI(t, args...)
		var ce *apperr.ConfigError
		if !errors.As(err, &ce) || !strings.Contains(ce.Msg, "decisions report|calibrate") {
			t.Errorf("Run(%q) error = %v, want a ConfigError with the usage", args, err)
		}
	}
}

func TestDecisionsCalibrate_MissingDirIsNoRuns(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	out, err := runCLI(t, "decisions", "calibrate", "--runs", missing)
	if want := "Calibration report: " + missing + "\nno runs yet\n"; err != nil || out != want {
		t.Errorf("out %q, err %v; want %q", out, err, want)
	}
}

// treeEntry is everything about a file or directory that a read could change.
type treeEntry struct {
	Mode    fs.FileMode
	Size    int64
	ModTime int64
	Sum     [sha256.Size]byte
}

func snapshotTree(t *testing.T, root string) map[string]treeEntry {
	t.Helper()
	tree := map[string]treeEntry{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		e := treeEntry{Mode: info.Mode(), Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			e.Sum = sha256.Sum256(data)
		}
		tree[path] = e
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// The command reads and prints; it leaves the runs directory exactly as it found it: no new
// file or directory (a lock, a temporary file), and no changed byte, mode or time.
func TestDecisionsCalibrate_ReadOnly(t *testing.T) {
	root := t.TempDir()
	hash := domain.PolicyHash(domain.DecisionPolicy{ProjectContextThreshold: 0.5})
	for _, sub := range []string{"flat", "nested/cfg/rep1"} {
		store := persistence.NewStore(filepath.Join(root, sub))
		run, err := store.NewRun(domain.Task{Description: "fix the bug"})
		if err != nil {
			t.Fatal(err)
		}
		p, th := 0.7, 0.5
		trace := domain.DecisionTrace{
			Request: "fix the bug", Source: domain.DecisionSourceJev, Provider: "jev", Model: "jev-1.13.0", PolicyHash: hash,
			Complexity: domain.ComplexityOutcome{Selected: domain.ComplexityMedium},
			Stages:     []domain.StageOutcome{{Stage: domain.StageTesting, Probability: &p, Threshold: &th, Enabled: true}},
		}
		if err := store.SaveDecision(run.ID, trace); err != nil {
			t.Fatal(err)
		}
	}
	// A broken run is listed, not repaired.
	broken := filepath.Join(root, "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "run.json"), []byte(`{"surprise":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Let a changed mtime show: the runs above were written a moment ago.
	old := time.Now().Add(-48 * time.Hour)
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, old, old)
	}); err != nil {
		t.Fatal(err)
	}

	before := snapshotTree(t, root)
	out, err := runCLI(t, "decisions", "calibrate", "--runs", root, "--min-runs", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Calibration report: "+root+"\n") || !strings.Contains(out, "policy "+hash[:12]) ||
		!strings.Contains(out, "1 unreadable") || !strings.Contains(out, "policy.stages.thresholds") {
		t.Errorf("report:\n%s", out)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(before, after) {
		for path, e := range after {
			if b, ok := before[path]; !ok {
				t.Errorf("created %s", path)
			} else if b != e {
				t.Errorf("changed %s: %+v -> %+v", path, b, e)
			}
		}
		for path := range before {
			if _, ok := after[path]; !ok {
				t.Errorf("removed %s", path)
			}
		}
	}
}
