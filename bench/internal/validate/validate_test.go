package validate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchbench/internal/cache"
	"orchbench/internal/task"
	"orchbench/internal/testutil"
)

var fixedNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func options(dir cache.Dir, jobs int) Options {
	return Options{Cache: dir, Jobs: jobs, Now: func() time.Time { return fixedNow }}
}

// A case is one task file and where its validation must end.
type tcase struct {
	id    string
	make  func(p *testutil.SHProject, id string) task.Task
	raw   string // file content instead of make
	check string // "" for a valid task
	hint  string
}

func cases(t *testing.T) []tcase {
	counter := t.TempDir()
	return []tcase{
		{id: "valid-code", make: func(p *testutil.SHProject, id string) task.Task { return p.Task(id) }},
		{id: "valid-question", make: func(p *testutil.SHProject, id string) task.Task { return p.Question(id) }},
		{
			id: "draft",
			make: func(p *testutil.SHProject, id string) task.Task {
				tk := p.Task(id)
				tk.Draft = true
				return tk
			},
			check: CheckDraft, hint: "draft: rewrite the task and remove `draft`",
		},
		{
			id: "schema",
			make: func(p *testutil.SHProject, id string) task.Task {
				tk := p.Task(id)
				tk.Lang = "de"
				return tk
			},
			check: CheckSchema, hint: `lang "de" must be en or ru`,
		},
		{id: "unknown-key", raw: "id: unknown-key\nnope: 1\n", check: CheckSchema, hint: "field nope not found"},
		{
			id: "missing-commit",
			make: func(p *testutil.SHProject, id string) task.Task {
				tk := p.Task(id)
				tk.FixCommit = strings.Repeat("1", 40)
				return tk
			},
			check: CheckCommits, hint: "fix_commit " + strings.Repeat("1", 40) + " does not exist",
		},
		{
			id: "not-an-ancestor",
			make: func(p *testutil.SHProject, id string) task.Task {
				tk := p.Task(id)
				tk.BaseCommit, tk.FixCommit = tk.FixCommit, tk.BaseCommit
				return tk
			},
			check: CheckCommits, hint: "base_commit is not an ancestor of fix_commit",
		},
		{
			id: "setup-fails",
			make: func(p *testutil.SHProject, id string) task.Task {
				tk := p.Task(id)
				tk.SetupCommand = "exit 3"
				return tk
			},
			check: CheckSetup, hint: "setup_command exit 3",
		},
		{
			id: "setup-dirty",
			make: func(p *testutil.SHProject, id string) task.Task {
				tk := p.Task(id)
				tk.SetupCommand = "touch build.out"
				return tk
			},
			check: CheckClean, hint: "setup_command left the tree dirty (?? build.out): dependencies must land in ignored or bound paths",
		},
		{
			id: "red-at-base",
			make: func(p *testutil.SHProject, id string) task.Task {
				tk := p.Task(id)
				tk.TestCommand = "exit 1"
				return tk
			},
			check: CheckTestsBase, hint: "test_command red at base_commit: the pre-run test would refuse (exit 1)",
		},
		{
			id: "oracle-green-at-base",
			make: func(p *testutil.SHProject, id string) task.Task {
				tk := p.Task(id)
				tk.Oracle.Command = "true"
				return tk
			},
			check: CheckOracleBase, hint: "oracle green at base_commit: the hidden test checks nothing new",
		},
		{
			id: "oracle-file-missing",
			make: func(p *testutil.SHProject, id string) task.Task {
				tk := p.Task(id)
				tk.Oracle.Files = []string{"tests/nope.sh"}
				return tk
			},
			check: CheckOracleBase, hint: "oracle file tests/nope.sh does not exist at fix_commit",
		},
		{
			// Red at base (run 1), green at the fix's first run, red at its second.
			id: "oracle-flaky",
			make: func(p *testutil.SHProject, id string) task.Task {
				tk := p.Task(id)
				tk.Sandbox.RwBinds = []string{counter}
				tk.Oracle.Command = `n=$(cat ` + counter + `/n 2>/dev/null || echo 0); n=$((n+1)); echo $n > ` + counter + `/n; ` +
					`[ $n -ne 3 ] && sh tests/test_mul.sh`
				return tk
			},
			check: CheckOracleFix, hint: "oracle red at fix_commit (run 2 of 3): flaky or wrong oracle.command",
		},
		{
			// Green at base, red once calc.sh multiplies.
			id: "suite-red-at-fix",
			make: func(p *testutil.SHProject, id string) task.Task {
				tk := p.Task(id)
				tk.TestCommand = `sh tests/test_add.sh && ! grep -q '\*' calc.sh`
				return tk
			},
			check: CheckTestsFix, hint: "test_command red at fix_commit: the reference solution breaks the suite",
		},
		{
			// test_add.sh is the same at both commits: the model would see it.
			id: "leak",
			make: func(p *testutil.SHProject, id string) task.Task {
				tk := p.Task(id)
				tk.Oracle.Files = []string{"tests/test_add.sh"}
				tk.Oracle.Command = `sh tests/test_add.sh && grep -q '\*' calc.sh`
				return tk
			},
			check: CheckLeak, hint: "tests/test_add.sh already has fix_commit's content at base_commit: the model would see the answer",
		},
		{
			id: "question-misses-reference",
			make: func(p *testutil.SHProject, id string) task.Task {
				q := p.Question(id)
				q.AnswerChecks = append(q.AnswerChecks, `division`)
				return q
			},
			check: CheckAnswer, hint: `answer_checks "division" do not match reference_answer`,
		},
		{
			id: "question-trivial-check",
			make: func(p *testutil.SHProject, id string) task.Task {
				q := p.Question(id)
				q.AnswerChecks = append(q.AnswerChecks, `wrong results`)
				q.ReferenceAnswer += " That is why it gives wrong results."
				return q
			},
			check: CheckAnswer, hint: `answer_checks "wrong results" match the task text itself: they measure nothing`,
		},
	}
}

