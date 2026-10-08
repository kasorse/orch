// Package task is the benchmark's task file (specs/benchmark/SPEC.md §5.1): strict loading,
// schema-only validation and the file hash a result line records. Nothing here runs git or
// any other process; whether a task is sound against its repository is `orchbench validate`.
package task

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	CategoryBugfix   = "bugfix"
	CategoryFeature  = "feature"
	CategoryRefactor = "refactor"
	CategoryQuestion = "question"
)

// Task is one task file. Field names are the YAML keys of §5.1, §5.3 and §5.4.
type Task struct {
	ID              string   `yaml:"id"`
	Repo            string   `yaml:"repo"`
	BaseCommit      string   `yaml:"base_commit"`
	FixCommit       string   `yaml:"fix_commit"`
	Category        string   `yaml:"category"`
	Language        string   `yaml:"language"`
	Lang            string   `yaml:"lang"`
	Pair            string   `yaml:"pair"`
	Task            string   `yaml:"task"`
	ExpectedStages  []string `yaml:"expected_stages"`
	SetupCommand    string   `yaml:"setup_command"`
	TestCommand     string   `yaml:"test_command"`
	TestGlobs       []string `yaml:"test_globs"`
	Oracle          Oracle   `yaml:"oracle"`
	Sandbox         Sandbox  `yaml:"sandbox"`
	Timeout         Duration `yaml:"timeout"`
	AnswerChecks    []string `yaml:"answer_checks"`
	ReferenceAnswer string   `yaml:"reference_answer"`
	Draft           bool     `yaml:"draft"`

	// path is the file Load read; empty for a Task built in code.
	path string
}

// Oracle is the hidden test: files copied from fix_commit over the final tree, and the
// command that must turn red → green.
type Oracle struct {
	Files   []string `yaml:"files"`
	Command string   `yaml:"command"`
}

// Sandbox lists the binds for setup, test and oracle commands, as test.sandbox does in the
// orchestrator's config.
type Sandbox struct {
	RoBinds []string `yaml:"ro_binds"`
	RwBinds []string `yaml:"rw_binds"`
}

// Duration is a time.Duration written as "30m" in YAML.
type Duration time.Duration

// UnmarshalYAML parses the value with time.ParseDuration.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("timeout: %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("timeout: %w", err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML writes the duration back the way it is read.
func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// IsDraft reports whether the file is an unfinished draft written by `orchbench mine`.
// A draft loads and validates; refusing it is the caller's job (`validate`, `run`).
func (t Task) IsDraft() bool { return t.Draft }

// Path is the file the task was loaded from, empty for a Task built in code.
func (t Task) Path() string { return t.path }

// FileID is the loaded file's name without its extension, which `id` must equal.
func (t Task) FileID() string {
	if t.path == "" {
		return ""
	}
	name := filepath.Base(t.path)
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// Load reads and strictly decodes one task file: an unknown key, an empty file or more than
// one YAML document is an error. It does not validate; call Validate.
func Load(file string) (Task, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return Task{}, err
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var t Task
	if err := dec.Decode(&t); err != nil {
		if errors.Is(err, io.EOF) {
			return Task{}, fmt.Errorf("%s: empty task file", file)
		}
		return Task{}, fmt.Errorf("%s: %w", file, err)
	}

	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case err == nil:
		return Task{}, fmt.Errorf("%s: more than one YAML document", file)
	case !errors.Is(err, io.EOF):
		return Task{}, fmt.Errorf("%s: %w", file, err)
	}

	t.path = file
	return t, nil
}

// SHA256 returns the hex SHA-256 of a task file's bytes: the hash a result line records.
func SHA256(file string) (string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	return SHA256Bytes(data), nil
}

// SHA256Bytes returns the hex SHA-256 of data.
func SHA256Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Defaults are the per-language values a task may leave out (SPEC.md §5.5). It lives here,
// not in package lang, so that lang can import task.
type Defaults struct {
	TestGlobs    []string
	SetupCommand string
	TestCommand  string
}

// Apply fills the fields t leaves empty from d, keeping what t sets, and returns the YAML
// names of the fields that are still empty.
func (d Defaults) Apply(t *Task) (missing []string) {
	if len(t.TestGlobs) == 0 {
		t.TestGlobs = append([]string(nil), d.TestGlobs...)
	}
	if t.SetupCommand == "" {
		t.SetupCommand = d.SetupCommand
	}
	if t.TestCommand == "" {
		t.TestCommand = d.TestCommand
	}

	if len(t.TestGlobs) == 0 {
		missing = append(missing, "test_globs")
	}
	if t.SetupCommand == "" {
		missing = append(missing, "setup_command")
	}
	if t.TestCommand == "" {
		missing = append(missing, "test_command")
	}
	return missing
}

