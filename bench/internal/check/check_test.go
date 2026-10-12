package check

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"orchbench/internal/cache"
	"orchbench/internal/task"
	"orchbench/internal/testutil"
	"orchbench/internal/workspace"
)

const (
	// CalcBrokenAdd fixes mul and breaks add: the oracle passes, the suite does not.
	calcBrokenAdd = `add() { echo 0; }
mul() { echo $(($1 * $2)); }
`
	weakTest   = "exit 0\n"
	failing    = "exit 1\n"
	newPassing = ". ./calc.sh\n[ \"$(mul 3 4)\" = 12 ] || exit 1\n"
)

func setup(t *testing.T) (*testutil.SHProject, *workspace.Manager) {
	t.Helper()
	return testutil.NewSHProject(t), workspace.New(cache.Dir(t.TempDir()), nil)
}

func create(t *testing.T, m *workspace.Manager, tk task.Task, commit string) *workspace.Workspace {
	t.Helper()
	ws, err := m.Create(context.Background(), tk, commit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Remove() })
	return ws
}

func wsGit(t *testing.T, ws *workspace.Workspace, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = ws.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestDecide(t *testing.T) {
	tests := []struct {
		oracle, suite int
		pass          bool
		reason        string
	}{
		{0, 0, true, ""},
		{1, 0, false, ReasonOracle},
		{1, 1, false, ReasonOracle},
		{-1, 0, false, ReasonOracle},
		{0, 2, false, ReasonRegression},
		{0, -1, false, ReasonRegression},
	}
	for _, tt := range tests {
		r := Decide(tt.oracle, tt.suite, 3)
		if r.Pass != tt.pass || r.FailReason != tt.reason || r.OracleExit != tt.oracle || r.SuiteExit != tt.suite || r.TestsReset != 3 {
			t.Errorf("Decide(%d, %d) = %+v", tt.oracle, tt.suite, r)
		}
	}
}

func TestFinal(t *testing.T) {
	testutil.RequireBwrap(t)

	tests := []struct {
		name   string
		change func(t *testing.T, ws *workspace.Workspace)
		pass   bool
		reason string
		reset  int
		// present: files that must exist with this content afterwards; absent: files that
		// must not.
		present map[string]string
		absent  []string
		// before changes the tree before the config runs (what setup_command generated).
		before func(t *testing.T, ws *workspace.Workspace)
	}{
		{
			name:    "correct fix; the oracle file's old expectation at base is replaced",
			change:  files(map[string]string{"calc.sh": testutil.CalcFixed}),
			pass:    true,
			present: map[string]string{"tests/test_mul.sh": testutil.MulTestNew},
		},
		{
			name:   "no change",
			change: files(nil),
			reason: ReasonOracle,
		},
		{
			name:   "fix plus a broken unrelated function",
			change: files(map[string]string{"calc.sh": calcBrokenAdd}),
			reason: ReasonRegression,
		},
		{
			name: "a weakened existing test is reset and the regression caught",
			change: files(map[string]string{
				"calc.sh":           calcBrokenAdd,
				"tests/test_add.sh": weakTest,
			}),
			reason:  ReasonRegression,
			reset:   1,
			present: map[string]string{"tests/test_add.sh": testutil.AddTest},
		},
		{
			name: "a deleted test is restored",
			change: files(map[string]string{
				"calc.sh":           calcBrokenAdd,
				"tests/test_add.sh": testutil.Delete,
			}),
			reason:  ReasonRegression,
			reset:   1,
			present: map[string]string{"tests/test_add.sh": testutil.AddTest},
		},
		{
			name: "the model's own edit of the oracle file counts as reset and is overwritten",
			change: files(map[string]string{
				"calc.sh":           testutil.CalcFixed,
				"tests/test_mul.sh": testutil.MulTestNew,
			}),
			pass:  true,
			reset: 1,
		},
		{
			name: "an added test is removed: the model's own tests are not the measure",
			change: files(map[string]string{
				"calc.sh":           testutil.CalcFixed,
				"tests/test_new.sh": failing,
			}),
			pass:   true,
			reset:  1,
			absent: []string{"tests/test_new.sh"},
		},
		{
			name:    "a test file that was there before the config ran stays",
			before:  files(map[string]string{"tests/test_generated.sh": newPassing}),
			change:  files(map[string]string{"calc.sh": testutil.CalcFixed}),
			pass:    true,
			present: map[string]string{"tests/test_generated.sh": newPassing},
		},
		{
			name: "a weakened test committed and a deleted one staged are reset",
			change: func(t *testing.T, ws *workspace.Workspace) {
				testutil.Apply(t, ws.Dir, map[string]string{
					"calc.sh":           calcBrokenAdd,
					"tests/test_add.sh": weakTest,
				})
				wsGit(t, ws, "commit", "-q", "-am", "the model's snapshot")
				wsGit(t, ws, "rm", "-q", "tests/test_mul.sh")
			},
			reason:  ReasonRegression,
			reset:   2,
			present: map[string]string{"tests/test_add.sh": testutil.AddTest},
		},
		{
			name: "a test made executable is reset",
			change: func(t *testing.T, ws *workspace.Workspace) {
				testutil.Apply(t, ws.Dir, map[string]string{"calc.sh": testutil.CalcFixed})
				if err := os.Chmod(filepath.Join(ws.Dir, "tests/test_add.sh"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			pass:  true,
			reset: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, m := setup(t)
			tk := p.Task("fixture")
			ws := create(t, m, tk, p.Base)
			if tt.before != nil {
				tt.before(t, ws)
			}
			beforeConfig, err := TestFiles(ws.Dir, tk)
			if err != nil {
				t.Fatal(err)
			}
			tt.change(t, ws)

			logs := t.TempDir()
			r, err := Final(context.Background(), ws, tk, logs, beforeConfig)
			if err != nil {
				t.Fatal(err)
			}
			if r.Pass != tt.pass || r.FailReason != tt.reason || r.TestsReset != tt.reset {
				t.Fatalf("result %+v, want pass=%v reason=%q tests_reset=%d", r, tt.pass, tt.reason, tt.reset)
			}
			if r.Pass && (r.OracleExit != 0 || r.SuiteExit != 0) {
				t.Errorf("a pass with exits %d/%d", r.OracleExit, r.SuiteExit)
			}
			for name, want := range tt.present {
				got, err := os.ReadFile(filepath.Join(ws.Dir, name))
				if err != nil || string(got) != want {
					t.Errorf("%s = %q (%v), want %q", name, got, err, want)
				}
			}
			for _, name := range tt.absent {
				if _, err := os.Lstat(filepath.Join(ws.Dir, name)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s still exists (%v)", name, err)
				}
			}
			for _, log := range []string{"oracle.log", "suite.log"} {
				data, err := os.ReadFile(filepath.Join(logs, log))
				if err != nil || !bytes.Contains(data, []byte("# exit ")) {
					t.Errorf("%s: %q, %v", log, data, err)
				}
			}
		})
	}
}

func files(f map[string]string) func(*testing.T, *workspace.Workspace) {
	return func(t *testing.T, ws *workspace.Workspace) { testutil.Apply(t, ws.Dir, f) }
}

// A model can put a symlink where a test file or a test directory was. The reset replaces it
// and never writes through it.
func TestResetDoesNotFollowSymlinks(t *testing.T) {
	p, m := setup(t)
	tk := p.Task("fixture")
	ws := create(t, m, tk, p.Base)

	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(ws.Dir, "tests")); err != nil {
		t.Fatal(err)
	}
	// tests/ → outside, with tests/test_add.sh a symlink to the victim.
	if err := os.Symlink(outside, filepath.Join(ws.Dir, "tests")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(outside, "test_add.sh")); err != nil {
		t.Fatal(err)
	}

	n, err := ResetTests(context.Background(), ws, tk, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("tests_reset = %d, want 2", n)
	}
	if data, _ := os.ReadFile(victim); string(data) != "keep\n" {
		t.Errorf("the reset wrote through a symlink: victim = %q", data)
	}
	info, err := os.Lstat(filepath.Join(ws.Dir, "tests"))
	if err != nil || !info.IsDir() {
		t.Fatalf("tests is not a directory again: %v %v", info, err)
	}
	got, _ := os.ReadFile(filepath.Join(ws.Dir, "tests/test_add.sh"))
	if string(got) != testutil.AddTest {
		t.Errorf("test_add.sh = %q", got)
	}
}

func TestResetOfAnUnchangedTreeCountsNothing(t *testing.T) {
	p, m := setup(t)
	tk := p.Task("fixture")
	ws := create(t, m, tk, p.Base)

	n, err := ResetTests(context.Background(), ws, tk, nil)
	if err != nil || n != 0 {
		t.Fatalf("ResetTests = %d, %v", n, err)
	}
}

func TestWriteOracleNeedsTheFileAtFix(t *testing.T) {
	p, m := setup(t)
	tk := p.Task("fixture")
	tk.Oracle.Files = []string{"tests/test_mul.sh", "tests/nope.sh"}
	ws := create(t, m, tk, p.Base)

	err := WriteOracle(context.Background(), ws, tk)
	var missing *MissingOracleFileError
	if !errors.As(err, &missing) || missing.Path != "tests/nope.sh" {
		t.Fatalf("err = %v, want a MissingOracleFileError for tests/nope.sh", err)
	}
}

// The fix commit is read from the mirror; the workspace never gets its objects.
func TestWriteOracleLeavesTheWorkspaceWithoutTheFix(t *testing.T) {
	p, m := setup(t)
	tk := p.Task("fixture")
	ws := create(t, m, tk, p.Base)

	if err := WriteOracle(context.Background(), ws, tk); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(ws.Dir, "tests/test_mul.sh"))
	if string(got) != testutil.MulTestNew {
		t.Errorf("test_mul.sh = %q", got)
	}
	cmd := exec.Command("git", "cat-file", "-e", p.Fix)
	cmd.Dir = ws.Dir
	if cmd.Run() == nil {
		t.Error("the workspace has the fix commit")
	}
}

func TestFinalTimeoutIsARedCommand(t *testing.T) {
	testutil.RequireBwrap(t)

	p, m := setup(t)
	tk := p.Task("fixture")
	tk.Oracle.Command = "sleep 30"
	tk.Timeout = task.Duration(300 * time.Millisecond)
	ws := create(t, m, tk, p.Base)
	testutil.Apply(t, ws.Dir, map[string]string{"calc.sh": testutil.CalcFixed})

	logs := t.TempDir()
	r, err := Final(context.Background(), ws, tk, logs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Pass || r.FailReason != ReasonOracle || r.OracleExit != -1 || r.SuiteExit != 0 {
		t.Fatalf("result %+v", r)
	}
	if data, _ := os.ReadFile(filepath.Join(logs, "oracle.log")); !bytes.Contains(data, []byte("timed out")) {
		t.Errorf("oracle.log = %q", data)
	}
}

func TestAnswer(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		change map[string]string
		pass   bool
		reason string
		failed []string
	}{
		{
			name:   "every check matches, case-insensitively",
			answer: "MUL in CALC.SH adds the numbers.",
			pass:   true,
		},
		{
			name:   "one check missing",
			answer: "It is in calc.sh.",
			reason: ReasonAnswer,
			failed: []string{`mul.*(adds|\+)`},
		},
		{
			name:   "the tree changed",
			answer: "mul in calc.sh adds",
			change: map[string]string{"calc.sh": testutil.CalcFixed},
			reason: ReasonTreeChanged,
		},
		{
			name:   "an untracked file is a change",
			answer: "mul in calc.sh adds",
			change: map[string]string{"notes.txt": "x\n"},
			reason: ReasonTreeChanged,
		},
		{
			name:   "the orchestrator's run directory is not a change",
			answer: "mul in calc.sh adds",
			change: map[string]string{".orchestrator/runs/x/run.json": "{}\n"},
			pass:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, m := setup(t)
			q := p.Question("q")
			ws := create(t, m, q, p.Base)
			testutil.Apply(t, ws.Dir, tt.change)

			r, err := Answer(context.Background(), q, tt.answer, ws)
			if err != nil {
				t.Fatal(err)
			}
			if r.Pass != tt.pass || r.FailReason != tt.reason || !slices.Equal(r.AnswerChecksFailed, tt.failed) {
				t.Fatalf("result %+v, want pass=%v reason=%q failed=%q", r, tt.pass, tt.reason, tt.failed)
			}
			if r.OracleExit != -1 || r.SuiteExit != -1 {
				t.Errorf("a question reports exits %d/%d", r.OracleExit, r.SuiteExit)
			}
		})
	}
}

func TestMatchChecks(t *testing.T) {
	checks := []string{`\bfoo\b`, `BAR`, `(`}
	got := MatchChecks(checks, "a foo and a bar")
	if !slices.Equal(got, []string{`(`}) {
		t.Fatalf("MatchChecks = %q", got)
	}
	if got := MatchChecks(nil, "x"); got != nil {
		t.Fatalf("no checks: %q", got)
	}
}
