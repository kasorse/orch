package task

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	baseSHA = "3f2c1a9b8d7e6f5a4b3c2d1e0f9a8b7c6d5e4f3a"
	fixSHA  = "9a1b2c3d4e5f60718293a4b5c6d7e8f901234567"
)

const fullYAML = `id: go-pgqueue-retry-backoff
repo: https://github.com/org/pgqueue.git
base_commit: ` + baseSHA + `
fix_commit: ` + fixSHA + `
category: bugfix
language: go
lang: en
pair: go-pgqueue-retry-backoff-ru
task: |
  Retries currently use a fixed 1s delay. Make the delay exponential.
expected_stages: [analysis, development, testing]
setup_command: "go mod download"
test_command: "go test ./..."
test_globs: ["**/*_test.go"]
oracle:
  files: [queue/retry_test.go]
  command: "go test ./queue/ -run 'TestRetry'"
sandbox:
  ro_binds: [/home/u/go/pkg/mod]
  rw_binds: [/tmp/cache]
timeout: 30m
`

func writeTask(t *testing.T, name, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestLoadRoundTrip(t *testing.T) {
	file := writeTask(t, "go-pgqueue-retry-backoff.yaml", fullYAML)

	got, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}

	want := Task{
		ID:             "go-pgqueue-retry-backoff",
		Repo:           "https://github.com/org/pgqueue.git",
		BaseCommit:     baseSHA,
		FixCommit:      fixSHA,
		Category:       "bugfix",
		Language:       "go",
		Lang:           "en",
		Pair:           "go-pgqueue-retry-backoff-ru",
		Task:           "Retries currently use a fixed 1s delay. Make the delay exponential.\n",
		ExpectedStages: []string{"analysis", "development", "testing"},
		SetupCommand:   "go mod download",
		TestCommand:    "go test ./...",
		TestGlobs:      []string{"**/*_test.go"},
		Oracle: Oracle{
			Files:   []string{"queue/retry_test.go"},
			Command: "go test ./queue/ -run 'TestRetry'",
		},
		Sandbox: Sandbox{
			RoBinds: []string{"/home/u/go/pkg/mod"},
			RwBinds: []string{"/tmp/cache"},
		},
		Timeout: Duration(30 * time.Minute),
		path:    file,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load =\n%+v\nwant\n%+v", got, want)
	}
	if got.FileID() != "go-pgqueue-retry-backoff" {
		t.Fatalf("FileID = %q", got.FileID())
	}
	if got.IsDraft() {
		t.Fatal("IsDraft = true for a file without draft")
	}
	if err := got.Validate(Defaults{}); err != nil {
		t.Fatalf("Validate = %v", err)
	}
}

func TestLoadQuestionFields(t *testing.T) {
	file := writeTask(t, "go-q.yaml", `id: go-q
category: question
answer_checks: ['\bretry_test\.go\b', 'exponential|backoff']
reference_answer: |
  See retry_test.go: exponential backoff.
`)
	got, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`\bretry_test\.go\b`, `exponential|backoff`}; !reflect.DeepEqual(got.AnswerChecks, want) {
		t.Fatalf("AnswerChecks = %q, want %q", got.AnswerChecks, want)
	}
	if got.ReferenceAnswer != "See retry_test.go: exponential backoff.\n" {
		t.Fatalf("ReferenceAnswer = %q", got.ReferenceAnswer)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"unknown top-level key", fullYAML + "colour: red\n", "colour"},
		{"unknown nested key", strings.Replace(fullYAML, "  command:", "  foo: 1\n  command:", 1), "foo"},
		{"empty file", "", "empty"},
		{"comments only", "# nothing here\n", "empty"},
		{"two documents", fullYAML + "---\n" + fullYAML, "more than one"},
		{"bad timeout", strings.Replace(fullYAML, "timeout: 30m", "timeout: soon", 1), "timeout"},
		{"timeout without unit", strings.Replace(fullYAML, "timeout: 30m", "timeout: 1800", 1), "timeout"},
		{"not yaml", "id: [unclosed\n", "yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := writeTask(t, "go-pgqueue-retry-backoff.yaml", tt.content)
			_, err := Load(file)
			if err == nil {
				t.Fatal("Load succeeded")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not mention %q", err, tt.want)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load = %v, want not exist", err)
	}
}

