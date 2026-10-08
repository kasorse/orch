package results

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testManifest() Manifest {
	var m Manifest
	m.Orchestrator = Build{Version: "orchestrator dev", SHA256: "bin1"}
	m.ClaudeCLI = "2.1.293 (Claude Code)"
	m.Configs = map[string]string{"A": "cfgA", "B": "cfgB"}
	m.Tasks = map[string]string{"t1": "hash1", "t2": "hash2"}
	m.Contributor = "owner"
	return m.New(time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC))
}

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, found, err := LoadManifest(dir); err != nil || found {
		t.Fatalf("empty dir: found = %v, err = %v", found, err)
	}

	want := testManifest()
	if err := SaveManifest(dir, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := LoadManifest(dir)
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("manifest\n%+v\nwant\n%+v", got, want)
	}
	if want.Created != "2026-10-07T10:00:00Z" || want.Updated != want.Created || want.Schema != ManifestSchema {
		t.Errorf("New stamped %+v", want)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("the directory holds %d files, want only %s", len(entries), ManifestName)
	}
}

func TestLoadManifestRejectsABrokenFile(t *testing.T) {
	for name, content := range map[string]string{
		"not json":     "{",
		"wrong schema": `{"schema": 9}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := LoadManifest(dir); err == nil {
				t.Fatal("LoadManifest succeeded")
			}
		})
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		name   string
		change func(m *Manifest)
		want   []string // the What of each mismatch
	}{
		{"the same", func(m *Manifest) {}, nil},
		{"another orchestrator version", func(m *Manifest) { m.Orchestrator.Version = "orchestrator c0ffee" }, []string{"orchestrator version"}},
		{"another binary", func(m *Manifest) { m.Orchestrator.SHA256 = "bin2" }, []string{"orchestrator binary sha256"}},
		{"another claude CLI", func(m *Manifest) { m.ClaudeCLI = "2.1.300" }, []string{"claude CLI"}},
		{"another contributor", func(m *Manifest) { m.Contributor = "friend" }, []string{"contributor"}},
		{"an edited config", func(m *Manifest) { m.Configs["B"] = "other" }, []string{"config B sha256"}},
		{"an edited task", func(m *Manifest) { m.Tasks["t2"] = "other" }, []string{"task t2 sha256"}},
		{"a new config and a new task", func(m *Manifest) { m.Configs["E"] = "cfgE"; m.Tasks["t3"] = "hash3" }, nil},
		{"fewer configs and tasks", func(m *Manifest) { delete(m.Configs, "A"); delete(m.Tasks, "t1") }, nil},
		{"everything at once", func(m *Manifest) {
			m.Orchestrator = Build{Version: "v2", SHA256: "bin2"}
			m.ClaudeCLI = "3"
		}, []string{"orchestrator version", "orchestrator binary sha256", "claude CLI"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old, cur := testManifest(), testManifest()
			tt.change(&cur)

			var got []string
			for _, m := range Compare(old, cur) {
				got = append(got, m.What)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mismatches %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMismatchErrorNamesEachDifference(t *testing.T) {
	old, cur := testManifest(), testManifest()
	cur.ClaudeCLI = "2.1.300"
	cur.Orchestrator.Version = ""

	var err error = &MismatchError{List: Compare(old, cur)}
	var me *MismatchError
	if !errors.As(err, &me) || len(me.List) != 2 {
		t.Fatalf("error %v", err)
	}
	for _, want := range []string{`claude CLI: was "2.1.293 (Claude Code)", now "2.1.300"`, `orchestrator version: was "orchestrator dev", now —`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestMergeAddsConfigsAndTasksAndRecordsAMixedContinuation(t *testing.T) {
	old := testManifest()
	cur := testManifest()
	cur.Configs = map[string]string{"B": "cfgB2", "E": "cfgE"}
	cur.Tasks = map[string]string{"t3": "hash3"}
	cur.ClaudeCLI = "2.1.300"
	later := time.Date(2026, 10, 8, 9, 30, 0, 0, time.UTC)

	mismatches := Compare(old, cur)
	merged := old.Merge(cur, mismatches, later)

	wantConfigs := map[string]string{"A": "cfgA", "B": "cfgB2", "E": "cfgE"}
	wantTasks := map[string]string{"t1": "hash1", "t2": "hash2", "t3": "hash3"}
	if !reflect.DeepEqual(merged.Configs, wantConfigs) || !reflect.DeepEqual(merged.Tasks, wantTasks) {
		t.Errorf("configs %v, tasks %v", merged.Configs, merged.Tasks)
	}
	if merged.ClaudeCLI != "2.1.300" || merged.Created != old.Created || merged.Updated != "2026-10-08T09:30:00Z" {
		t.Errorf("merged %+v", merged)
	}
	if len(merged.Mixed) != 1 || merged.Mixed[0].ClaudeCLI != "2.1.300" || merged.Mixed[0].At != merged.Updated {
		t.Fatalf("mixed = %+v", merged.Mixed)
	}
	if len(merged.Mixed[0].Differences) != 2 {
		t.Errorf("differences = %v, want the claude CLI and config B", merged.Mixed[0].Differences)
	}
	if len(old.Mixed) != 0 || old.Configs["B"] != "cfgB" {
		t.Errorf("Merge changed the old manifest: %+v", old)
	}
}

func TestMergeWithoutMismatchesRecordsNothing(t *testing.T) {
	old := testManifest()
	cur := testManifest()
	cur.Configs["E"] = "cfgE"

	merged := old.Merge(cur, Compare(old, cur), time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC))
	if len(merged.Mixed) != 0 || merged.Configs["E"] != "cfgE" || merged.Configs["A"] != "cfgA" {
		t.Errorf("merged %+v", merged)
	}
}
