package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"orchbench/internal/exitcode"
	"orchbench/internal/results"
)

// pass is the Exec of a model that solves every cell for cost dollars.
func pass(cost float64) ExecFunc {
	return func(ctx context.Context, c Cell) (Finished, error) {
		return Finished{Record: results.Record{
			Task: c.Task.ID, Config: c.Config.Name, Rep: c.Rep, Result: results.Pass, CostUSD: cost, WallMS: 1500,
		}}, nil
	}
}

// memory is an Appender that keeps the lines in the order they came.
type memory struct {
	mu   sync.Mutex
	recs []results.Record
	err  error
}

func (m *memory) Append(rec results.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.recs = append(m.recs, rec)
	return nil
}

func (m *memory) keys() []string {
	var out []string
	for _, r := range m.recs {
		out = append(out, fmt.Sprintf("%s:%s%d", r.Task, r.Config, r.Rep))
	}
	return out
}

func plan(t *testing.T, tasks, cfgs int, reps int) []Cell {
	t.Helper()
	var ids, names []string
	for i := 1; i <= tasks; i++ {
		ids = append(ids, fmt.Sprintf("t%02d", i))
	}
	for i := 0; i < cfgs; i++ {
		names = append(names, string(rune('A'+i)))
	}
	cells, _ := Plan(sources(ids...), configs(names...), reps, nil, false)
	return cells
}

func TestRunStopsAtTheBudgetAndStartsNothingNew(t *testing.T) {
	cells := plan(t, 1, 2, 3) // A1 B1 A2 B2 A3 B3
	var mem memory
	var out bytes.Buffer

	totals, err := Run(context.Background(), cells, Options{Jobs: 1, MaxCost: 2.5, Writer: &mem, Exec: pass(1)}, &out)

	var budget exitcode.BudgetError
	if !errors.As(err, &budget) || exitcode.FromError(err) != exitcode.Budget {
		t.Fatalf("error = %v, want a budget error (exit 3)", err)
	}
	want := []string{"t01:A1", "t01:B1", "t01:A2"}
	if got := mem.keys(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("lines %v, want %v: the cell that starts at $2 < $2.50 is the last", got, want)
	}
	if totals.Results != 3 || totals.NotRun != 3 || totals.Cost != 3 {
		t.Errorf("totals = %+v", totals)
	}
	if !strings.Contains(out.String(), "total $3.00 of $2.50") || !strings.Contains(out.String(), "3 not run: budget") {
		t.Errorf("output:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "higher --max-cost") {
		t.Errorf("error %q does not say how to continue", err)
	}
}

func TestRunContinuesWithAHigherLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.jsonl")
	all, _ := Plan(sources("t1"), configs("A", "B"), 3, nil, false)

	first := openWriter(t, path)
	_, err := Run(context.Background(), all, Options{Jobs: 1, MaxCost: 2.5, Writer: first, Exec: pass(1)}, nil)
	first.Close()
	if exitcode.FromError(err) != exitcode.Budget {
		t.Fatalf("first run: %v", err)
	}

	// The second call plans from the file: what is recorded is skipped, and the recorded cost
	// is where the budget starts.
	recs, err := results.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if recs.CostUSD != 3 || len(recs.Cells) != 3 {
		t.Fatalf("after the first run: cost %v, cells %d", recs.CostUSD, len(recs.Cells))
	}
	rest, skipped := Plan(sources("t1"), configs("A", "B"), 3, Done(recs.Cells), false)
	if skipped != 3 || len(rest) != 3 {
		t.Fatalf("second plan: %v, skipped %d", keys(rest), skipped)
	}

	second := openWriter(t, path)
	totals, err := Run(context.Background(), rest, Options{Jobs: 1, MaxCost: 10, InitialCost: recs.CostUSD, Writer: second, Exec: pass(1)}, nil)
	second.Close()
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if totals.Results != 3 || totals.Cost != 6 {
		t.Errorf("totals = %+v", totals)
	}

	final, err := results.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if final.Lines != 6 || len(final.Cells) != 6 || final.Replaced != 0 {
		t.Errorf("final file: %d lines, %d cells, %d replaced; want every cell once", final.Lines, len(final.Cells), final.Replaced)
	}
}