func TestLoadDraft(t *testing.T) {
	file := writeTask(t, "orche-b648329.yaml", "draft: true\nid: orche-b648329\nexpected_stages: []\n")

	got, err := Load(file)
	if err != nil {
		t.Fatalf("a draft must load: %v", err)
	}
	if !got.IsDraft() {
		t.Fatal("IsDraft = false")
	}
}

func TestFileID(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"", ""},
		{"/tasks/a-b.yaml", "a-b"},
		{"/tasks/a-b.yml", "a-b"},
		{"a-b.yaml", "a-b"},
	}
	for _, tt := range tests {
		if got := (Task{path: tt.path}).FileID(); got != tt.want {
			t.Errorf("FileID(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func validGo() Task {
	return Task{
		ID:         "go-queue-retry",
		Repo:       "https://github.com/org/queue.git",
		BaseCommit: baseSHA,
		FixCommit:  fixSHA,
		Category:   "bugfix",
		Language:   "go",
		Lang:       "en",
		Task:       "Make the retry delay exponential.",
		Oracle: Oracle{
			Files:   []string{"queue/retry_test.go"},
			Command: "go test ./queue/",
		},
		Timeout: Duration(30 * time.Minute),
		path:    "/tasks/go-queue-retry.yaml",
	}
}

var goDefaults = Defaults{
	TestGlobs:    []string{"**/*_test.go"},
	SetupCommand: "go mod download",
	TestCommand:  "go test ./...",
}

func validQuestion() Task {
	t := validGo()
	t.Category = "question"
	t.Oracle = Oracle{}
	t.AnswerChecks = []string{`retry_test\.go`, `exponential|backoff`}
	t.ReferenceAnswer = "The exponential backoff is tested in retry_test.go."
	return t
}

func TestValidateAccepts(t *testing.T) {
	tests := []struct {
		name string
		task Task
		d    Defaults
	}{
		{"go task relying on defaults", validGo(), goDefaults},
		{"question", validQuestion(), goDefaults},
		{"draft is not a validation error", func() Task { x := validGo(); x.Draft = true; return x }(), goDefaults},
		{"unknown language with everything set", func() Task {
			x := validGo()
			x.Language = "rust"
			x.TestGlobs = []string{"tests/**"}
			x.SetupCommand = "cargo fetch"
			x.TestCommand = "cargo test"
			x.Oracle.Files = []string{"tests/retry.rs"}
			return x
		}(), Defaults{}},
		{"task set globs override the defaults", func() Task {
			x := validGo()
			x.TestGlobs = []string{"queue/**"}
			x.Oracle.Files = []string{"queue/retry_test.go"}
			return x
		}(), goDefaults},
		{"task without a file", func() Task { x := validGo(); x.path = ""; x.ID = "anything"; return x }(), goDefaults},
		{"no pair", func() Task { x := validGo(); x.Pair = ""; return x }(), goDefaults},
		{"pair", func() Task { x := validGo(); x.Pair = "go-queue-retry-ru"; return x }(), goDefaults},
		{"russian task", func() Task { x := validGo(); x.Lang = "ru"; return x }(), goDefaults},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.task.Validate(tt.d); err != nil {
				t.Fatalf("Validate = %v", err)
			}
		})
	}
}

