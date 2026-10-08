package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"orchbench/internal/cache"
	"orchbench/internal/exitcode"
	"orchbench/internal/results"
	benchrun "orchbench/internal/run"
	"orchbench/internal/task"
	"orchbench/internal/testutil"
)

// runFixture is a cache, a task directory and an output directory for `orchbench run`, with every
// program and the cell executor replaced, so that no git, bwrap, orchestrator or model is needed.
type runFixture struct {
	t     *testing.T
	cache string
	tasks string
	out   string
	exec  *fakeExec
}

// fakeExec is the cell executor of the tests: every cell passes and costs cost dollars.
type fakeExec struct {
	mu    sync.Mutex
	cost  float64
	cells []string
	env   benchrun.Env
}

func (f *fakeExec) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cells)
}

func (f *fakeExec) run(env benchrun.Env) benchrun.ExecFunc {
	f.mu.Lock()
	f.env = env
	f.mu.Unlock()
	return func(ctx context.Context, c benchrun.Cell) (benchrun.Finished, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.cells = append(f.cells, c.Task.ID+":"+c.Config.Name)
		return benchrun.Finished{Record: results.Record{
			Task: c.Task.ID, TaskSHA256: c.TaskSHA256, Config: c.Config.Name, Rep: c.Rep,
			Contributor: env.Contributor, Result: results.Pass, CostUSD: f.cost, WallMS: 1000,
		}}, nil
	}
}

// setBuild makes the probe of the orchestrator and the claude CLI answer with this CLI version.
func setBuild(cli string) {
	probeBuild = func(context.Context, commonFlags) (results.Build, string, error) {
		return results.Build{Version: "orchestrator dev", SHA256: "bin1"}, cli, nil
	}
}

func newRunFixture(t *testing.T, ids ...string) *runFixture {
	t.Helper()
	oldLook, oldProbe, oldBuild, oldExec := lookPath, probe, probeBuild, newExec
	t.Cleanup(func() { lookPath, probe, probeBuild, newExec = oldLook, oldProbe, oldBuild, oldExec })

	f := &runFixture{t: t, cache: t.TempDir(), tasks: t.TempDir(), out: filepath.Join(t.TempDir(), "out"), exec: &fakeExec{cost: 0.1}}
	lookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	probe = func(context.Context) error { return nil }
	setBuild("2.1.0")
	newExec = f.exec.run

	for _, id := range ids {
		f.writeTask(id, "")
		f.validate(id)
	}
	return f
}

// project is a task project without a repository: `run` is not given a real one, because the
// cell executor is replaced.
func project() *testutil.SHProject {
	return &testutil.SHProject{
		Repo: &testutil.Repo{Dir: "/nonexistent/repo"},
		Base: strings.Repeat("a", 40),
		Fix:  strings.Repeat("b", 40),
	}
}

// writeTask writes the task file of id; a non-empty text replaces the task text.
func (f *runFixture) writeTask(id, text string) {
	f.t.Helper()
	tk := project().Task(id)
	if text != "" {
		tk.Task = text
	}
	testutil.WriteTask(f.t, f.tasks, tk)
}

// validate records the current file of id as validated.
func (f *runFixture) validate(id string) {
	f.t.Helper()
	path := filepath.Join(f.tasks, id+".yaml")
	hash, err := task.SHA256(path)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := cache.OpenValidated(cache.Dir(f.cache)).Record(id, hash, time.Now()); err != nil {
		f.t.Fatal(err)
	}
}

// args is the flags every call has; --out and --max-cost are the caller's.
func (f *runFixture) args(extra ...string) []string {
	configs, err := filepath.Abs(filepath.Join("..", "..", "configs"))
	if err != nil {
		f.t.Fatal(err)
	}
	base := []string{"run", "--tasks", filepath.Join(f.tasks, "*.yaml"), "--cache-dir", f.cache, "--configs-dir", configs}
	return append(base, extra...)
}

