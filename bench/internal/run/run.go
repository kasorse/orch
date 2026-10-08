package run

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"orchbench/internal/exitcode"
	"orchbench/internal/results"
)

// Appender is where a finished cell's line goes: a *results.Writer.
type Appender interface {
	Append(rec results.Record) error
}

// Options configure Run.
type Options struct {
	// Jobs is how many cells run at once; less than 1 is 1.
	Jobs int
	// MaxCost is the budget in dollars. Once the recorded cost, InitialCost plus the lines
	// appended by this call, is at or above it, no new cell starts.
	MaxCost     float64
	InitialCost float64
	Writer      Appender
	Exec        ExecFunc
}

// Totals is what a call to Run did.
type Totals struct {
	// Results counts the pass and fail lines written, Infra the infra_error ones, NotRun the
	// planned cells that got no line (the budget or an interrupt stopped them).
	Results int
	Infra   int
	NotRun  int
	// Cost is the recorded cost at the end: InitialCost plus this call's lines.
	Cost    float64
	Elapsed time.Duration
}

// Run runs the cells, Jobs at a time, in the order given, appends one line per cell and prints
// one progress line per cell and a total to out.
//
// Before it hands out a cell it compares the recorded cost with MaxCost: at or above it nothing
// new starts, the running cells finish and are recorded, and the error is an
// exitcode.BudgetError. A cancelled ctx stops the running cells without a line for them and
// returns ctx's error. Cells left as infra_error make the error non-nil too, after all the
// others ran.
//
// Run does not signal processes itself: cancelling the context is what kills a cell's children,
// because runner.Run and sandbox start each one in its own process group and kill the group when
// the context ends.
func Run(ctx context.Context, cells []Cell, opts Options, out io.Writer) (Totals, error) {
	if out == nil {
		out = io.Discard
	}
	begin := time.Now()
	taskW, cfgW := Width(cells), configWidth(cells)
	countW := len(strconv.Itoa(len(cells)))

	// A failing writer cancels the cells still running: their lines could not be kept.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// mu guards the variables after it and serialises the output.
	var mu sync.Mutex
	spent := opts.InitialCost
	var (
		next      int
		finished  int
		totals    Totals
		budgetHit bool
		failure   error
	)
	fail := func(err error) {
		mu.Lock()
		if failure == nil {
			failure = err
		}
		mu.Unlock()
		cancel()
	}

	worker := func() {
		for {
			mu.Lock()
			if failure != nil || runCtx.Err() != nil || next >= len(cells) {
				mu.Unlock()
				return
			}
			if spent >= opts.MaxCost {
				budgetHit = true
				mu.Unlock()
				return
			}
			c := cells[next]
			next++
			mu.Unlock()

			fin, err := opts.Exec(runCtx, c)
			if err != nil {
				if runCtx.Err() == nil {
					fail(fmt.Errorf("%s %s rep%d: %w", c.Task.ID, c.Config.Name, c.Rep, err))
				}
				return
			}
			if err := opts.Writer.Append(fin.Record); err != nil {
				fail(fmt.Errorf("writing the result of %s %s rep%d: %w", c.Task.ID, c.Config.Name, c.Rep, err))
				return
			}

			mu.Lock()
			spent += fin.Record.CostUSD
			finished++
			if fin.Record.Result == results.InfraError {
				totals.Infra++
			} else {
				totals.Results++
			}
			fmt.Fprintln(out, progressLine(finished, len(cells), countW, taskW, cfgW, fin))
			mu.Unlock()
		}
	}

	var wg sync.WaitGroup
	for range max(opts.Jobs, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker()
		}()
	}
	wg.Wait()

	totals.NotRun = len(cells) - finished
	totals.Cost = spent
	totals.Elapsed = time.Since(begin)

	why := ""
	switch {
	case budgetHit:
		why = "budget"
	case ctx.Err() != nil:
		why = "interrupted"
	}
	fmt.Fprintln(out, totalLine(totals, opts.MaxCost, why))

	switch {
	case ctx.Err() != nil:
		return totals, ctx.Err()
	case failure != nil:
		return totals, failure
	case budgetHit:
		return totals, exitcode.BudgetError{Msg: fmt.Sprintf(
			"stopped by --max-cost: $%.2f recorded of $%.2f, %d cells not started; run the same command with a higher --max-cost to continue",
			totals.Cost, opts.MaxCost, totals.NotRun)}
	case totals.Infra > 0:
		return totals, fmt.Errorf("%d %s left as infra_error; run the same command again to retry %s",
			totals.Infra, plural(totals.Infra, "cell", "cells"), plural(totals.Infra, "it", "them"))
	}
	return totals, nil
}

func configWidth(cells []Cell) int {
	w := 0
	for _, c := range cells {
		w = max(w, len(c.Config.Name))
	}
	return w
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// totalLine is the last line of a run (SPEC.md §13.10). why says what left cells unstarted.
func totalLine(t Totals, maxCost float64, why string) string {
	line := fmt.Sprintf("total $%.2f of $%.2f, %s, %d %s, %d infra %s",
		t.Cost, maxCost, t.Elapsed.Round(time.Second),
		t.Results, plural(t.Results, "result", "results"),
		t.Infra, plural(t.Infra, "error", "errors"))
	if t.NotRun > 0 && why != "" {
		line += fmt.Sprintf(", %d not run: %s", t.NotRun, why)
	}
	return line
}

// progressLine is one cell of the output (SPEC.md §13.10): its place in completion order, which
// with -j is not the plan's order, the cell, result, cost, time and a detail.
func progressLine(n, total, countW, taskW, cfgW int, fin Finished) string {
	r := fin.Record
	word, cost, wall := r.Result, fmt.Sprintf("$%.2f", r.CostUSD), (time.Duration(r.WallMS) * time.Millisecond).Round(time.Second).String()
	if r.Result == results.InfraError {
		word = "infra"
		if r.CostUSD == 0 {
			cost, wall = "—", "—"
		}
	}

	line := fmt.Sprintf("[%*d/%d] %-*s  %-*s  rep%d  %-5s  %7s  %7s  %s",
		countW, n, total, taskW, r.Task, cfgW, r.Config, r.Rep, word, cost, wall, detail(r))
	line = strings.TrimRight(line, " ")
	if fin.Kept != "" {
		line += " [kept: " + fin.Kept + "]"
	}
	return line
}

// detail is what a progress line says after the numbers: the stages of a pass, why a cell
// failed, the reason of an infra error.
func detail(r results.Record) string {
	switch r.Result {
	case results.Pass:
		return strings.Join(r.Stages, ",")
	case results.InfraError:
		return oneLine(r.FailureReason)
	}

	var d string
	switch r.FailReason {
	case results.FailOracle:
		d = "oracle: hidden tests " + exited(r.OracleExit)
	case results.FailRegression:
		d = "regression: oracle ok, suite " + exited(r.SuiteExit)
	case results.FailAnswer:
		quoted := make([]string, len(r.AnswerChecksFailed))
		for i, c := range r.AnswerChecksFailed {
			quoted[i] = strconv.Quote(c)
		}
		d = "answer: no match for " + strings.Join(quoted, ", ")
	case results.FailTreeChanged:
		d = "tree_changed: the question's tree was changed"
	default:
		d = r.FailReason
	}
	if r.RunStatus == "failed" && r.FailureReason != "" {
		d += " (run failed: " + r.FailureReason + ")"
	}
	return oneLine(d)
}