func TestValidateProblems(t *testing.T) {
	mod := func(f func(*Task)) Task {
		x := validGo()
		f(&x)
		return x
	}
	modQ := func(f func(*Task)) Task {
		x := validQuestion()
		f(&x)
		return x
	}

	tests := []struct {
		name string
		task Task
		d    Defaults
		want string
	}{
		{"empty id", mod(func(x *Task) { x.ID = "" }), goDefaults, "id is required"},
		{"uppercase id", mod(func(x *Task) { x.ID = "Go-Queue" }), goDefaults, `id "Go-Queue" must match [a-z0-9-]+`},
		{"underscore id", mod(func(x *Task) { x.ID = "go_queue" }), goDefaults, `id "go_queue" must match`},
		{"id differs from the file name", mod(func(x *Task) { x.ID = "go-other" }), goDefaults, `id "go-other" must equal the file name "go-queue-retry"`},
		{"bad pair", mod(func(x *Task) { x.Pair = "Go Queue" }), goDefaults, `pair "Go Queue" must match`},
		{"pair is the task itself", mod(func(x *Task) { x.Pair = x.ID }), goDefaults, "must be another task"},
		{"missing repo", mod(func(x *Task) { x.Repo = "" }), goDefaults, "repo is required"},
		{"missing task text", mod(func(x *Task) { x.Task = " \n" }), goDefaults, "task is required"},
		{"short base commit", mod(func(x *Task) { x.BaseCommit = "3f2c" }), goDefaults, `base_commit "3f2c" must be 40`},
		{"non-hex base commit", mod(func(x *Task) { x.BaseCommit = strings.Repeat("g", 40) }), goDefaults, "base_commit"},
		{"uppercase base commit", mod(func(x *Task) { x.BaseCommit = strings.ToUpper(baseSHA) }), goDefaults, "base_commit"},
		{"short fix commit", mod(func(x *Task) { x.FixCommit = "9a1b" }), goDefaults, `fix_commit "9a1b" must be 40`},
		{"non-hex fix commit", mod(func(x *Task) { x.FixCommit = strings.Repeat("z", 40) }), goDefaults, "fix_commit"},
		{"missing commits", mod(func(x *Task) { x.BaseCommit, x.FixCommit = "", "" }), goDefaults, `base_commit "" must be 40`},
		{"bad category", mod(func(x *Task) { x.Category = "chore" }), goDefaults, `category "chore" must be one of bugfix, feature, refactor, question`},
		{"missing category", mod(func(x *Task) { x.Category = "" }), goDefaults, "category"},
		{"bad lang", mod(func(x *Task) { x.Lang = "de" }), goDefaults, `lang "de" must be en or ru`},
		{"missing lang", mod(func(x *Task) { x.Lang = "" }), goDefaults, "lang"},
		{"missing language", mod(func(x *Task) { x.Language = "" }), goDefaults, "language is required"},
		{"unknown language without anything", mod(func(x *Task) { x.Language = "rust" }), Defaults{}, `language "rust" has no defaults for test_globs, setup_command, test_command`},
		{"unknown language without test_globs", mod(func(x *Task) {
			x.Language, x.SetupCommand, x.TestCommand = "rust", "cargo fetch", "cargo test"
		}), Defaults{}, `no defaults for test_globs:`},
		{"unknown language without setup_command", mod(func(x *Task) {
			x.Language, x.TestGlobs, x.TestCommand = "rust", []string{"tests/**"}, "cargo test"
		}), Defaults{}, `no defaults for setup_command:`},
		{"unknown language without test_command", mod(func(x *Task) {
			x.Language, x.TestGlobs, x.SetupCommand = "rust", []string{"tests/**"}, "cargo fetch"
		}), Defaults{}, `no defaults for test_command:`},
		{"zero timeout", mod(func(x *Task) { x.Timeout = 0 }), goDefaults, "timeout is required and must be positive"},
		{"negative timeout", mod(func(x *Task) { x.Timeout = Duration(-time.Minute) }), goDefaults, "timeout is required and must be positive"},
		{"empty oracle files", mod(func(x *Task) { x.Oracle.Files = nil }), goDefaults, "oracle.files must not be empty"},
		{"oracle file outside test_globs", mod(func(x *Task) { x.Oracle.Files = []string{"queue/retry.go"} }), goDefaults, `oracle.files entry "queue/retry.go" matches none of test_globs`},
		{"one of several oracle files outside test_globs", mod(func(x *Task) {
			x.Oracle.Files = []string{"queue/retry_test.go", "queue/helper.go"}
		}), goDefaults, `"queue/helper.go" matches none`},
		{"oracle file outside the task's own test_globs", mod(func(x *Task) {
			x.TestGlobs = []string{"tests/**"}
		}), goDefaults, `matches none of test_globs [tests/**]`},
		{"absolute oracle file", mod(func(x *Task) { x.Oracle.Files = []string{"/etc/passwd_test.go"} }), goDefaults, "clean relative path"},
		{"oracle file escaping the repository", mod(func(x *Task) { x.Oracle.Files = []string{"../x_test.go"} }), goDefaults, "clean relative path"},
		{"missing oracle command", mod(func(x *Task) { x.Oracle.Command = "" }), goDefaults, "oracle.command is required"},
		{"question without answer_checks", modQ(func(x *Task) { x.AnswerChecks = nil }), goDefaults, "answer_checks must not be empty"},
		{"answer check that does not compile", modQ(func(x *Task) { x.AnswerChecks = []string{`(unclosed`} }), goDefaults, `answer_checks entry "(unclosed" does not compile`},
		{"empty answer check", modQ(func(x *Task) { x.AnswerChecks = []string{""} }), goDefaults, "answer_checks entry must not be empty"},
		{"question without reference_answer", modQ(func(x *Task) { x.ReferenceAnswer = "" }), goDefaults, "reference_answer is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.task.Validate(tt.d)
			if err == nil {
				t.Fatal("Validate succeeded")
			}
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("error is %T, want *ValidationError", err)
			}
			found := false
			for _, p := range verr.Problems {
				if strings.Contains(p, tt.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("problems %q do not contain %q", verr.Problems, tt.want)
			}
		})
	}
}

