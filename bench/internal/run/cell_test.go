package run

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"orchbench/internal/cache"
	"orchbench/internal/config"
	"orchbench/internal/results"
	"orchbench/internal/runner"
	"orchbench/internal/task"
	"orchbench/internal/testutil"
	"orchbench/internal/workspace"
)

// newEnv is an Env over a throwaway cache and results directory whose clock moves one second at
// each reading, so that a cell started at the second reading and ended at the third took 1 s.
func newEnv(t *testing.T, fake Runner) Env {
	t.Helper()
	var ticks atomic.Int64
	base := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	return Env{
		Workspaces:          workspace.New(cache.Dir(t.TempDir()), nil),
		OutDir:              t.TempDir(),
		ClaudeCLI:           "2.1.0 (Claude Code)",
		OrchestratorVersion: "orchestrator dev",
		Contributor:         "owner",
		Now:                 func() time.Time { return base.Add(time.Duration(ticks.Add(1)-1) * time.Second) },
		Runner:              fake,
	}
}

func cellOf(tk task.Task) Cell {
	return Cell{Task: tk, TaskSHA256: "task-hash", Config: config.Config{Name: "B", Model: "sonnet", SHA256: "cfg-file"}, Rep: 1}
}

// fixer is a model that leaves the run directory of an orchestrator behind and, for a code task,
// fixes mul. It counts its calls.
func fixer(t *testing.T, calls *atomic.Int32, cost float64, answer string, fix bool) Runner {
	return func(ctx context.Context, cfg config.Config, tk task.Task, ws runner.Workspace) (runner.Outcome, error) {
		calls.Add(1)
		files := map[string]string{".orchestrator/runs/r1/run.json": "{}\n"}
		if fix {
			files["calc.sh"] = testutil.CalcFixed
		}
		testutil.Apply(t, ws.Dir, files)
		return runner.Outcome{
			Status:       "completed",
			CostUSD:      cost,
			Stages:       []string{"development", "testing"},
			Answer:       answer,
			ConfigSHA256: "cfg-sent",
			RunDir:       filepath.Join(ws.Dir, ".orchestrator", "runs", "r1"),
		}, nil
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func workDirEntries(t *testing.T, e Env) int {
	t.Helper()
	entries, err := os.ReadDir(e.Workspaces.Cache.Work())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return len(entries)
}

func TestExecRecordsAPassingCodeTask(t *testing.T) {
	testutil.RequireBwrap(t)
	p := testutil.NewSHProject(t)
	var calls atomic.Int32
	env := newEnv(t, fixer(t, &calls, 0.5, "", true))
	tk := p.Task("code")
	tk.Pair = "code-ru"

	fin, err := env.Exec(context.Background(), cellOf(tk))
	if err != nil {
		t.Fatal(err)
	}

	r := fin.Record
	if r.Result != results.Pass || r.Task != "code" || r.TaskSHA256 != "task-hash" || r.Config != "B" || r.Rep != 1 {
		t.Errorf("record %+v", r)
	}
	if r.Language != "sh" || r.Category != tk.Category || r.Lang != "en" || r.Pair != "code-ru" || r.Contributor != "owner" {
		t.Errorf("task fields of %+v", r)
	}
	if r.StartedAt != "2026-10-08T10:00:01Z" || r.WallMS != 1000 {
		t.Errorf("started %s, wall %d ms; want 2026-10-08T10:00:01Z, 1000", r.StartedAt, r.WallMS)
	}
	if r.RunStatus != "completed" || r.CostUSD != 0.5 || strings.Join(r.Stages, ",") != "development,testing" {
		t.Errorf("outcome fields of %+v", r)
	}
	if want := (results.Diff{Files: 1, Added: 1, Deleted: 1}); r.Diff != want {
		t.Errorf("diff %+v, want %+v", r.Diff, want)
	}
	if r.Model != "sonnet" || r.ClaudeCLI != "2.1.0 (Claude Code)" || r.OrchestratorCommit != "orchestrator dev" || r.ConfigSHA256 != "cfg-sent" {
		t.Errorf("build fields of %+v", r)
	}
	if r.OracleExit != 0 || r.SuiteExit != 0 || r.FailReason != "" {
		t.Errorf("check fields of %+v", r)
	}

	logs := filepath.Join(env.OutDir, "logs", "code", "B", "rep1")
	for _, name := range []string{"setup.log", "oracle.log", "suite.log", "config.stdout", "config.stderr", "diff.patch"} {
		if !exists(filepath.Join(logs, name)) {
			t.Errorf("no %s in %s", name, logs)
		}
	}
	patch, _ := os.ReadFile(filepath.Join(logs, "diff.patch"))
	if !strings.Contains(string(patch), "calc.sh") || strings.Contains(string(patch), ".orchestrator") {
		t.Errorf("diff.patch:\n%s", patch)
	}
	if !exists(filepath.Join(env.OutDir, "runs", "code", "B", "rep1", "run.json")) {
		t.Error("the run directory was not copied to runs/code/B/rep1")
	}
	if fin.Kept != "" || workDirEntries(t, env) != 0 {
		t.Errorf("the workspace was not removed (kept %q, %d entries)", fin.Kept, workDirEntries(t, env))
	}
}

func TestExecRecordsAFailedOracle(t *testing.T) {
	testutil.RequireBwrap(t)
	p := testutil.NewSHProject(t)
	var calls atomic.Int32
	env := newEnv(t, fixer(t, &calls, 0.25, "", false))

	fin, err := env.Exec(context.Background(), cellOf(p.Task("code")))
	if err != nil {
		t.Fatal(err)
	}

	r := fin.Record
	if r.Result != results.Fail || r.FailReason != results.FailOracle || r.OracleExit == 0 || r.Diff.Files != 0 {
		t.Errorf("record %+v", r)
	}
	if calls.Load() != 1 {
		t.Errorf("the runner was called %d times; a failed cell is not retried", calls.Load())
	}
}

func TestExecChecksTheAnswerOfAQuestion(t *testing.T) {
	testutil.RequireBwrap(t)
	p := testutil.NewSHProject(t)

	tests := []struct {
		name   string
		answer string
		result string
		reason string
		failed int
	}{
		{"a matching answer", "mul in calc.sh adds its arguments", results.Pass, "", 0},
		{"a wrong answer", "it is in README", results.Fail, results.FailAnswer, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			env := newEnv(t, fixer(t, &calls, 0.1, tt.answer, false))

			fin, err := env.Exec(context.Background(), cellOf(p.Question("q")))
			if err != nil {
				t.Fatal(err)
			}

			r := fin.Record
			if r.Result != tt.result || r.FailReason != tt.reason || len(r.AnswerChecksFailed) != tt.failed || r.Category != task.CategoryQuestion {
				t.Errorf("record %+v", r)
			}
			answer, err := os.ReadFile(filepath.Join(env.OutDir, "logs", "q", "B", "rep1", "answer.txt"))
			if err != nil || string(answer) != tt.answer {
				t.Errorf("answer.txt = %q, %v", answer, err)
			}
		})
	}
}

func TestExecKeepsTheWorkspaceOnRequest(t *testing.T) {
	testutil.RequireBwrap(t)
	p := testutil.NewSHProject(t)
	var calls atomic.Int32
	env := newEnv(t, fixer(t, &calls, 0, "", true))
	env.KeepWorkspaces = true

	fin, err := env.Exec(context.Background(), cellOf(p.Task("code")))
	if err != nil {
		t.Fatal(err)
	}
	if fin.Kept == "" || !exists(filepath.Join(fin.Kept, "calc.sh")) {
		t.Fatalf("kept = %q: the workspace is gone", fin.Kept)
	}
}

func TestExecReplacesTheFilesOfAnEarlierTry(t *testing.T) {
	testutil.RequireBwrap(t)
	p := testutil.NewSHProject(t)
	var calls atomic.Int32
	env := newEnv(t, fixer(t, &calls, 0, "", true))
	c := cellOf(p.Task("code"))
	stale := []string{
		filepath.Join(env.cellDir("logs", c), "stale.log"),
		filepath.Join(env.cellDir("runs", c), "stale.json"),
	}
	for _, f := range stale {
		testutil.Apply(t, filepath.Dir(f), map[string]string{filepath.Base(f): "old\n"})
	}

	if _, err := env.Exec(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	for _, f := range stale {
		if exists(f) {
			t.Errorf("%s survived a redo", f)
		}
	}
}

func TestExecRetriesAnInfraErrorOnceAtOnce(t *testing.T) {
	testutil.RequireBwrap(t)
	p := testutil.NewSHProject(t)

	t.Run("it passes on the second try", func(t *testing.T) {
		var calls atomic.Int32
		good := fixer(t, &calls, 0.5, "", true)
		env := newEnv(t, func(ctx context.Context, cfg config.Config, tk task.Task, ws runner.Workspace) (runner.Outcome, error) {
			if calls.Load() == 0 {
				calls.Add(1)
				return runner.Outcome{CostUSD: 0.2}, &runner.InfraError{Reason: "claude is not available"}
			}
			return good(ctx, cfg, tk, ws)
		})

		fin, err := env.Exec(context.Background(), cellOf(p.Task("code")))
		if err != nil {
			t.Fatal(err)
		}
		if fin.Record.Result != results.Pass || calls.Load() != 2 {
			t.Errorf("result %s after %d calls", fin.Record.Result, calls.Load())
		}
		if math.Abs(fin.Record.CostUSD-0.7) > 1e-9 {
			t.Errorf("cost %v, want 0.7: the failed try was paid for", fin.Record.CostUSD)
		}
		if workDirEntries(t, env) != 0 {
			t.Error("a workspace was left behind")
		}
	})

	t.Run("it fails again", func(t *testing.T) {
		var calls atomic.Int32
		env := newEnv(t, func(ctx context.Context, cfg config.Config, tk task.Task, ws runner.Workspace) (runner.Outcome, error) {
			calls.Add(1)
			return runner.Outcome{CostUSD: 0.2}, &runner.InfraError{Reason: "claude is not available"}
		})

		fin, err := env.Exec(context.Background(), cellOf(p.Task("code")))
		if err != nil {
			t.Fatal(err)
		}
		r := fin.Record
		if r.Result != results.InfraError || !strings.Contains(r.FailureReason, "claude is not available") || r.CostUSD != 0.4 || calls.Load() != 2 {
			t.Errorf("record %+v after %d calls", r, calls.Load())
		}
		if r.Task != "code" || r.Config != "B" || r.Rep != 1 || r.Contributor != "owner" || r.Language != "sh" || r.TaskSHA256 != "task-hash" {
			t.Errorf("an infra_error line still names its cell: %+v", r)
		}
		if workDirEntries(t, env) != 0 {
			t.Error("a workspace was left behind")
		}
	})
}

func TestExecTreatsAFailedSetupAsInfrastructure(t *testing.T) {
	testutil.RequireBwrap(t)
	p := testutil.NewSHProject(t)
	var calls atomic.Int32
	env := newEnv(t, fixer(t, &calls, 1, "", true))
	tk := p.Task("code")
	tk.SetupCommand = "exit 3"

	fin, err := env.Exec(context.Background(), cellOf(tk))
	if err != nil {
		t.Fatal(err)
	}
	if fin.Record.Result != results.InfraError || fin.Record.FailureReason != "setup: exit 3" {
		t.Errorf("record %+v", fin.Record)
	}
	if calls.Load() != 0 || fin.Record.CostUSD != 0 {
		t.Errorf("the model ran %d times for a cell whose setup failed", calls.Load())
	}
}

func TestExecWritesNothingWhenInterrupted(t *testing.T) {
	testutil.RequireBwrap(t)
	p := testutil.NewSHProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	env := newEnv(t, func(ctx context.Context, cfg config.Config, tk task.Task, ws runner.Workspace) (runner.Outcome, error) {
		calls.Add(1)
		cancel() // Ctrl-C while the model works
		<-ctx.Done()
		return runner.Outcome{}, ctx.Err()
	})

	fin, err := env.Exec(ctx, cellOf(p.Task("code")))

	if !errors.Is(err, context.Canceled) || fin.Record.Task != "" {
		t.Fatalf("finished %+v, err %v; want no record and the context's error", fin, err)
	}
	if calls.Load() != 1 {
		t.Errorf("the runner was called %d times: an interrupted cell is not retried", calls.Load())
	}
	if workDirEntries(t, env) != 0 {
		t.Error("a workspace was left behind")
	}
}

func TestCountDiff(t *testing.T) {
	patch := `diff --git a/calc.sh b/calc.sh
index 1111111..2222222 100644
--- a/calc.sh
+++ b/calc.sh
@@ -1,2 +1,3 @@
 add() { echo $(($1 + $2)); }
-mul() { echo $(($1 + $2)); }
-- a deleted line that starts with dashes
+mul() { echo $(($1 * $2)); }
++ an added line that starts with pluses
+# done
diff --git a/img.png b/img.png
new file mode 100644
index 0000000..3333333
GIT binary patch
literal 4
LcmZQzU|?VY0001

`
	if got, want := countDiff(patch), (results.Diff{Files: 2, Added: 3, Deleted: 2}); got != want {
		t.Errorf("countDiff = %+v, want %+v", got, want)
	}
	if got := countDiff(""); got != (results.Diff{}) {
		t.Errorf("countDiff of nothing = %+v", got)
	}
}

func TestCopyTreeKeepsLinksAndNeverFollowsThem(t *testing.T) {
	src := t.TempDir()
	outside := t.TempDir()
	testutil.Apply(t, src, map[string]string{"run.json": "{}\n", "steps/1/out.txt": "x\n"})
	testutil.Apply(t, outside, map[string]string{"secret": "s\n"})
	if err := os.Symlink(outside, filepath.Join(src, "escape")); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "runs", "t", "B", "rep1")
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}

	if data, err := os.ReadFile(filepath.Join(dst, "steps", "1", "out.txt")); err != nil || string(data) != "x\n" {
		t.Errorf("nested file: %q, %v", data, err)
	}
	target, err := os.Readlink(filepath.Join(dst, "escape"))
	if err != nil || target != outside {
		t.Errorf("the symlink was not copied as a link: %q, %v", target, err)
	}
}

func TestReasonIsOneShortLine(t *testing.T) {
	long := errors.New("setup:\n" + strings.Repeat("word ", 200))
	got := reason(long)
	if strings.Contains(got, "\n") || len([]rune(got)) > maxReason+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("reason = %q", got)
	}
}
