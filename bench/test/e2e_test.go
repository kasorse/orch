// Package e2e_test runs the whole of orchbench (specs/benchmark/SPEC.md §16, BM12): validate,
// run with every config and two repetitions, an interrupt, a continue, the --max-cost stop and
// summarize, as the real binary in subprocesses over a fixture repository.
//
// Only the model and the two command line programs are fake: testdata/fake is built and
// installed as `claude` and as `orchestrator`. The git mirror and workspaces, bwrap and the
// final check, the parsing of stream-json and run.json, results.jsonl and summary.md are real.
// The test needs no network; where bwrap cannot create a sandbox it skips, as BM05's do.
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"orchbench/internal/results"
	"orchbench/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite testdata/summary.golden.md from the current output")

// scenario is what the fakes do for each cell and what the cell must end as: the kind of the
// plan file (see testdata/fake) and the result, with "/<fail_reason>" for a fail. Every kind of
// result line is among them; the orchestrator's exit 2 of e2e-fix D is the infra_error.
var scenario = []struct{ task, config, kind, want string }{
	{"e2e-fix", "A", "fix", "pass"},
	{"e2e-fix", "B", "nochange", "fail/oracle"},
	{"e2e-fix", "C", "regression", "fail/regression"},
	{"e2e-fix", "D", "infra", "infra_error"},
	{"e2e-q", "A", "answer-ok", "pass"},
	{"e2e-q", "B", "answer-bad", "fail/answer"},
	{"e2e-q", "C", "answer-ok", "pass"},
	{"e2e-q", "D", "answer-ok", "pass"},
}

// regressionCalc fixes mul and breaks add: the oracle passes, the suite does not.
const regressionCalc = `add() { echo $(($1 - $2)); }
mul() { echo $(($1 * $2)); }
`

// programs are the executables of the scenario.
type programs struct{ orchbench, orchestrator, claude string }

var binDir string
var buildOnce sync.Once
var built programs
var buildErr error

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}

// buildPrograms builds orchbench and the fake, once per test binary. The fake is installed under
// both names: it plays the program it is run as.
func buildPrograms(t *testing.T) programs {
	t.Helper()
	buildOnce.Do(func() { built, buildErr = build() })
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return built
}

func build() (programs, error) {
	dir, err := os.MkdirTemp("", "orchbench-e2e-")
	if err != nil {
		return programs{}, err
	}
	binDir = dir

	bench, err := filepath.Abs("..")
	if err != nil {
		return programs{}, err
	}
	fake, err := filepath.Abs(filepath.Join("testdata", "fake"))
	if err != nil {
		return programs{}, err
	}
	p := programs{orchbench: filepath.Join(dir, "orchbench"), orchestrator: filepath.Join(dir, "orchestrator"), claude: filepath.Join(dir, "claude")}

	if err := goBuild(bench, p.orchbench, "./cmd/orchbench"); err != nil {
		return programs{}, err
	}
	if err := goBuild(fake, p.orchestrator, "."); err != nil {
		return programs{}, err
	}
	data, err := os.ReadFile(p.orchestrator)
	if err != nil {
		return programs{}, err
	}
	if err := os.WriteFile(p.claude, data, 0o755); err != nil {
		return programs{}, err
	}
	return p, nil
}

// goBuild builds the package pkg of the module in dir. GOWORK is off so that a workspace file
// above the repository cannot take the fake's module in or out.
func goBuild(dir, out, pkg string) error {
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build %s in %s: %w\n%s", pkg, dir, err, output)
	}
	return nil
}

// start prepares a run of bin with its output collected in two buffers.
func start(ctx context.Context, bin string, env, args []string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	return cmd, &stdout, &stderr
}

// exitCode is the exit code of a finished command: 0 for nil, -1 when it did not exit.
func exitCode(err error) int {
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exitErr):
		return exitErr.ExitCode()
	}
	return -1
}

