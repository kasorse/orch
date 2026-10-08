package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchbench/internal/cache"
	"orchbench/internal/exitcode"
	"orchbench/internal/sandbox"
	"orchbench/internal/task"
	"orchbench/internal/testutil"
)

func validateArgs(t *testing.T, cacheDir string, args ...string) (stdout string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = run(context.Background(), append(append([]string{"validate"}, args...), "--cache-dir", cacheDir), &out, &errOut)
	return out.String(), err
}

func TestValidateUsageErrors(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"a", "b"} {
		testutil.Apply(t, dir, map[string]string{id + ".yaml": "id: " + id + "\n"})
	}
	other := t.TempDir()
	testutil.Apply(t, other, map[string]string{"a.yaml": "id: a\n"})

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no --tasks", nil, "--tasks is required"},
		{"a glob without a match", []string{"--tasks", filepath.Join(dir, "*.yml")}, "matches no file"},
		{"a bad glob", []string{"--tasks", filepath.Join(dir, "[")}, "syntax error in pattern"},
		{"an unknown id", []string{"--tasks", filepath.Join(dir, "*.yaml"), "--ids", "a,c"}, `no task file with id "c"`},
		{"two files with one id", []string{"--tasks", filepath.Join(dir, "*.yaml"), "--tasks", filepath.Join(other, "*.yaml")}, `two task files with id "a"`},
		{"-j 0", []string{"--tasks", filepath.Join(dir, "*.yaml"), "-j", "0"}, "must be at least 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, err := validateArgs(t, t.TempDir(), tt.args...)
			if code := exitcode.FromError(err); code != exitcode.Usage {
				t.Fatalf("exit code %d (%v), want %d", code, err, exitcode.Usage)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not mention %q", err, tt.want)
			}
			if stdout != "" {
				t.Errorf("stdout = %q", stdout)
			}
		})
	}
}

func TestSelectTasksSortsAndFilters(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"c", "a", "b"} {
		testutil.Apply(t, dir, map[string]string{id + ".yaml": ""})
	}
	t.Setenv("HOME", dir)

	fs := newFlagSet(t, "validate", "--tasks", "~/*.yaml", "--tasks", filepath.Join(dir, "a.yaml"))
	paths, err := selectTasks(fs)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids(paths), ","); got != "a,b,c" {
		t.Errorf("ids %s, want a,b,c (sorted, deduplicated, ~ expanded)", got)
	}

	fs = newFlagSet(t, "validate", "--tasks", filepath.Join(dir, "*.yaml"), "--ids", "c, a")
	paths, err = selectTasks(fs)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids(paths), ","); got != "a,c" {
		t.Errorf("--ids: %s, want a,c", got)
	}
}

func newFlagSet(t *testing.T, name string, args ...string) *flag.FlagSet {
	t.Helper()
	for _, c := range commands {
		if c.name != name {
			continue
		}
		fs := newCommandFlags(c, &commonFlags{})
		if _, err := parseInterspersed(fs, args); err != nil {
			t.Fatal(err)
		}
		return fs
	}
	t.Fatalf("no command %s", name)
	return nil
}

func ids(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = fileID(p)
	}
	return out
}

func TestValidateChecksPrerequisites(t *testing.T) {
	dir := t.TempDir()
	testutil.Apply(t, dir, map[string]string{"a.yaml": ""})

	old := lookPath
	t.Cleanup(func() { lookPath = old })
	lookPath = func(file string) (string, error) {
		if file == "bwrap" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + file, nil
	}

	_, err := validateArgs(t, t.TempDir(), "--tasks", filepath.Join(dir, "*.yaml"))
	if code := exitcode.FromError(err); code != exitcode.Usage || !strings.Contains(err.Error(), "bwrap (not on PATH)") {
		t.Fatalf("exit %d: %v", code, err)
	}
}

func TestValidateRefusesWhereBwrapCannotSandbox(t *testing.T) {
	dir := t.TempDir()
	testutil.Apply(t, dir, map[string]string{"a.yaml": ""})

	oldLook, oldProbe := lookPath, probe
	t.Cleanup(func() { lookPath, probe = oldLook, oldProbe })
	lookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	probe = func(context.Context) error {
		return &sandbox.ProbeError{Detail: "bwrap: setting up uid map: Permission denied"}
	}

	cacheDir := t.TempDir()
	_, err := validateArgs(t, cacheDir, "--tasks", filepath.Join(dir, "*.yaml"))
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Fatalf("exit %d: %v", code, err)
	}
	for _, want := range []string{"Permission denied", "AppArmor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	if entries, _ := os.ReadDir(cacheDir); len(entries) != 0 {
		t.Errorf("the cache directory was written: %v", entries)
	}
}

func TestValidateEndToEnd(t *testing.T) {
	testutil.RequireBwrap(t)

	p := testutil.NewSHProject(t)
	dir := t.TempDir()
	testutil.WriteTask(t, dir, p.Task("code"))
	testutil.WriteTask(t, dir, p.Question("question"))
	cacheDir := t.TempDir()

	stdout, err := validateArgs(t, cacheDir, "--tasks", filepath.Join(dir, "*.yaml"), "-j", "2")
	if err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "code ") || !strings.HasPrefix(lines[1], "question ") || lines[2] != "2 of 2 valid" {
		t.Fatalf("output:\n%s", stdout)
	}

	store := cache.OpenValidated(cache.Dir(cacheDir))
	hash, _ := task.SHA256(filepath.Join(dir, "code.yaml"))
	if !store.IsValid("code", hash) {
		t.Error("the code task was not recorded")
	}
}

func TestValidateExitsOneOnAnInvalidTask(t *testing.T) {
	testutil.RequireBwrap(t)

	p := testutil.NewSHProject(t)
	dir := t.TempDir()
	testutil.WriteTask(t, dir, p.Task("good"))
	bad := p.Task("bad")
	bad.Oracle.Command = "true"
	testutil.WriteTask(t, dir, bad)

	stdout, err := validateArgs(t, t.TempDir(), "--tasks", filepath.Join(dir, "*.yaml"))
	if code := exitcode.FromError(err); code != exitcode.Failed {
		t.Fatalf("exit %d (%v), want 1\n%s", code, err, stdout)
	}
	if err.Error() != "1 of 2 tasks invalid" {
		t.Errorf("error %q", err)
	}
	if !strings.Contains(stdout, "bad   FAIL  oracle green at base_commit") || !strings.HasSuffix(stdout, "1 of 2 valid\n") {
		t.Errorf("output:\n%s", stdout)
	}
}
