// Package run is `orchbench run` without the command line (specs/benchmark/SPEC.md §7, §13.5):
// the plan of cells, the cell itself, and the scheduler that runs them under a budget and
// appends one result line each.
package run

import (
	"fmt"
	"strings"

	"orchbench/internal/config"
	"orchbench/internal/results"
	"orchbench/internal/task"
)

// Source is a task to run and the hash of its file, which every result line of the task records.
type Source struct {
	Task   task.Task
	SHA256 string
}

// Cell is one task × config × repetition.
type Cell struct {
	Task       task.Task
	TaskSHA256 string
	Config     config.Config
	Rep        int
}

// Key is the cell as results.jsonl names it.
func (c Cell) Key() results.Cell {
	return results.Cell{Task: c.Task.ID, Config: c.Config.Name, Rep: c.Rep}
}

// Done maps the cells that have a result to their latest result.
func Done(latest []results.Record) map[results.Cell]string {
	done := make(map[results.Cell]string, len(latest))
	for _, r := range latest {
		done[r.Cell()] = r.Result
	}
	return done
}

// Plan is the cells to run, in order: task by task (tasks as given), and inside a task
// repetition by repetition with the configs interleaved, so that task1 gives A1 B1 C1 D1 A2 B2
// …. A model or rate-limit change during the benchmark then reaches every config alike. A cell
// whose latest result is pass or fail is left out and counted in skipped, unless redo is set;
// one with infra_error or no result is planned. Raising reps or adding a config therefore plans
// only the cells that are missing.
func Plan(tasks []Source, configs []config.Config, reps int, done map[results.Cell]string, redo bool) (cells []Cell, skipped int) {
	for _, s := range tasks {
		for rep := 1; rep <= reps; rep++ {
			for _, cfg := range configs {
				c := Cell{Task: s.Task, TaskSHA256: s.SHA256, Config: cfg, Rep: rep}
				if result := done[c.Key()]; !redo && (result == results.Pass || result == results.Fail) {
					skipped++
					continue
				}
				cells = append(cells, c)
			}
		}
	}
	return cells, skipped
}

// Header is the count line of a plan: the cells to run and what they were chosen from.
func Header(cells, tasks, configs, reps, skipped int) string {
	return fmt.Sprintf("%d %s (%d %s × %d %s × %d %s, %d already done), order: task-major, configs interleaved",
		cells, plural(cells, "cell", "cells"),
		tasks, plural(tasks, "task", "tasks"),
		configs, plural(configs, "config", "configs"),
		reps, plural(reps, "rep", "reps"),
		skipped)
}

// Line is one cell of a --dry-run listing.
func (c Cell) Line(taskWidth int) string {
	return fmt.Sprintf("%-*s  %s  rep%d", taskWidth, c.Task.ID, c.Config.Name, c.Rep)
}

// Width is the length of the longest task id of the cells.
func Width(cells []Cell) int {
	w := 0
	for _, c := range cells {
		w = max(w, len(c.Task.ID))
	}
	return w
}

// oneLine joins a multi-line message so that every cell stays one output line.
func oneLine(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
