package lang

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"orchbench/internal/task"
)

func TestTableMatchesSpec(t *testing.T) {
	tests := []struct {
		language string
		globs    []string
		setup    string
		test     string
	}{
		{
			"go",
			[]string{"**/*_test.go"},
			"go mod download",
			"go test ./...",
		},
		{
			"python",
			[]string{"**/test_*.py", "**/*_test.py", "tests/**"},
			"python -m venv .venv && .venv/bin/pip install -e .[test]",
			".venv/bin/pytest -q",
		},
		{
			"node",
			[]string{"**/*.{test,spec}.{js,jsx,ts,tsx,mjs,cjs}", "**/__tests__/**", "test/**"},
			"npm ci",
			"npm test",
		},
		{
			"php",
			[]string{"tests/**/*Test.php"},
			"composer install --no-interaction",
			"vendor/bin/phpunit",
		},
		{
			"cpp",
			[]string{"**/*_test.{cc,cpp}", "**/*_unittest.{cc,cpp}", "test/**", "tests/**"},
			"cmake -S . -B build && cmake --build build -j",
			"cmake --build build -j && ctest --test-dir build --output-on-failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.language, func(t *testing.T) {
			d, ok := Lookup(tt.language)
			if !ok {
				t.Fatal("no defaults")
			}
			if !reflect.DeepEqual(d.TestGlobs, tt.globs) || d.SetupCommand != tt.setup || d.TestCommand != tt.test {
				t.Fatalf("defaults = %+v", d)
			}
		})
	}
	if len(defaults) != len(tests) {
		t.Fatalf("the table has %d languages, the spec test %d", len(defaults), len(tests))
	}
}

func TestLookupUnknown(t *testing.T) {
	for _, language := range []string{"rust", "", "Go"} {
		if _, ok := Lookup(language); ok {
			t.Errorf("Lookup(%q) found defaults", language)
		}
	}
}

func TestLookupReturnsCopy(t *testing.T) {
	d, _ := Lookup("go")
	d.TestGlobs[0] = "changed"

	again, _ := Lookup("go")
	if again.TestGlobs[0] != "**/*_test.go" {
		t.Fatal("Lookup exposes the table's slice")
	}
}

func TestResolveFillsEveryField(t *testing.T) {
	for language, want := range defaults {
		t.Run(language, func(t *testing.T) {
			x := task.Task{Language: language}
			if err := Resolve(&x); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(x.TestGlobs, want.TestGlobs) || x.SetupCommand != want.SetupCommand || x.TestCommand != want.TestCommand {
				t.Fatalf("resolved = %+v", x)
			}
		})
	}
}

func TestResolveKeepsOverrides(t *testing.T) {
	tests := []struct {
		name string
		task task.Task
		want task.Task
	}{
		{
			"test_globs",
			task.Task{Language: "go", TestGlobs: []string{"pkg/**"}},
			task.Task{Language: "go", TestGlobs: []string{"pkg/**"}, SetupCommand: "go mod download", TestCommand: "go test ./..."},
		},
		{
			"setup_command",
			task.Task{Language: "go", SetupCommand: "make deps"},
			task.Task{Language: "go", TestGlobs: []string{"**/*_test.go"}, SetupCommand: "make deps", TestCommand: "go test ./..."},
		},
		{
			"test_command",
			task.Task{Language: "go", TestCommand: "go test -short ./..."},
			task.Task{Language: "go", TestGlobs: []string{"**/*_test.go"}, SetupCommand: "go mod download", TestCommand: "go test -short ./..."},
		},
		{
			"all three",
			task.Task{Language: "go", TestGlobs: []string{"a"}, SetupCommand: "b", TestCommand: "c"},
			task.Task{Language: "go", TestGlobs: []string{"a"}, SetupCommand: "b", TestCommand: "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Resolve(&tt.task); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tt.task, tt.want) {
				t.Fatalf("resolved = %+v, want %+v", tt.task, tt.want)
			}
		})
	}
}

func TestResolveUnknownLanguage(t *testing.T) {
	full := task.Task{Language: "rust", TestGlobs: []string{"tests/**"}, SetupCommand: "cargo fetch", TestCommand: "cargo test"}

	tests := []struct {
		name    string
		mutate  func(*task.Task)
		wantErr string // empty: no error
	}{
		{"everything set", func(*task.Task) {}, ""},
		{"no test_globs", func(x *task.Task) { x.TestGlobs = nil }, "test_globs"},
		{"no setup_command", func(x *task.Task) { x.SetupCommand = "" }, "setup_command"},
		{"no test_command", func(x *task.Task) { x.TestCommand = "" }, "test_command"},
		{"nothing set", func(x *task.Task) { *x = task.Task{Language: "rust"} }, "test_globs, setup_command, test_command"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x := full
			tt.mutate(&x)
			err := Resolve(&x)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), `"rust"`) {
				t.Fatalf("Resolve = %v, want an error naming %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateUsesLanguageDefaults(t *testing.T) {
	x := task.Task{
		ID:         "go-queue-retry",
		Repo:       "https://example.com/queue.git",
		BaseCommit: strings.Repeat("a", 40),
		FixCommit:  strings.Repeat("b", 40),
		Category:   "bugfix",
		Language:   "go",
		Lang:       "en",
		Task:       "Make the retry delay exponential.",
		Oracle:     task.Oracle{Files: []string{"queue/retry_test.go"}, Command: "go test ./queue/"},
		Timeout:    task.Duration(30 * time.Minute),
	}

	if err := Validate(x); err != nil {
		t.Fatalf("a go task needs no test_globs: %v", err)
	}

	x.Language = "rust"
	err := Validate(x)
	var verr *task.ValidationError
	if !errors.As(err, &verr) || !strings.Contains(err.Error(), "test_globs, setup_command, test_command") {
		t.Fatalf("Validate = %v, want the missing fields of an unknown language", err)
	}
}