func openWriter(t *testing.T, path string) *results.Writer {
	t.Helper()
	w, err := results.OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestRunBudgetReachedWithNothingLeftIsNotAnError(t *testing.T) {
	cells := plan(t, 1, 3, 1)
	var mem memory

	totals, err := Run(context.Background(), cells, Options{Jobs: 1, MaxCost: 3, Writer: &mem, Exec: pass(1)}, nil)

	if err != nil || totals.Results != 3 || totals.NotRun != 0 {
		t.Fatalf("err = %v, totals = %+v", err, totals)
	}
}

func TestRunWithTheBudgetAlreadySpentStartsNothing(t *testing.T) {
	var mem memory
	var calls atomic.Int32
	exec := func(ctx context.Context, c Cell) (Finished, error) {
		calls.Add(1)
		return pass(1)(ctx, c)
	}

	_, err := Run(context.Background(), plan(t, 1, 2, 1), Options{Jobs: 2, MaxCost: 5, InitialCost: 5, Writer: &mem, Exec: exec}, nil)

	if exitcode.FromError(err) != exitcode.Budget || calls.Load() != 0 || len(mem.recs) != 0 {
		t.Fatalf("err = %v, %d cells started, %d lines", err, calls.Load(), len(mem.recs))
	}
}

func TestRunInterruptLeavesNoLineForTheUnfinishedCell(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.jsonl")
	all, _ := Plan(sources("t1"), configs("A", "B"), 1, nil, false)

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan string, 2)
	exec := func(ctx context.Context, c Cell) (Finished, error) {
		if c.Config.Name == "A" {
			return pass(1)(ctx, c)
		}
		started <- c.Config.Name
		<-ctx.Done() // the cell is still running when Ctrl-C comes
		return Finished{}, ctx.Err()
	}
	go func() {
		<-started
		cancel()
	}()

	w := openWriter(t, path)
	var out bytes.Buffer
	totals, err := Run(ctx, all, Options{Jobs: 1, MaxCost: 10, Writer: w, Exec: exec}, &out)
	w.Close()

	if !errors.Is(err, context.Canceled) || exitcode.FromError(err) != exitcode.Interrupted {
		t.Fatalf("error = %v, want an interrupt (exit 130)", err)
	}
	if totals.Results != 1 || totals.NotRun != 1 {
		t.Errorf("totals = %+v", totals)
	}
	if !strings.Contains(out.String(), "1 not run: interrupted") {
		t.Errorf("output:\n%s", out.String())
	}

	recs, err := results.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if recs.Lines != 1 || recs.Partial != 0 || recs.Cells[0].Config != "A" {
		t.Fatalf("file has %d lines (%d partial): %+v", recs.Lines, recs.Partial, recs.Cells)
	}

	// The next invocation plans the interrupted cell again.
	rest, skipped := Plan(sources("t1"), configs("A", "B"), 1, Done(recs.Cells), false)
	if got := keys(rest); len(got) != 1 || got[0] != "t1:B1" || skipped != 1 {
		t.Errorf("next plan %v (skipped %d), want only t1:B1", got, skipped)
	}
}

func TestRunWritesEveryCellOnceWithSeveralJobs(t *testing.T) {
	cells := plan(t, 10, 3, 1)
	var mem memory
	var running, peak atomic.Int32
	exec := func(ctx context.Context, c Cell) (Finished, error) {
		n := running.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		time.Sleep(2 * time.Millisecond)
		running.Add(-1)
		return pass(0.1)(ctx, c)
	}
	var out bytes.Buffer

	totals, err := Run(context.Background(), cells, Options{Jobs: 4, MaxCost: 100, Writer: &mem, Exec: exec}, &out)

	if err != nil || totals.Results != 30 {
		t.Fatalf("err = %v, totals = %+v", err, totals)
	}
	seen := make(map[string]int)
	for _, k := range mem.keys() {
		seen[k]++
	}
	if len(seen) != 30 {
		t.Errorf("%d distinct lines, want 30", len(seen))
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("%s written %d times", k, n)
		}
	}
	if peak.Load() < 2 {
		t.Errorf("at most %d cells ran at once with -j 4", peak.Load())
	}
	if lines := strings.Count(out.String(), "\n"); lines != 31 {
		t.Errorf("%d output lines, want 30 progress lines and a total:\n%s", lines, out.String())
	}
	if !strings.Contains(out.String(), "[30/30]") {
		t.Errorf("no [30/30] line:\n%s", out.String())
	}
}

