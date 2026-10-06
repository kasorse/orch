// Package testutil is what the tests of several packages share: throwaway git repositories,
// a tiny shell project to run tasks against, and the probe that skips a test where bwrap
// cannot run. Only tests import it.
package testutil

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"orchbench/internal/sandbox"
	"orchbench/internal/task"
)

// Delete as the content of a file in Apply and Repo.Commit removes the file.
const Delete = "\x00delete"

// Repo is a git repository in a temporary directory.
type Repo struct {
	Dir string
	t   testing.TB
}

// NewRepo creates an empty repository on branch main with an identity and without hooks or
// signing, whatever the developer's global git configuration says.
func NewRepo(t testing.TB) *Repo {
	t.Helper()
	r := &Repo{Dir: t.TempDir(), t: t}
	r.Git("-c", "init.defaultBranch=main", "init", "-q")
	r.Git("config", "user.name", "test")
	r.Git("config", "user.email", "test@example.com")
	r.Git("config", "commit.gpgsign", "false")
	r.Git("config", "core.hooksPath", "/dev/null")
	return r
}

// Git runs git in the repository, fails the test on an error and returns the trimmed stdout.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// Commit writes the files (path → content, Delete to remove one), commits everything and
// returns the new commit.
func (r *Repo) Commit(msg string, files map[string]string) string {
	r.t.Helper()
	Apply(r.t, r.Dir, files)
	r.Git("add", "-A")
	r.Git("commit", "-q", "--allow-empty", "-m", msg)
	return r.Git("rev-parse", "HEAD")
}

// Apply writes files (path → content, Delete to remove one) under dir.
func Apply(t testing.TB, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if content == Delete {
			if err := os.RemoveAll(p); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The files of SHProject. mul is wrong at the base commit and its test, written to match,
// expects the wrong answer; the fix corrects both.
const (
	AddTest = `. ./calc.sh
[ "$(add 2 3)" = 5 ] || exit 1
`
	MulTestOld = `. ./calc.sh
[ "$(mul 2 3)" = 5 ] || exit 1
`
	MulTestNew = `. ./calc.sh
[ "$(mul 2 3)" = 6 ] || exit 1
`
	CalcBuggy = `add() { echo $(($1 + $2)); }
mul() { echo $(($1 + $2)); }
`
	CalcFixed = `add() { echo $(($1 + $2)); }
mul() { echo $(($1 * $2)); }
`
	// SHTestCommand runs every tests/*.sh with sh.
	SHTestCommand = `for f in tests/*.sh; do sh "$f" || exit 1; done`
)

// SHProject is a repository with no toolchain: a library of two shell functions and one shell
// test per function. History: Root, Base (mul is wrong), Fix (mul is right and its test
// changed), Later (something unrelated).
type SHProject struct {
	Repo                   *Repo
	Root, Base, Fix, Later string
}

// NewSHProject builds the project in a temporary directory.
func NewSHProject(t testing.TB) *SHProject {
	t.Helper()
	r := NewRepo(t)
	p := &SHProject{Repo: r}
	p.Root = r.Commit("root", map[string]string{"README": "calc\n"})
	p.Base = r.Commit("base", map[string]string{
		"calc.sh":           CalcBuggy,
		"tests/test_add.sh": AddTest,
		"tests/test_mul.sh": MulTestOld,
	})
	p.Fix = r.Commit("fix", map[string]string{
		"calc.sh":           CalcFixed,
		"tests/test_mul.sh": MulTestNew,
	})
	p.Later = r.Commit("later", map[string]string{"later.txt": "after the fix\n"})
	return p
}

// Task is a valid code task over the project: tests/test_mul.sh is the oracle.
func (p *SHProject) Task(id string) task.Task {
	return task.Task{
		ID:           id,
		Repo:         p.Repo.Dir,
		BaseCommit:   p.Base,
		FixCommit:    p.Fix,
		Category:     task.CategoryBugfix,
		Language:     "sh",
		Lang:         "en",
		Task:         "mul returns the sum of its arguments instead of the product; fix it.",
		SetupCommand: "true",
		TestCommand:  SHTestCommand,
		TestGlobs:    []string{"tests/**"},
		Oracle:       task.Oracle{Files: []string{"tests/test_mul.sh"}, Command: "sh tests/test_mul.sh"},
		Timeout:      task.Duration(time.Minute),
	}
}

// Question is a valid question task over the project.
func (p *SHProject) Question(id string) task.Task {
	return task.Task{
		ID:              id,
		Repo:            p.Repo.Dir,
		BaseCommit:      p.Base,
		FixCommit:       p.Fix,
		Category:        task.CategoryQuestion,
		Language:        "sh",
		Lang:            "en",
		Task:            "Where is multiplication implemented, and why does it give wrong results?",
		SetupCommand:    "true",
		TestCommand:     SHTestCommand,
		TestGlobs:       []string{"tests/**"},
		Timeout:         task.Duration(time.Minute),
		AnswerChecks:    []string{`calc\.sh`, `mul.*(adds|\+)`},
		ReferenceAnswer: "It is the mul function in calc.sh: it adds its arguments instead of multiplying them.",
	}
}

// WriteTask writes the task as dir/<id>.yaml and returns the path.
func WriteTask(t testing.TB, dir string, tk task.Task) string {
	t.Helper()
	data, err := yaml.Marshal(tk)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, tk.ID+".yaml")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

var (
	probeOnce sync.Once
	probeErr  error
)

// RequireBwrap skips the test where bwrap cannot create a sandbox, which is where AppArmor
// blocks unprivileged user namespaces. The probe runs once per test binary.
func RequireBwrap(t testing.TB) {
	t.Helper()
	probeOnce.Do(func() { probeErr = sandbox.Probe(context.Background()) })
	if probeErr == nil {
		return
	}
	detail := probeErr.Error()
	var pe *sandbox.ProbeError
	if errors.As(probeErr, &pe) {
		detail = pe.Detail
	}
	t.Skipf("bwrap cannot create a sandbox here: %s (on Ubuntu 24.04 and later AppArmor may block it; see sandbox.Probe)", detail)
}