// ValidationError lists every problem found in one task, so a task file is fixed in one go.
type ValidationError struct {
	Path     string
	Problems []string
}

func (e *ValidationError) Error() string {
	prefix := "invalid task"
	if e.Path != "" {
		prefix += " " + e.Path
	}
	return prefix + ": " + strings.Join(e.Problems, "; ")
}

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9-]+$`)
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Validate checks the task against the schema of SPEC.md §5.1 and returns a
// *ValidationError holding every problem, or nil. d are the defaults of the task's language
// (the zero value for a language without any); they fill test_globs, setup_command and
// test_command before they are checked. Validate does not modify t and runs nothing.
func (t Task) Validate(d Defaults) error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	switch {
	case t.ID == "":
		add("id is required")
	case !idPattern.MatchString(t.ID):
		add("id %q must match [a-z0-9-]+", t.ID)
	}
	if fileID := t.FileID(); fileID != "" && t.ID != "" && t.ID != fileID {
		add("id %q must equal the file name %q", t.ID, fileID)
	}
	if t.Pair != "" {
		if !idPattern.MatchString(t.Pair) {
			add("pair %q must match [a-z0-9-]+", t.Pair)
		} else if t.Pair == t.ID {
			add("pair %q must be another task", t.Pair)
		}
	}

	if t.Repo == "" {
		add("repo is required")
	}
	if strings.TrimSpace(t.Task) == "" {
		add("task is required")
	}
	if !commitPattern.MatchString(t.BaseCommit) {
		add("base_commit %q must be 40 lowercase hex characters", t.BaseCommit)
	}
	if !commitPattern.MatchString(t.FixCommit) {
		add("fix_commit %q must be 40 lowercase hex characters", t.FixCommit)
	}

	switch t.Category {
	case CategoryBugfix, CategoryFeature, CategoryRefactor, CategoryQuestion:
	default:
		add("category %q must be one of bugfix, feature, refactor, question", t.Category)
	}
	switch t.Lang {
	case "en", "ru":
	default:
		add("lang %q must be en or ru", t.Lang)
	}

	resolved := t
	if t.Language == "" {
		add("language is required")
	} else if missing := d.Apply(&resolved); len(missing) > 0 {
		add("language %q has no defaults for %s: set them in the task", t.Language, strings.Join(missing, ", "))
	}

	if t.Timeout <= 0 {
		add("timeout is required and must be positive, e.g. 30m")
	}

	if t.Category == CategoryQuestion {
		problems = append(problems, t.validateQuestion()...)
	} else {
		problems = append(problems, t.validateOracle(resolved.TestGlobs)...)
	}

	if len(problems) == 0 {
		return nil
	}
	return &ValidationError{Path: t.path, Problems: problems}
}

// validateOracle checks what a task with a code change needs: oracle files, all of them test
// files, and the command that runs them.
func (t Task) validateOracle(testGlobs []string) []string {
	var problems []string

	if len(t.Oracle.Files) == 0 {
		problems = append(problems, "oracle.files must not be empty")
	}
	for _, f := range t.Oracle.Files {
		if f == "" || path.IsAbs(f) || path.Clean(f) != f || f == ".." || strings.HasPrefix(f, "../") {
			problems = append(problems, fmt.Sprintf("oracle.files entry %q must be a clean relative path inside the repository", f))
			continue
		}
		if len(testGlobs) > 0 && !matchesAny(testGlobs, f) {
			problems = append(problems, fmt.Sprintf("oracle.files entry %q matches none of test_globs %v", f, testGlobs))
		}
	}
	if strings.TrimSpace(t.Oracle.Command) == "" {
		problems = append(problems, "oracle.command is required")
	}
	return problems
}

// validateQuestion checks what a question needs: answer checks that compile and a reference
// answer. That the checks match the reference answer is validate's job (§6 step 7).
func (t Task) validateQuestion() []string {
	var problems []string

	if len(t.AnswerChecks) == 0 {
		problems = append(problems, "answer_checks must not be empty for a question")
	}
	for _, c := range t.AnswerChecks {
		if c == "" {
			problems = append(problems, "answer_checks entry must not be empty")
			continue
		}
		if _, err := regexp.Compile("(?i)" + c); err != nil {
			problems = append(problems, fmt.Sprintf("answer_checks entry %q does not compile: %v", c, err))
		}
	}
	if strings.TrimSpace(t.ReferenceAnswer) == "" {
		problems = append(problems, "reference_answer is required for a question")
	}
	return problems
}

func matchesAny(globs []string, file string) bool {
	for _, g := range globs {
		if Match(g, file) {
			return true
		}
	}
	return false
}