func TestValidateCollectsEveryProblem(t *testing.T) {
	bad := Task{path: "/tasks/x.yaml"}

	err := bad.Validate(Defaults{})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("Validate = %v", err)
	}
	for _, want := range []string{"id is required", "repo is required", "task is required", "base_commit", "fix_commit", "category", "lang ", "language is required", "timeout", "oracle.files", "oracle.command"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if !strings.Contains(err.Error(), "/tasks/x.yaml") {
		t.Errorf("error %q does not name the file", err)
	}
}

func TestValidateIgnoresOracleOfQuestion(t *testing.T) {
	q := validQuestion()
	q.Oracle = Oracle{}
	if err := q.Validate(goDefaults); err != nil {
		t.Fatalf("a question needs no oracle: %v", err)
	}
}

func TestValidateDoesNotModifyTask(t *testing.T) {
	x := validGo()
	if err := x.Validate(goDefaults); err != nil {
		t.Fatal(err)
	}
	if x.TestGlobs != nil || x.SetupCommand != "" || x.TestCommand != "" {
		t.Fatalf("Validate filled the caller's task: %+v", x)
	}
}

func TestDefaultsApply(t *testing.T) {
	x := Task{SetupCommand: "make deps"}
	missing := goDefaults.Apply(&x)
	if len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}
	if x.SetupCommand != "make deps" {
		t.Errorf("override lost: %q", x.SetupCommand)
	}
	if x.TestCommand != "go test ./..." || !reflect.DeepEqual(x.TestGlobs, goDefaults.TestGlobs) {
		t.Errorf("defaults not applied: %+v", x)
	}

	// The filled slice must not alias the table.
	x.TestGlobs[0] = "changed"
	if goDefaults.TestGlobs[0] != "**/*_test.go" {
		t.Error("Apply aliases the defaults' slice")
	}

	empty := Task{}
	if got := (Defaults{}).Apply(&empty); !reflect.DeepEqual(got, []string{"test_globs", "setup_command", "test_command"}) {
		t.Errorf("missing = %v", got)
	}
}

func TestSHA256(t *testing.T) {
	// echo -n abc | sha256sum
	const abc = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

	if got := SHA256Bytes([]byte("abc")); got != abc {
		t.Fatalf("SHA256Bytes = %s", got)
	}

	file := writeTask(t, "x.yaml", "abc")
	got, err := SHA256(file)
	if err != nil {
		t.Fatal(err)
	}
	if got != abc {
		t.Fatalf("SHA256 = %s", got)
	}

	if _, err := SHA256(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("SHA256 of a missing file succeeded")
	}
}
