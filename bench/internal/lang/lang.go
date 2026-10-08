// Package lang holds the per-language defaults of specs/benchmark/SPEC.md §5.5. `language`
// in a task file is an open name: these are the languages orchbench knows, for any other a
// task sets test_globs, setup_command and test_command itself.
package lang

import (
	"fmt"
	"strings"

	"orchbench/internal/task"
)

// defaults is the table of §5.5, verbatim.
var defaults = map[string]task.Defaults{
	"go": {
		TestGlobs:    []string{"**/*_test.go"},
		SetupCommand: "go mod download",
		TestCommand:  "go test ./...",
	},
	"python": {
		TestGlobs:    []string{"**/test_*.py", "**/*_test.py", "tests/**"},
		SetupCommand: "python -m venv .venv && .venv/bin/pip install -e .[test]",
		TestCommand:  ".venv/bin/pytest -q",
	},
	// `mine` and `validate` pick pnpm or yarn instead of npm by the lock file.
	"node": {
		TestGlobs:    []string{"**/*.{test,spec}.{js,jsx,ts,tsx,mjs,cjs}", "**/__tests__/**", "test/**"},
		SetupCommand: "npm ci",
		TestCommand:  "npm test",
	},
	"php": {
		TestGlobs:    []string{"tests/**/*Test.php"},
		SetupCommand: "composer install --no-interaction",
		TestCommand:  "vendor/bin/phpunit",
	},
	"cpp": {
		TestGlobs:    []string{"**/*_test.{cc,cpp}", "**/*_unittest.{cc,cpp}", "test/**", "tests/**"},
		SetupCommand: "cmake -S . -B build && cmake --build build -j",
		TestCommand:  "cmake --build build -j && ctest --test-dir build --output-on-failure",
	},
}

// Lookup returns the defaults of a language and whether orchbench has any. The result is a
// copy: changing it does not change the table.
func Lookup(language string) (task.Defaults, bool) {
	d, ok := defaults[language]
	d.TestGlobs = append([]string(nil), d.TestGlobs...)
	return d, ok
}

// Resolve fills the test_globs, setup_command and test_command a task leaves out from its
// language's defaults and keeps what it sets. For a language without defaults it fills
// nothing, and returns an error naming what is still missing.
func Resolve(t *task.Task) error {
	d, _ := Lookup(t.Language)
	if missing := d.Apply(t); len(missing) > 0 {
		return fmt.Errorf("language %q has no defaults for %s: set them in the task", t.Language, strings.Join(missing, ", "))
	}
	return nil
}

// Validate is task.Validate with the defaults of the task's language: the one call for a
// loaded task.
func Validate(t task.Task) error {
	d, _ := Lookup(t.Language)
	return t.Validate(d)
}
