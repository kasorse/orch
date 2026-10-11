package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSpec_WriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	spec := testSpec(t)
	spec.Sandbox.ReadOnly = []string{"/home/u/go/pkg/mod"}
	spec.Sandbox.ReadWrite = []string{"/home/u/.cache/go-build"}
	if err := WriteSpec(dir, spec); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, SpecFile)
	if m := mode(t, path); m != 0o600 {
		t.Errorf("verify.json mode = %v, want 0600", m)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("the directory holds %v (%v), want verify.json alone: no temporary file is left", entries, err)
	}

	got, err := ReadSpec(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Errorf("round trip changed the spec:\n got %+v\nwant %+v", got, spec)
	}
	if got.Version != 1 || got.CallTimeout != time.Minute {
		t.Errorf("version %d, timeout %v", got.Version, got.CallTimeout)
	}
}

func TestSpec_WriteReplacesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	spec := testSpec(t)
	if err := WriteSpec(dir, spec); err != nil {
		t.Fatal(err)
	}
	spec.MaxCalls = 3
	if err := WriteSpec(dir, spec); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSpec(dir)
	if err != nil || got.MaxCalls != 3 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestSpec_WriteRefusesAnInvalidSpec(t *testing.T) {
	dir := t.TempDir()
	spec := testSpec(t)
	spec.Sandbox = nil
	err := WriteSpec(dir, spec)
	if err == nil || !strings.Contains(err.Error(), "verify.json") || !strings.Contains(err.Error(), "sandbox") {
		t.Errorf("WriteSpec = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused spec left %v behind", entries)
	}
	if err := WriteSpec(filepath.Join(dir, "missing"), testSpec(t)); err == nil {
		t.Error("writing into a directory that does not exist succeeded")
	}
}