// full is args with --out and a budget big enough for most calls.
func (f *runFixture) full(extra ...string) []string {
	return f.args(append([]string{"--out", f.out, "--max-cost", "100"}, extra...)...)
}

func (f *runFixture) call(args []string) (string, error) {
	f.t.Helper()
	var out, errOut bytes.Buffer
	err := run(context.Background(), args, &out, &errOut)
	return out.String(), err
}

func (f *runFixture) results() results.Records {
	f.t.Helper()
	recs, err := results.ReadFile(filepath.Join(f.out, resultsFile))
	if err != nil {
		f.t.Fatal(err)
	}
	return recs
}

func (f *runFixture) manifest() results.Manifest {
	f.t.Helper()
	m, found, err := results.LoadManifest(f.out)
	if err != nil || !found {
		f.t.Fatalf("manifest: found = %v, err = %v", found, err)
	}
	return m
}

func (f *runFixture) outExists() bool {
	_, err := os.Stat(f.out)
	return err == nil
}

func wantUsage(t *testing.T, err error, substrings ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("run succeeded")
	}
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Fatalf("exit code %d (%v), want %d", code, err, exitcode.Usage)
	}
	for _, s := range substrings {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q does not mention %q", err, s)
		}
	}
}

func TestRunUsageErrors(t *testing.T) {
	f := newRunFixture(t, "t-a")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no --out", f.args("--max-cost", "5"), "--out is required"},
		{"no --max-cost", f.args("--out", f.out), "--max-cost is required"},
		{"--max-cost 0", f.full("--max-cost", "0"), "--max-cost is required"},
		{"--max-cost negative", f.full("--max-cost", "-1"), "--max-cost is required"},
		{"--max-cost infinite", f.full("--max-cost", "+Inf"), "--max-cost is required"},
		{"--reps 0", f.full("--reps", "0"), "--reps 0: must be at least 1"},
		{"-j 0", f.full("-j", "0"), "-j 0: must be at least 1"},
		{"--limit negative", f.full("--limit", "-1"), "--limit -1"},
		{"a contributor with a space", f.full("--contributor", "a b"), "--contributor"},
		{"an empty contributor", f.full("--contributor", ""), "--contributor"},
		{"an empty config name", f.full("--configs", "A,,B"), "an empty name"},
		{"a config twice", f.full("--configs", "A,A"), "listed twice"},
		{"a config that is not there", f.full("--configs", "A,Z"), "config Z"},
		{"a directory without common.yaml", f.full("--configs-dir", t.TempDir()), "common.yaml"},
		{"no --tasks", []string{"run", "--out", f.out, "--max-cost", "5"}, "--tasks is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, err := f.call(tt.args)
			wantUsage(t, err, tt.want)
			if stdout != "" || f.outExists() {
				t.Errorf("something was done: stdout %q, out exists %v", stdout, f.outExists())
			}
			if f.exec.count() != 0 {
				t.Error("a cell ran")
			}
		})
	}
}

func TestRunRefusesTasksNotValidatedWithTheirHash(t *testing.T) {
	f := newRunFixture(t, "t-a", "t-b")
	f.writeTask("t-b", "the text changed after validation")
	f.writeTask("t-c", "") // never validated

	stdout, err := f.call(f.full("--configs", "A"))

	wantUsage(t, err, "not validated", "t-b", "t-c", "orchbench validate")
	if strings.Contains(err.Error(), "t-a") {
		t.Errorf("the validated task is named in %q", err)
	}
	if stdout != "" || f.outExists() || f.exec.count() != 0 {
		t.Errorf("something was done: stdout %q, out exists %v, %d cells", stdout, f.outExists(), f.exec.count())
	}
}

func TestRunRefusesADraft(t *testing.T) {
	f := newRunFixture(t)
	tk := project().Task("t-a")
	tk.Draft = true
	testutil.WriteTask(t, f.tasks, tk)
	f.validate("t-a")

	_, err := f.call(f.full("--configs", "A"))

	wantUsage(t, err, "drafts", "t-a")
}

