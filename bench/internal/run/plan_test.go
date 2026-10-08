package run

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"orchbench/internal/config"
	"orchbench/internal/results"
	"orchbench/internal/task"
)

func sources(ids ...string) []Source {
	out := make([]Source, len(ids))
	for i, id := range ids {
		out[i] = Source{Task: task.Task{ID: id}, SHA256: "hash-" + id}
	}
	return out
}

func configs(names ...string) []config.Config {
	out := make([]config.Config, len(names))
	for i, name := range names {
		out[i] = config.Config{Name: name}
	}
	return out
}

// keys names the cells as task:config+rep, in order.
func keys(cells []Cell) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = fmt.Sprintf("%s:%s%d", c.Task.ID, c.Config.Name, c.Rep)
	}
	return out
}

func doneCells(result string, cells ...string) map[results.Cell]string {
	done := make(map[results.Cell]string)
	for _, c := range cells {
		var id, cfg string
		var rep int
		if _, err := fmt.Sscanf(strings.NewReplacer(":", " ", "+", " ").Replace(c), "%s %s %d", &id, &cfg, &rep); err != nil {
			panic(c + ": " + err.Error())
		}
		done[results.Cell{Task: id, Config: cfg, Rep: rep}] = result
	}
	return done
}

func TestPlanOrderIsTaskMajorWithConfigsInterleavedPerRepetition(t *testing.T) {
	cells, skipped := Plan(sources("t1", "t2"), configs("A", "B"), 2, nil, false)

	want := []string{
		"t1:A1", "t1:B1", "t1:A2", "t1:B2",
		"t2:A1", "t2:B1", "t2:A2", "t2:B2",
	}
	if got := keys(cells); !reflect.DeepEqual(got, want) || skipped != 0 {
		t.Errorf("plan %v (skipped %d), want %v", got, skipped, want)
	}
}

func TestPlanCarriesTheTaskHashAndTheConfig(t *testing.T) {
	cells, _ := Plan(sources("t1"), configs("A"), 1, nil, false)
	if len(cells) != 1 || cells[0].TaskSHA256 != "hash-t1" || cells[0].Config.Name != "A" || cells[0].Task.ID != "t1" {
		t.Fatalf("cells = %+v", cells)
	}
	if cells[0].Key() != (results.Cell{Task: "t1", Config: "A", Rep: 1}) {
		t.Errorf("key = %+v", cells[0].Key())
	}
}

func TestPlanSkipsPassAndFailAndPlansInfraAndMissing(t *testing.T) {
	done := doneCells(results.Pass, "t1:A+1")
	for k, v := range doneCells(results.Fail, "t1:B+1") {
		done[k] = v
	}
	for k, v := range doneCells(results.InfraError, "t2:A+1") {
		done[k] = v
	}

	cells, skipped := Plan(sources("t1", "t2"), configs("A", "B"), 1, done, false)

	want := []string{"t2:A1", "t2:B1"}
	if got := keys(cells); !reflect.DeepEqual(got, want) || skipped != 2 {
		t.Errorf("plan %v (skipped %d), want %v (skipped 2): infra_error is run again", got, skipped, want)
	}
}

func TestPlanRedoRunsEverythingAgain(t *testing.T) {
	done := doneCells(results.Pass, "t1:A+1", "t1:B+1")

	cells, skipped := Plan(sources("t1"), configs("A", "B"), 1, done, true)

	if got := keys(cells); !reflect.DeepEqual(got, []string{"t1:A1", "t1:B1"}) || skipped != 0 {
		t.Errorf("plan %v (skipped %d)", got, skipped)
	}
}

func TestPlanLargerRepsAddsOnlyTheMissingRepetitions(t *testing.T) {
	done := doneCells(results.Pass, "t1:A+1", "t1:B+1")

	cells, skipped := Plan(sources("t1"), configs("A", "B"), 3, done, false)

	want := []string{"t1:A2", "t1:B2", "t1:A3", "t1:B3"}
	if got := keys(cells); !reflect.DeepEqual(got, want) || skipped != 2 {
		t.Errorf("plan %v (skipped %d), want %v", got, skipped, want)
	}
}

func TestPlanNewConfigAddsOnlyItsCells(t *testing.T) {
	done := doneCells(results.Pass, "t1:A+1", "t1:B+1", "t1:A+2", "t1:B+2")

	cells, skipped := Plan(sources("t1"), configs("E"), 2, done, false)

	want := []string{"t1:E1", "t1:E2"}
	if got := keys(cells); !reflect.DeepEqual(got, want) || skipped != 0 {
		t.Errorf("plan %v (skipped %d), want %v", got, skipped, want)
	}
}

func TestPlanOfNothingToDo(t *testing.T) {
	done := doneCells(results.Pass, "t1:A+1")
	cells, skipped := Plan(sources("t1"), configs("A"), 1, done, false)
	if len(cells) != 0 || skipped != 1 {
		t.Errorf("plan %v (skipped %d)", keys(cells), skipped)
	}
}

func TestDoneKeepsTheLatestResultOfEachCell(t *testing.T) {
	latest := []results.Record{
		{Task: "t1", Config: "A", Rep: 1, Result: results.Pass},
		{Task: "t1", Config: "B", Rep: 1, Result: results.InfraError},
	}
	done := Done(latest)
	if len(done) != 2 || done[results.Cell{Task: "t1", Config: "A", Rep: 1}] != results.Pass {
		t.Errorf("done = %v", done)
	}
}

func TestHeaderAndLines(t *testing.T) {
	want := "20 cells (5 tasks × 4 configs × 1 rep, 0 already done), order: task-major, configs interleaved"
	if got := Header(20, 5, 4, 1, 0); got != want {
		t.Errorf("header = %q, want %q", got, want)
	}
	want = "1 cell (1 task × 1 config × 3 reps, 2 already done), order: task-major, configs interleaved"
	if got := Header(1, 1, 1, 3, 2); got != want {
		t.Errorf("header = %q, want %q", got, want)
	}

	cells, _ := Plan(sources("go-a", "b"), configs("A"), 1, nil, false)
	width := Width(cells)
	if width != 4 {
		t.Fatalf("width = %d", width)
	}
	if got := cells[1].Line(width); got != "b     A  rep1" {
		t.Errorf("line = %q", got)
	}
}