func TestRunLeavesInfraErrorCellsAndGoesOn(t *testing.T) {
	cells := plan(t, 1, 3, 1)
	var mem memory
	exec := func(ctx context.Context, c Cell) (Finished, error) {
		if c.Config.Name != "B" {
			return pass(1)(ctx, c)
		}
		return Finished{Record: results.Record{
			Task: c.Task.ID, Config: "B", Rep: 1, Result: results.InfraError, FailureReason: "setup: exit 1",
		}}, nil
	}
	var out bytes.Buffer

	totals, err := Run(context.Background(), cells, Options{Jobs: 1, MaxCost: 10, Writer: &mem, Exec: exec}, &out)

	if err == nil || exitcode.FromError(err) != exitcode.Failed || !strings.Contains(err.Error(), "1 cell left as infra_error") {
		t.Fatalf("error = %v, want exit 1 naming the infra_error cell", err)
	}
	if totals.Results != 2 || totals.Infra != 1 || len(mem.recs) != 3 {
		t.Errorf("totals = %+v, %d lines", totals, len(mem.recs))
	}
	if !strings.Contains(out.String(), "2 results, 1 infra error") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestRunStopsWhenALineCannotBeWritten(t *testing.T) {
	cells := plan(t, 1, 3, 1)
	mem := memory{err: errors.New("disk full")}
	var calls atomic.Int32
	exec := func(ctx context.Context, c Cell) (Finished, error) {
		calls.Add(1)
		return pass(1)(ctx, c)
	}

	_, err := Run(context.Background(), cells, Options{Jobs: 1, MaxCost: 10, Writer: &mem, Exec: exec}, nil)

	if err == nil || !strings.Contains(err.Error(), "disk full") || exitcode.FromError(err) != exitcode.Failed {
		t.Fatalf("error = %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("%d cells ran after the writer failed, want 1", calls.Load())
	}
}

func TestRunReturnsAnExecutorError(t *testing.T) {
	var mem memory
	exec := func(ctx context.Context, c Cell) (Finished, error) { return Finished{}, errors.New("boom") }

	_, err := Run(context.Background(), plan(t, 1, 1, 1), Options{Jobs: 1, MaxCost: 10, Writer: &mem, Exec: exec}, nil)

	if err == nil || !strings.Contains(err.Error(), "t01 A rep1: boom") {
		t.Fatalf("error = %v", err)
	}
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestProgressLine(t *testing.T) {
	tests := []struct {
		name string
		fin  Finished
		want string
	}{
		{
			"a pass lists the stages",
			Finished{Record: results.Record{Task: "go-pgqueue-retry-backoff", Config: "B", Rep: 1, Result: results.Pass,
				CostUSD: 0.91, WallMS: 362_000, Stages: []string{"analysis", "development", "testing"}}},
			"[ 2/20] go-pgqueue-retry-backoff B rep1 pass $0.91 6m2s analysis,development,testing",
		},
		{
			"a pass of config A has no detail",
			Finished{Record: results.Record{Task: "go-pgqueue-retry-backoff", Config: "B", Rep: 1, Result: results.Pass, CostUSD: 0.62, WallMS: 190_000}},
			"[ 2/20] go-pgqueue-retry-backoff B rep1 pass $0.62 3m10s",
		},
		{
			"an oracle failure",
			Finished{Record: results.Record{Task: "go-pgqueue-retry-backoff", Config: "B", Rep: 1, Result: results.Fail,
				FailReason: results.FailOracle, OracleExit: 1, SuiteExit: 0, CostUSD: 0.55, WallMS: 260_000}},
			"[ 2/20] go-pgqueue-retry-backoff B rep1 fail $0.55 4m20s oracle: hidden tests exit 1",
		},
		{
			"a regression",
			Finished{Record: results.Record{Task: "go-pgqueue-retry-backoff", Config: "B", Rep: 1, Result: results.Fail,
				FailReason: results.FailRegression, SuiteExit: 1, CostUSD: 0.48, WallMS: 175_000}},
			"[ 2/20] go-pgqueue-retry-backoff B rep1 fail $0.48 2m55s regression: oracle ok, suite exit 1",
		},
		{
			"a timed-out run and a wrong answer",
			Finished{Record: results.Record{Task: "go-pgqueue-retry-backoff", Config: "B", Rep: 1, Result: results.Fail,
				FailReason: results.FailAnswer, AnswerChecksFailed: []string{`calc\.sh`}, RunStatus: "failed", FailureReason: "timeout", WallMS: 1000}},
			`[ 2/20] go-pgqueue-retry-backoff B rep1 fail $0.00 1s answer: no match for "calc\\.sh" (run failed: timeout)`,
		},
		{
			"an infra error without cost",
			Finished{Record: results.Record{Task: "go-pgqueue-retry-backoff", Config: "B", Rep: 1, Result: results.InfraError, FailureReason: "setup: pip install exit 1"}},
			"[ 2/20] go-pgqueue-retry-backoff B rep1 infra — — setup: pip install exit 1",
		},
		{
			"a kept workspace",
			Finished{Record: results.Record{Task: "go-pgqueue-retry-backoff", Config: "B", Rep: 1, Result: results.Pass, CostUSD: 1, WallMS: 60_000}, Kept: "/cache/work/x-1"},
			"[ 2/20] go-pgqueue-retry-backoff B rep1 pass $1.00 1m0s [kept: /cache/work/x-1]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collapse(progressLine(2, 20, 2, len("go-pgqueue-retry-backoff"), 1, tt.fin))
			if got != tt.want {
				t.Errorf("line\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
