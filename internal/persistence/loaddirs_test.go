package persistence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

// placeRun creates a run with a Store, then moves its directory to dst, the way a bench
// lays its runs out; dst's last element names the run's directory. With nameByID, dst is
// the parent directory and the run's own ID names the directory, as a plain store does.
func placeRun(t *testing.T, dst string, withDecision, nameByID bool) *domain.Run {
	t.Helper()
	scratch := NewStore(t.TempDir())
	run, err := scratch.NewRun(domain.Task{Description: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if withDecision {
		trace := domain.DecisionTrace{Request: "fix", Source: domain.DecisionSourceJev, Provider: "jev", Model: "jev-1.13.0"}
		if err := scratch.SaveDecision(run.ID, trace); err != nil {
			t.Fatal(err)
		}
	}
	if nameByID {
		dst = filepath.Join(dst, run.ID)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(scratch.runDir(run.ID), dst); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestLoadRunDirs_NestedTree(t *testing.T) {
	root := t.TempDir()
	a := placeRun(t, filepath.Join(root, "taskA", "cfg1", "rep1"), true, false)
	// A directory named like a run, as a plain store lays it out.
	b := placeRun(t, filepath.Join(root, "taskA", "cfg2", "rep1"), false, true)
	c := placeRun(t, filepath.Join(root, "taskB", "cfg1", "rep2"), false, false)

	// A run directory named after another run is refused, as LoadRun refuses it.
	misnamed := filepath.Join(root, "taskB", "cfg1", "rep3", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	placeRun(t, misnamed, false, false)
	// A corrupt run.json, and a directory without one.
	corrupt := filepath.Join(root, "taskB", "cfg2", "rep1")
	if err := os.MkdirAll(corrupt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corrupt, "run.json"), []byte(`{"id":"x","surprise":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "taskC", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "taskC", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	found, bad, err := LoadRunDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 3 {
		t.Fatalf("found %d runs, want 3: %+v", len(found), found)
	}
	for i, want := range []struct {
		dir string
		run *domain.Run
	}{
		{filepath.Join(root, "taskA", "cfg1", "rep1"), a},
		{filepath.Join(root, "taskA", "cfg2", "rep1", b.ID), b},
		{filepath.Join(root, "taskB", "cfg1", "rep2"), c},
	} {
		if found[i].Dir != want.dir || found[i].RunID != want.run.ID || found[i].Run.ID != want.run.ID {
			t.Errorf("found[%d] = %s %s, want %s %s", i, found[i].Dir, found[i].RunID, want.dir, want.run.ID)
		}
	}
	if found[0].Trace == nil || found[0].Trace.Model != "jev-1.13.0" || found[0].At.IsZero() {
		t.Errorf("the run with a decision.json has trace %+v at %v", found[0].Trace, found[0].At)
	}
	if found[1].Trace != nil || found[2].Trace != nil {
		t.Errorf("a run without decision.json has no trace")
	}

	if len(bad) != 2 || bad[0].Dir != misnamed || bad[1].Dir != corrupt {
		t.Fatalf("unreadable = %+v, want the misnamed and the corrupt run, sorted", bad)
	}
	if !strings.Contains(bad[0].Err.Error(), "directory name") || !strings.Contains(bad[1].Err.Error(), "not a valid run") {
		t.Errorf("unreadable errors: %v / %v", bad[0].Err, bad[1].Err)
	}
}

func TestLoadRunDirs_BadDecisionMakesTheRunUnreadable(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "rep1")
	placeRun(t, dir, false, false)
	if err := os.WriteFile(filepath.Join(dir, "decision.json"), []byte(`{"request":"fix","surprise":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	found, bad, err := LoadRunDirs(root)
	if err != nil || len(found) != 0 || len(bad) != 1 || !strings.Contains(bad[0].Err.Error(), "decision.json") {
		t.Errorf("found %+v, unreadable %+v, err %v", found, bad, err)
	}
}

func TestLoadRunDirs_DoesNotSearchInsideARun(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "rep1")
	placeRun(t, dir, false, false)
	// What a step wrote into the run's directory is not a run.
	placeRun(t, filepath.Join(dir, "artifacts", "forged"), false, false)

	found, bad, err := LoadRunDirs(root)
	if err != nil || len(found) != 1 || len(bad) != 0 || found[0].Dir != dir {
		t.Errorf("found %+v, unreadable %+v, err %v", found, bad, err)
	}
}

func TestLoadRunDirs_MissingAndNonDirectoryRoot(t *testing.T) {
	base := t.TempDir()
	if found, bad, err := LoadRunDirs(filepath.Join(base, "missing")); err != nil || found != nil || bad != nil {
		t.Errorf("a missing root = %v, %v, %v; want no runs", found, bad, err)
	}
	if found, bad, err := LoadRunDirs(base); err != nil || found != nil || bad != nil {
		t.Errorf("an empty root = %v, %v, %v; want no runs", found, bad, err)
	}
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadRunDirs(file); err == nil {
		t.Error("a root that is a file must be an error")
	}
}

func TestLoadRunDirs_DoesNotFollowSymlinks(t *testing.T) {
	outside := t.TempDir()
	placeRun(t, filepath.Join(outside, "rep1"), false, false)

	root := t.TempDir()
	inside := placeRun(t, filepath.Join(root, "real", "rep1"), false, false)
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("no symlinks: %v", err)
	}

	found, bad, err := LoadRunDirs(root)
	if err != nil || len(bad) != 0 || len(found) != 1 || found[0].RunID != inside.ID {
		t.Errorf("found %+v, unreadable %+v, err %v; want only the run inside the root", found, bad, err)
	}

	// The root itself may be a link: it is the directory the user named.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("no symlinks: %v", err)
	}
	if found, _, err := LoadRunDirs(link); err != nil || len(found) != 1 {
		t.Errorf("through a root link: found %+v, err %v", found, err)
	}
}