// specMap is the valid spec as the generic JSON a test can damage.
func specMap(t *testing.T) map[string]any {
	t.Helper()
	data, err := json.Marshal(testSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func writeRaw(t *testing.T, dir string, data []byte, perm os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, SpecFile)
	if err := os.WriteFile(path, data, perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
}

func TestReadSpec_Rejects(t *testing.T) {
	manyChecks := func(m map[string]any) {
		var checks []map[string]string
		for i := range 17 {
			checks = append(checks, map[string]string{"name": "c" + strings.Repeat("x", i), "command": "true"})
		}
		m["checks"] = checks
	}
	cases := []struct {
		name   string
		mutate func(m map[string]any)
		want   string
	}{
		{"unknown field", func(m map[string]any) { m["extra"] = 1 }, "unknown field"},
		{"unknown field in the sandbox", func(m map[string]any) { m["sandbox"].(map[string]any)["network"] = true }, "unknown field"},
		{"version 2", func(m map[string]any) { m["version"] = 2 }, "unsupported version 2"},
		{"no version", func(m map[string]any) { delete(m, "version") }, "unsupported version 0"},
		{"no sandbox", func(m map[string]any) { m["sandbox"] = nil }, "sandbox is missing"},
		{"sandbox absent", func(m map[string]any) { delete(m, "sandbox") }, "sandbox is missing"},
		{"relative sandbox home", func(m map[string]any) { m["sandbox"].(map[string]any)["home"] = "home/u" }, "not absolute"},
		{"relative bind", func(m map[string]any) { m["sandbox"].(map[string]any)["ro_binds"] = []string{"-x"} }, "not absolute"},
		{"relative repo root", func(m map[string]any) { m["repo_root"] = "repo" }, "not an absolute path"},
		{"empty test command", func(m map[string]any) { m["test_command"] = "  " }, "test_command is empty"},
		{"bad check name", func(m map[string]any) { m["checks"] = []map[string]string{{"name": "Vet;", "command": "x"}} }, "invalid"},
		{"duplicate check", func(m map[string]any) {
			m["checks"] = []map[string]string{{"name": "vet", "command": "x"}, {"name": "vet", "command": "y"}}
		}, "twice"},
		{"check without a command", func(m map[string]any) { m["checks"] = []map[string]string{{"name": "vet", "command": ""}} }, "no command"},
		{"too many checks", manyChecks, "at most"},
		{"ORCH variable in the env", func(m map[string]any) { m["test_env"] = []string{"ORCH_TEST_FILTER=x"} }, "ORCH_"},
		{"env entry without a value", func(m map[string]any) { m["test_env"] = []string{"PATH"} }, "NAME=value"},
		{"env entry with a bad name", func(m map[string]any) { m["test_env"] = []string{"A-B=1"} }, "NAME=value"},
		{"no before-state", func(m map[string]any) { delete(m, "before") }, "guard state"},
		{"incomplete before-state", func(m map[string]any) { m["before"] = map[string]any{"patterns": []string{"a"}} }, "guard state"},
		{"no calls allowed", func(m map[string]any) { m["max_calls"] = 0 }, "max_calls"},
		{"too many calls", func(m map[string]any) { m["max_calls"] = 51 }, "max_calls"},
		{"no timeout", func(m map[string]any) { m["call_timeout"] = 0 }, "call_timeout"},
		{"negative output bound", func(m map[string]any) { m["output_bytes"] = -1 }, "output_bytes"},
		{"wrong type", func(m map[string]any) { m["max_calls"] = "8" }, "max_calls"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			m := specMap(t)
			tc.mutate(m)
			data, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			writeRaw(t, dir, data, 0o600)
			_, err = ReadSpec(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "verify.json") {
				t.Errorf("ReadSpec = %v, want an error naming verify.json and containing %q", err, tc.want)
			}
		})
	}
}

func TestReadSpec_RejectsABadFile(t *testing.T) {
	valid, err := json.Marshal(testSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("missing", func(t *testing.T) {
		if _, err := ReadSpec(t.TempDir()); err == nil || !strings.Contains(err.Error(), "verify.json") {
			t.Errorf("ReadSpec = %v", err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		dir := t.TempDir()
		writeRaw(t, dir, nil, 0o600)
		if _, err := ReadSpec(dir); err == nil {
			t.Error("accepted an empty file")
		}
	})
	t.Run("not json", func(t *testing.T) {
		dir := t.TempDir()
		writeRaw(t, dir, []byte("{version: 1}"), 0o600)
		if _, err := ReadSpec(dir); err == nil {
			t.Error("accepted malformed JSON")
		}
	})
	t.Run("data after the object", func(t *testing.T) {
		dir := t.TempDir()
		writeRaw(t, dir, append(append([]byte(nil), valid...), []byte(" {}")...), 0o600)
		if _, err := ReadSpec(dir); err == nil || !strings.Contains(err.Error(), "after the object") {
			t.Errorf("ReadSpec = %v", err)
		}
	})
	t.Run("readable by others", func(t *testing.T) {
		for _, perm := range []os.FileMode{0o644, 0o640, 0o602} {
			dir := t.TempDir()
			writeRaw(t, dir, valid, perm)
			if _, err := ReadSpec(dir); err == nil || !strings.Contains(err.Error(), "0600") {
				t.Errorf("mode %v: ReadSpec = %v", perm, err)
			}
		}
	})
	t.Run("a symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "real.json")
		if err := os.WriteFile(target, valid, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, SpecFile)); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadSpec(dir); err == nil {
			t.Error("followed a symlink")
		}
	})
	t.Run("a directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, SpecFile), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadSpec(dir); err == nil {
			t.Error("accepted a directory")
		}
	})
	t.Run("the valid file is accepted", func(t *testing.T) {
		dir := t.TempDir()
		writeRaw(t, dir, valid, 0o600)
		if _, err := ReadSpec(dir); err != nil {
			t.Error(err)
		}
	})
}

func TestSpec_ChecksAreOptional(t *testing.T) {
	dir := t.TempDir()
	spec := testSpec(t)
	spec.Checks = nil
	spec.TestEnv = nil
	if err := WriteSpec(dir, spec); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSpec(dir)
	if err != nil || len(got.Checks) != 0 || len(got.TestEnv) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
}