func wantExit(t *testing.T, got, want int, stdout, stderr string) {
	t.Helper()
	if got != want {
		t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", got, want, stdout, stderr)
	}
}

// cell is the name of a cell as a --dry-run line spells it, fields joined by one space.
func cell(task, config string, rep int) string {
	return fmt.Sprintf("%s %s rep%d", task, config, rep)
}

// lines are the result lines of results.jsonl in the order they were written. A last line with
// no newline is a failure here: run writes whole lines, and an interrupt must not cut one.
func lines(t *testing.T, out string) []results.Record {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(out, "results.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		t.Fatalf("results.jsonl ends in a partial line: %q", data[bytes.LastIndexByte(data, '\n')+1:])
	}

	var recs []results.Record
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec results.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("results.jsonl: %v in %q", err, line)
		}
		recs = append(recs, rec)
	}
	return recs
}

func cellsOf(recs []results.Record) []string {
	var names []string
	for _, r := range recs {
		names = append(names, cell(r.Task, r.Config, r.Rep))
	}
	return names
}

// outcome is a record's result as the scenario spells it.
func outcome(r results.Record) string {
	if r.Result == results.Fail {
		return r.Result + "/" + r.FailReason
	}
	return r.Result
}

func outcomesOf(recs []results.Record) []string {
	var names []string
	for _, r := range recs {
		names = append(names, outcome(r))
	}
	return names
}

// dryRunCells are the cell lines of a --dry-run output: three fields, the last one repN.
func dryRunCells(stdout string) []string {
	var found []string
	for _, line := range strings.Split(stdout, "\n") {
		if f := strings.Fields(line); len(f) == 3 && strings.HasPrefix(f[2], "rep") {
			found = append(found, strings.Join(f, " "))
		}
	}
	return found
}

func sameList(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func mustContain(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Errorf("%s has no %q:\n%s", what, part, text)
		}
	}
}

func TestPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("builds orchbench and runs the whole pipeline; skipped with -short")
	}
	testutil.RequireBwrap(t)
	for _, tool := range []string{"go", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH", tool)
		}
	}
	tools := buildPrograms(t)

	// The fixture: the shell project of testutil (no toolchain to find inside the sandbox), a
	// bugfix task and a question task, each with the marker the fakes find the task by.
	project := testutil.NewSHProject(t)
	tasksDir := t.TempDir()
	fixTask := project.Task("e2e-fix")
	fixTask.Task = "[e2e:e2e-fix] " + fixTask.Task
	questionTask := project.Question("e2e-q")
	questionTask.Task = "[e2e:e2e-q] " + questionTask.Task
	testutil.WriteTask(t, tasksDir, fixTask)
	testutil.WriteTask(t, tasksDir, questionTask)

	// The fakes' inputs: what each cell does, the canned trees and the directory for the block.
	plan := map[string]map[string]any{}
	for _, c := range scenario {
		plan[c.task+"/"+c.config] = map[string]any{"kind": c.kind, "cost": 0.1}
	}
	planData, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, planData, 0o644); err != nil {
		t.Fatal(err)
	}
	patches := t.TempDir()
	testutil.Apply(t, filepath.Join(patches, "fix"), map[string]string{"calc.sh": testutil.CalcFixed})
	testutil.Apply(t, filepath.Join(patches, "regression"), map[string]string{"calc.sh": regressionCalc})
	state := t.TempDir()
	env := append(os.Environ(), "FAKE_PLAN="+planPath, "FAKE_PATCHES="+patches, "FAKE_STATE="+state)

	configsDir, err := filepath.Abs(filepath.Join("..", "configs"))
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	out := filepath.Join(t.TempDir(), "out")
	taskGlob := filepath.Join(tasksDir, "*.yaml")
	common := []string{"--cache-dir", cacheDir, "--orchestrator", tools.orchestrator, "--claude", tools.claude}

	args := func(command string, more ...string) []string {
		return append(append([]string{command}, common...), more...)
	}
	runArgs := func(more ...string) []string {
		base := []string{"--tasks", taskGlob, "--out", out, "--configs", "A,B,C,D", "--reps", "2", "-j", "1", "--configs-dir", configsDir}
		return args("run", append(base, more...)...)
	}
	invoke := func(t *testing.T, extraEnv, argv []string) (int, string, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd, stdout, stderr := start(ctx, tools.orchbench, append(slices.Clone(env), extraEnv...), argv)
		err := cmd.Run()
		if ctx.Err() != nil {
			t.Fatalf("orchbench %s did not end in 5 minutes\nstdout:\n%s\nstderr:\n%s", argv[0], stdout, stderr)
		}
		if code := exitCode(err); code < 0 {
			t.Fatalf("orchbench %s: %v", argv[0], err)
		}
		return exitCode(err), stdout.String(), stderr.String()
	}
	step := func(name string, fn func(t *testing.T)) {
		t.Helper()
		if !t.Run(name, fn) {
			t.FailNow()
		}
	}

	step("validate", func(t *testing.T) {
		code, stdout, stderr := invoke(t, nil, args("validate", "--tasks", taskGlob))
		wantExit(t, code, 0, stdout, stderr)
		mustContain(t, "validate's output", stdout, "2 of 2 valid")
	})

	// A budget of $0.25 with cells of $0.10 lets three cells start: the third starts at $0.20.
	step("budget stop", func(t *testing.T) {
		code, stdout, stderr := invoke(t, nil, runArgs("--max-cost", "0.25"))
		wantExit(t, code, 3, stdout, stderr)
		recs := lines(t, out)
		sameList(t, "cells written", cellsOf(recs), []string{cell("e2e-fix", "A", 1), cell("e2e-fix", "B", 1), cell("e2e-fix", "C", 1)})
		sameList(t, "their results", outcomesOf(recs), []string{"pass", "fail/oracle", "fail/regression"})
		mustContain(t, "run's output", stdout, "13 not run: budget")
		mustContain(t, "run's error", stderr, "stopped by --max-cost")
	})

	// The first call of e2e-q B rep 1 leaves a marker and sleeps; the interrupt arrives then.
	step("interrupt", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd, stdout, stderr := start(ctx, tools.orchbench, append(slices.Clone(env), "FAKE_BLOCK=e2e-q/B"), runArgs("--max-cost", "100"))
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		marker := filepath.Join(state, "blocked")
		deadline := time.After(2 * time.Minute)
		for {
			if _, err := os.Stat(marker); err == nil {
				break
			}
			select {
			case err := <-done:
				t.Fatalf("orchbench ended (%v) before e2e-q B rep1 started\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			case <-deadline:
				cmd.Process.Kill()
				<-done
				t.Fatalf("e2e-q B rep1 did not start in 2 minutes\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			case <-time.After(50 * time.Millisecond):
			}
		}

		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			wantExit(t, exitCode(err), 130, stdout.String(), stderr.String())
		case <-time.After(time.Minute):
			cmd.Process.Kill()
			<-done
			t.Fatalf("orchbench did not stop in a minute after the interrupt\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		mustContain(t, "run's output", stdout.String(), "not run: interrupted")

		recs := lines(t, out)
		sameList(t, "cells written", cellsOf(recs), []string{
			cell("e2e-fix", "A", 1), cell("e2e-fix", "B", 1), cell("e2e-fix", "C", 1),
			cell("e2e-fix", "D", 1), cell("e2e-fix", "A", 2), cell("e2e-fix", "B", 2), cell("e2e-fix", "C", 2), cell("e2e-fix", "D", 2),
			cell("e2e-q", "A", 1),
		})
		read, err := results.ReadFile(filepath.Join(out, "results.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if read.Partial != 0 {
			t.Errorf("results.jsonl has a partial line after the interrupt")
		}

		// The cells to run now: the interrupted one and the rest, and the two infra_error
		// cells, which a result line does not settle.
		code, dryOut, dryErr := invoke(t, nil, runArgs("--max-cost", "100", "--dry-run"))
		wantExit(t, code, 0, dryOut, dryErr)
		sameList(t, "cells left", dryRunCells(dryOut), []string{
			cell("e2e-fix", "D", 1), cell("e2e-fix", "D", 2),
			cell("e2e-q", "B", 1), cell("e2e-q", "C", 1), cell("e2e-q", "D", 1),
			cell("e2e-q", "A", 2), cell("e2e-q", "B", 2), cell("e2e-q", "C", 2), cell("e2e-q", "D", 2),
		})
	})

	// The same command, nothing blocked: the missing cells run, exit 1 for the infra_error ones.
	step("continue", func(t *testing.T) {
		before, found, err := results.LoadManifest(out)
		if err != nil || !found {
			t.Fatalf("manifest.json after the first runs: found %v, error %v", found, err)
		}
		if before.Orchestrator.Version != "orchestrator e2e" || before.ClaudeCLI != "2.1.0 (Claude Code)" {
			t.Errorf("manifest names %q and %q, want what the fakes print", before.Orchestrator.Version, before.ClaudeCLI)
		}

		code, stdout, stderr := invoke(t, nil, runArgs("--max-cost", "100"))
		wantExit(t, code, 1, stdout, stderr)
		mustContain(t, "run's output", stdout, "total $1.40 of $100.00", ", 7 results, 2 infra errors")
		mustContain(t, "run's error", stderr, "2 cells left as infra_error", "run the same command again")

		after, found, err := results.LoadManifest(out)
		if err != nil || !found {
			t.Fatalf("manifest.json after the continue: found %v, error %v", found, err)
		}
		if after.Orchestrator != before.Orchestrator || after.ClaudeCLI != before.ClaudeCLI || after.Created != before.Created || len(after.Mixed) != 0 {
			t.Errorf("the manifest changed or was marked mixed:\nbefore %+v\nafter  %+v", before, after)
		}

		read, err := results.ReadFile(filepath.Join(out, "results.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		// 3 + 6 + 9 lines, 16 cells: the two infra_error lines of the second run were replaced.
		if len(read.Cells) != 16 || read.Lines != 18 || read.Replaced != 2 || read.Partial != 0 {
			t.Errorf("results.jsonl has %d cells in %d lines, %d replaced, %d partial; want 16 in 18, 2, 0",
				len(read.Cells), read.Lines, read.Replaced, read.Partial)
		}
		checkResults(t, read.Cells)

		code, dryOut, dryErr := invoke(t, nil, runArgs("--max-cost", "100", "--dry-run"))
		wantExit(t, code, 0, dryOut, dryErr)
		sameList(t, "cells left", dryRunCells(dryOut), []string{cell("e2e-fix", "D", 1), cell("e2e-fix", "D", 2)})
	})

	step("summarize", func(t *testing.T) {
		pinTimes(t, out)
		code, stdout, stderr := invoke(t, nil, args("summarize", out, "--seed", "1"))
		wantExit(t, code, 0, stdout, stderr)
		mustContain(t, "summarize's output", stdout, "(16 cells)")

		got, err := os.ReadFile(filepath.Join(out, "summary.md"))
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, "summary.md", string(got),
			"Based on 16 cells from 2 tasks × 4 configs × up to 2 reps: 8 pass, 6 fail, 2 infra_error",
			"2 result lines replaced by a later line")

		golden := filepath.Join("testdata", "summary.golden.md")
		if *update {
			if err := os.WriteFile(golden, got, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (go test ./test -run TestPipeline -update writes it)", err)
		}
		if string(got) != string(want) {
			t.Errorf("summary.md differs from %s (go test ./test -update rewrites it)\n--- got\n%s\n--- want\n%s", golden, got, want)
		}
	})
}

// checkResults compares the latest line of every cell with the scenario and looks at what the
// real parsers made of the fakes' output.
func checkResults(t *testing.T, latest []results.Record) {
	t.Helper()
	byCell := map[string]results.Record{}
	for _, r := range latest {
		byCell[cell(r.Task, r.Config, r.Rep)] = r
	}
	for _, c := range scenario {
		for rep := 1; rep <= 2; rep++ {
			name := cell(c.task, c.config, rep)
			r, ok := byCell[name]
			switch {
			case !ok:
				t.Errorf("%s has no result line", name)
			case outcome(r) != c.want:
				t.Errorf("%s: %s, want %s (failure_reason %q)", name, outcome(r), c.want, r.FailureReason)
			}
		}
	}

	// A cell that passed by config A: one call, the tokens and cost of the result event, and
	// the one-line change of the canned fix.
	a := byCell[cell("e2e-fix", "A", 1)]
	if a.LLMCalls != 1 || a.Tokens.Input != 1000 || a.Tokens.CacheRead != 3000 || a.CostUSD != 0.1 || a.Diff != (results.Diff{Files: 1, Added: 1, Deleted: 1}) {
		t.Errorf("e2e-fix A rep1: %d calls, %+v, $%v, diff %+v", a.LLMCalls, a.Tokens, a.CostUSD, a.Diff)
	}
	// B: the decision chose the stages, and the nothing it did leaves an oracle that fails.
	b := byCell[cell("e2e-fix", "B", 1)]
	if b.OracleExit == 0 || b.Diff.Files != 0 || b.Complexity != "medium" || b.Decision.Calls != 2 || !slices.Equal(b.Stages, []string{"development", "testing"}) {
		t.Errorf("e2e-fix B rep1: oracle exit %d, diff %+v, complexity %q, decision %+v, stages %v", b.OracleExit, b.Diff, b.Complexity, b.Decision, b.Stages)
	}
	// C: every stage by --stage, and the regression is the suite's.
	c := byCell[cell("e2e-fix", "C", 1)]
	if c.OracleExit != 0 || c.SuiteExit == 0 || len(c.Stages) != 6 || c.ReviewVerdict != "pass" {
		t.Errorf("e2e-fix C rep1: oracle exit %d, suite exit %d, stages %v, review %q", c.OracleExit, c.SuiteExit, c.Stages, c.ReviewVerdict)
	}
	// D of the fix task: both attempts ended in the orchestrator's exit 2, and cost nothing.
	d := byCell[cell("e2e-fix", "D", 1)]
	if !strings.Contains(d.FailureReason, "canned configuration failure") || d.CostUSD != 0 {
		t.Errorf("e2e-fix D rep1: failure_reason %q, $%v", d.FailureReason, d.CostUSD)
	}
	// The wrong answer fails both checks; D answers although it ran no read-only stage of its own.
	q := byCell[cell("e2e-q", "B", 1)]
	if len(q.AnswerChecksFailed) != 2 {
		t.Errorf("e2e-q B rep1: failed checks %q, want both", q.AnswerChecksFailed)
	}
	qd := byCell[cell("e2e-q", "D", 1)]
	if !slices.Equal(qd.Stages, []string{"development", "testing"}) {
		t.Errorf("e2e-q D rep1: stages %v, want development and testing", qd.Stages)
	}
}

// pinTimes rewrites results.jsonl with the wall time of each config and the start time fixed,
// keeping the lines, their order and every other field, so that summary.md is the same on every
// machine. Nothing else in a result line varies from run to run.
func pinTimes(t *testing.T, out string) {
	t.Helper()
	path := filepath.Join(out, "results.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wall := map[string]int64{"A": 1000, "B": 2000, "C": 3000, "D": 4000}

	var pinned bytes.Buffer
	enc := json.NewEncoder(&pinned)
	enc.SetEscapeHTML(false)
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("results.jsonl: %v in %q", err, line)
		}
		config, _ := rec["config"].(string)
		rec["wall_ms"] = wall[config]
		rec["started_at"] = "2026-01-01T00:00:00Z"
		if err := enc.Encode(rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, pinned.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