func writeCases(t *testing.T, p *testutil.SHProject, cs []tcase) (dir string, paths []string) {
	t.Helper()
	dir = t.TempDir()
	for _, c := range cs {
		var path string
		if c.raw != "" {
			path = filepath.Join(dir, c.id+".yaml")
			if err := os.WriteFile(path, []byte(c.raw), 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			path = testutil.WriteTask(t, dir, c.make(p, c.id))
		}
		paths = append(paths, path)
	}
	return dir, paths
}

func TestRun(t *testing.T) {
	testutil.RequireBwrap(t)

	p := testutil.NewSHProject(t)
	cs := cases(t)
	_, paths := writeCases(t, p, cs)
	cacheDir := cache.Dir(t.TempDir())

	// A failing task recorded by an earlier validation loses its entry.
	store := cache.OpenValidated(cacheDir)
	if err := store.Record("setup-fails", "old", fixedNow); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	outcomes, err := Run(context.Background(), paths, options(cacheDir, 4), &out)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != len(cs)+1 {
		t.Fatalf("%d lines, want %d:\n%s", len(lines), len(cs)+1, out.String())
	}
	if want := "2 of 17 valid"; lines[len(lines)-1] != want {
		t.Errorf("last line %q, want %q", lines[len(lines)-1], want)
	}

	for i, c := range cs {
		o := outcomes[i]
		if o.ID != c.id {
			t.Errorf("outcome %d is %q, want %q (task order)", i, o.ID, c.id)
			continue
		}
		if !strings.HasPrefix(lines[i], c.id+" ") {
			t.Errorf("line %d %q is not task %s (output order)", i, lines[i], c.id)
		}

		if c.check == "" {
			if !o.OK {
				t.Errorf("%s: FAIL at %s: %s", c.id, o.Check, o.Hint)
				continue
			}
			if !strings.Contains(lines[i], "  ok    setup ") {
				t.Errorf("%s: line %q", c.id, lines[i])
			}
			hash, _ := task.SHA256(paths[i])
			if !store.IsValid(c.id, hash) {
				t.Errorf("%s: not recorded with its hash", c.id)
			}
			continue
		}

		if o.OK || o.Check != c.check || !strings.Contains(o.Hint, c.hint) {
			t.Errorf("%s: ok=%v check=%q hint=%q, want check %q with hint %q", c.id, o.OK, o.Check, o.Hint, c.check, c.hint)
		}
		if !strings.Contains(lines[i], "  FAIL  "+o.Hint) {
			t.Errorf("%s: line %q", c.id, lines[i])
		}
		if o.Log != "" {
			if _, err := os.Stat(o.Log); err != nil {
				t.Errorf("%s: the hint's log: %v", c.id, err)
			}
		}
		if _, ok := store.Lookup(c.id); ok {
			t.Errorf("%s: an invalid task is in validated.json", c.id)
		}
	}

	if !strings.Contains(lines[0], "tests green, oracle red→green (3/3)") {
		t.Errorf("code task line %q", lines[0])
	}
	if !strings.Contains(lines[1], "answer checks match") {
		t.Errorf("question line %q", lines[1])
	}

	// Workspaces are removed.
	if entries, _ := os.ReadDir(cacheDir.Work()); len(entries) != 0 {
		t.Errorf("%d workspaces left behind", len(entries))
	}
}

// Editing a recorded task's file makes it unvalidated: its hash differs.
func TestEditingAValidTaskUnvalidatesIt(t *testing.T) {
	testutil.RequireBwrap(t)

	p := testutil.NewSHProject(t)
	path := testutil.WriteTask(t, t.TempDir(), p.Task("edited"))
	cacheDir := cache.Dir(t.TempDir())

	if _, err := Run(context.Background(), []string{path}, options(cacheDir, 1), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	store := cache.OpenValidated(cacheDir)
	hash, _ := task.SHA256(path)
	if !store.IsValid("edited", hash) {
		t.Fatal("the valid task was not recorded")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("# edited\n")
	f.Close()
	hash, _ = task.SHA256(path)
	if store.IsValid("edited", hash) {
		t.Fatal("an edited task is still valid")
	}
	if e, ok := store.Lookup("edited"); !ok || !e.ValidatedAt.Equal(fixedNow) {
		t.Errorf("entry %+v, %v", e, ok)
	}
}

func TestKeepWorkspaces(t *testing.T) {
	testutil.RequireBwrap(t)

	p := testutil.NewSHProject(t)
	path := testutil.WriteTask(t, t.TempDir(), p.Task("kept"))
	opts := options(cache.Dir(t.TempDir()), 1)
	opts.KeepWorkspaces = true

	var out bytes.Buffer
	outcomes, err := Run(context.Background(), []string{path}, opts, &out)
	if err != nil {
		t.Fatal(err)
	}
	o := outcomes[0]
	if !o.OK || len(o.Workspaces) != 2 {
		t.Fatalf("outcome %+v", o)
	}
	for _, ws := range o.Workspaces {
		if _, err := os.Stat(filepath.Join(ws, "calc.sh")); err != nil {
			t.Errorf("workspace %s: %v", ws, err)
		}
		if !strings.Contains(out.String(), ws) {
			t.Errorf("the line does not name %s: %q", ws, out.String())
		}
	}
}

// Draft and schema failures need neither git nor bwrap.
func TestSchemaFailuresRunNothing(t *testing.T) {
	p := testutil.NewSHProject(t)
	var cs []tcase
	for _, c := range cases(t) {
		if c.check == CheckDraft || c.check == CheckSchema {
			cs = append(cs, c)
		}
	}
	_, paths := writeCases(t, p, cs)
	cacheDir := cache.Dir(t.TempDir())

	outcomes, err := Run(context.Background(), paths, options(cacheDir, 2), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cs {
		if o := outcomes[i]; o.OK || o.Check != c.check || !strings.Contains(o.Hint, c.hint) {
			t.Errorf("%s: %+v", c.id, o)
		}
	}
	if _, err := os.Stat(cacheDir.Mirrors()); err == nil {
		t.Error("a schema failure mirrored the repository")
	}
}

func TestCancelledRunRecordsNothing(t *testing.T) {
	p := testutil.NewSHProject(t)
	path := testutil.WriteTask(t, t.TempDir(), p.Task("cancelled"))
	cacheDir := cache.Dir(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out bytes.Buffer
	_, err := Run(ctx, []string{path}, options(cacheDir, 1), &out)
	if err != context.Canceled {
		t.Fatalf("err = %v", err)
	}
	if _, ok := cache.OpenValidated(cacheDir).Lookup("cancelled"); ok {
		t.Error("a cancelled task was recorded")
	}
	if strings.Contains(out.String(), "valid") {
		t.Errorf("output %q", out.String())
	}
}

func TestLine(t *testing.T) {
	ok := Outcome{ID: "a", OK: true, Detail: "setup 1s, tests green, oracle red→green (3/3)"}
	if got, want := ok.Line(5), "a      ok    setup 1s, tests green, oracle red→green (3/3)"; got != want {
		t.Errorf("ok line\n got %q\nwant %q", got, want)
	}
	failed := Outcome{ID: "bb", Hint: "oracle green at base_commit: x", Log: "/l/oracle-base.log", Workspaces: []string{"/w"}}
	if got, want := failed.Line(3), "bb   FAIL  oracle green at base_commit: x (log: /l/oracle-base.log) [kept: /w]"; got != want {
		t.Errorf("fail line\n got %q\nwant %q", got, want)
	}
}