func TestRunRefusesAConfigThatCannotBeRunOnATask(t *testing.T) {
	f := newRunFixture(t)
	f.writeTask("t-a", "-fix the multiplication")
	f.validate("t-a")

	_, err := f.call(f.full("--configs", "A,B"))

	wantUsage(t, err, "t-a with config B", "flag")
	if strings.Contains(err.Error(), "config A") {
		t.Errorf("config A has no problem with this task: %v", err)
	}
	if f.outExists() {
		t.Error("the out directory was created")
	}
}

func TestRunDryRunPrintsThePlanAndTouchesNothing(t *testing.T) {
	f := newRunFixture(t, "t-a", "t-b", "t-c")
	// A dry run needs no program: it is what you run to see what a call would do.
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	probeBuild = func(context.Context, commonFlags) (results.Build, string, error) {
		return results.Build{}, "", errors.New("no orchestrator")
	}

	stdout, err := f.call(f.full("--dry-run", "--limit", "2", "--configs", "A,B", "--reps", "2"))
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	want := []string{
		"t-a  A  rep1", "t-a  B  rep1", "t-a  A  rep2", "t-a  B  rep2",
		"t-b  A  rep1", "t-b  B  rep1", "t-b  A  rep2", "t-b  B  rep2",
		"8 cells (2 tasks × 2 configs × 2 reps, 0 already done), order: task-major, configs interleaved",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("output\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if f.outExists() || f.exec.count() != 0 {
		t.Errorf("a dry run did something: out exists %v, %d cells", f.outExists(), f.exec.count())
	}
}

func TestRunDryRunKeepsOnlyTheFirstTasksByIDAfterIDs(t *testing.T) {
	f := newRunFixture(t, "t-a", "t-b", "t-c")

	stdout, err := f.call(f.full("--dry-run", "--configs", "A", "--reps", "1", "--ids", "t-b,t-c", "--limit", "1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "t-b  A  rep1") || strings.Contains(stdout, "t-c") || !strings.Contains(stdout, "1 cell (1 task") {
		t.Errorf("output:\n%s", stdout)
	}
}

func TestRunChecksPrerequisitesBeforeWriting(t *testing.T) {
	f := newRunFixture(t, "t-a")
	lookPath = func(file string) (string, error) {
		if file == "claude" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + file, nil
	}

	_, err := f.call(f.full("--configs", "A", "--reps", "1"))

	wantUsage(t, err, "claude")
	if f.outExists() || f.exec.count() != 0 {
		t.Errorf("out exists %v, %d cells", f.outExists(), f.exec.count())
	}
}

func TestRunWritesResultsAndManifestAndContinues(t *testing.T) {
	f := newRunFixture(t, "t-a", "t-b", "t-c")

	stdout, err := f.call(f.full("--configs", "A,B", "--reps", "1", "-j", "2", "--contributor", "owner"))
	if err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	if !strings.HasPrefix(stdout, "6 cells (3 tasks × 2 configs × 1 rep, 0 already done)") || !strings.Contains(stdout, "total $0.60 of $100.00") {
		t.Errorf("output:\n%s", stdout)
	}
	if recs := f.results(); recs.Lines != 6 || len(recs.Cells) != 6 {
		t.Fatalf("%d lines, %d cells", recs.Lines, len(recs.Cells))
	}
	if f.exec.env.Contributor != "owner" || f.exec.env.ClaudeCLI != "2.1.0" || f.exec.env.OrchestratorVersion != "orchestrator dev" || f.exec.env.OutDir != f.out {
		t.Errorf("the cell environment is %+v", f.exec.env)
	}

	m := f.manifest()
	if m.Orchestrator.Version != "orchestrator dev" || m.Orchestrator.SHA256 != "bin1" || m.ClaudeCLI != "2.1.0" || m.Contributor != "owner" {
		t.Errorf("manifest %+v", m)
	}
	if len(m.Configs) != 2 || m.Configs["A"] == "" || len(m.Tasks) != 3 || m.Tasks["t-b"] == "" || len(m.Mixed) != 0 {
		t.Errorf("manifest configs %v, tasks %v, mixed %v", m.Configs, m.Tasks, m.Mixed)
	}

	// The same call again has nothing to do.
	stdout, err = f.call(f.full("--configs", "A,B", "--reps", "1"))
	if err != nil || !strings.Contains(stdout, "nothing to run") || f.exec.count() != 6 {
		t.Errorf("second call: err %v, %d cells run in all\n%s", err, f.exec.count(), stdout)
	}

	// A new config on the same --out runs only its own cells.
	stdout, err = f.call(f.full("--configs", "A,B,C", "--reps", "1"))
	if err != nil || !strings.HasPrefix(stdout, "3 cells (3 tasks × 3 configs × 1 rep, 6 already done)") || f.exec.count() != 9 {
		t.Errorf("third call: err %v, %d cells run in all\n%s", err, f.exec.count(), stdout)
	}
	if m := f.manifest(); len(m.Configs) != 3 || len(m.Mixed) != 0 {
		t.Errorf("manifest after adding a config: configs %v, mixed %v", m.Configs, m.Mixed)
	}
}

func TestRunRefusesAManifestOfAnotherBuildUnlessAllowMixed(t *testing.T) {
	f := newRunFixture(t, "t-a")
	if _, err := f.call(f.full("--configs", "A", "--reps", "1")); err != nil {
		t.Fatal(err)
	}
	if f.exec.count() != 1 {
		t.Fatalf("%d cells ran", f.exec.count())
	}

	// Another CLI: refused, and nothing changes.
	setBuild("2.2.0")
	_, err := f.call(f.full("--configs", "A", "--reps", "2"))
	wantUsage(t, err, "claude CLI", "2.1.0", "2.2.0", "--allow-mixed")
	if f.exec.count() != 1 || f.results().Lines != 1 || f.manifest().ClaudeCLI != "2.1.0" {
		t.Errorf("a refused call changed something: %d cells, %d lines, manifest %+v", f.exec.count(), f.results().Lines, f.manifest())
	}

	// Another contributor is a difference too.
	setBuild("2.1.0")
	_, err = f.call(f.full("--configs", "A", "--reps", "2", "--contributor", "friend"))
	wantUsage(t, err, "contributor", "friend")

	// Another build of the orchestrator.
	probeBuild = func(context.Context, commonFlags) (results.Build, string, error) {
		return results.Build{Version: "orchestrator dev", SHA256: "bin2"}, "2.1.0", nil
	}
	_, err = f.call(f.full("--configs", "A", "--reps", "2"))
	wantUsage(t, err, "orchestrator binary sha256")

	// --allow-mixed continues and records what differed.
	setBuild("2.2.0")
	stdout, err := f.call(f.full("--configs", "A", "--reps", "2", "--allow-mixed"))
	if err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	if f.exec.count() != 2 || f.results().Lines != 2 {
		t.Errorf("%d cells ran, %d lines", f.exec.count(), f.results().Lines)
	}
	m := f.manifest()
	if m.ClaudeCLI != "2.2.0" || len(m.Mixed) != 1 || m.Mixed[0].ClaudeCLI != "2.2.0" || len(m.Mixed[0].Differences) != 1 || !strings.Contains(m.Mixed[0].Differences[0], "claude CLI") {
		t.Errorf("manifest %+v", m)
	}
}

func TestRunStopsAtTheBudgetWithExitThreeAndContinuesWithAHigherOne(t *testing.T) {
	f := newRunFixture(t, "t-a", "t-b", "t-c", "t-d")
	f.exec.cost = 1

	stdout, err := f.call(f.args("--out", f.out, "--max-cost", "2.5", "--configs", "A", "--reps", "1"))
	if code := exitcode.FromError(err); code != exitcode.Budget {
		t.Fatalf("exit code %d (%v), want %d\n%s", code, err, exitcode.Budget, stdout)
	}
	if recs := f.results(); recs.Lines != 3 || recs.CostUSD != 3 {
		t.Errorf("%d lines, $%v; the cell started at $2 is the last", recs.Lines, recs.CostUSD)
	}
	if !strings.Contains(stdout, "1 not run: budget") {
		t.Errorf("output:\n%s", stdout)
	}

	stdout, err = f.call(f.args("--out", f.out, "--max-cost", "10", "--configs", "A", "--reps", "1"))
	if err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	if !strings.HasPrefix(stdout, "1 cell (4 tasks × 1 config × 1 rep, 3 already done)") || !strings.Contains(stdout, "total $4.00 of $10.00") {
		t.Errorf("output:\n%s", stdout)
	}
	if recs := f.results(); recs.Lines != 4 || len(recs.Cells) != 4 {
		t.Errorf("%d lines, %d cells", recs.Lines, len(recs.Cells))
	}
}

func TestRunRedoAppendsAndTheNewLineReplacesTheOld(t *testing.T) {
	f := newRunFixture(t, "t-a")
	if _, err := f.call(f.full("--configs", "A", "--reps", "1")); err != nil {
		t.Fatal(err)
	}

	if _, err := f.call(f.full("--configs", "A", "--reps", "1", "--redo")); err != nil {
		t.Fatal(err)
	}

	if recs := f.results(); recs.Lines != 2 || len(recs.Cells) != 1 || recs.Replaced != 1 {
		t.Errorf("%d lines, %d cells, %d replaced", recs.Lines, len(recs.Cells), recs.Replaced)
	}
}

func TestRunExitsOneWhenCellsAreLeftAsInfraError(t *testing.T) {
	f := newRunFixture(t, "t-a")
	newExec = func(env benchrun.Env) benchrun.ExecFunc {
		return func(ctx context.Context, c benchrun.Cell) (benchrun.Finished, error) {
			return benchrun.Finished{Record: results.Record{
				Task: c.Task.ID, Config: c.Config.Name, Rep: c.Rep, Result: results.InfraError, FailureReason: "setup: exit 1",
			}}, nil
		}
	}

	stdout, err := f.call(f.full("--configs", "A", "--reps", "1"))

	if code := exitcode.FromError(err); code != exitcode.Failed {
		t.Fatalf("exit code %d (%v), want 1\n%s", code, err, stdout)
	}
	if !strings.Contains(stdout, "infra") || !strings.Contains(stdout, "setup: exit 1") {
		t.Errorf("output:\n%s", stdout)
	}
}

func TestSplitConfigs(t *testing.T) {
	got, err := splitConfigs(" A, B ,C")
	if err != nil || strings.Join(got, "|") != "A|B|C" {
		t.Errorf("splitConfigs = %q, %v", got, err)
	}
	for _, bad := range []string{"", "A,", ",A", "A,B,A"} {
		if _, err := splitConfigs(bad); err == nil {
			t.Errorf("splitConfigs(%q) succeeded", bad)
		}
	}
}

func TestContributorPattern(t *testing.T) {
	for _, ok := range []string{"owner", "a", "Jane_Doe-2", "x.y"} {
		if !contributorPattern.MatchString(ok) {
			t.Errorf("%q is refused", ok)
		}
	}
	for _, bad := range []string{"", " ", "a b", "-x", "a/b", "ü", strings.Repeat("a", 33)} {
		if contributorPattern.MatchString(bad) {
			t.Errorf("%q is accepted", bad)
		}
	}
}

func TestListSomeCountsTheRest(t *testing.T) {
	if got := listSome([]string{"a", "b"}); got != "a, b" {
		t.Errorf("got %q", got)
	}
	if got := listSome([]string{"a", "b", "c", "d", "e", "f", "g"}); got != "a, b, c, d, e, and 2 more" {
		t.Errorf("got %q", got)
	}
}
