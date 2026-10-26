package story

import (
	"reflect"
	"strings"
	"testing"
)

func tk(id, repo string, deps ...string) Task {
	return Task{ID: id, Repo: repo, DependsOn: deps}
}

func TestOrder(t *testing.T) {
	cases := []struct {
		name  string
		tasks []Task
		want  []string
	}{
		{"ties go to the smallest ID", []Task{tk("RF03", "a"), tk("RF01", "a"), tk("RF02", "a", "RF01"), tk("RF04", "a", "RF03")}, []string{"RF01", "RF02", "RF03", "RF04"}},
		{"a dependency beats the ID", []Task{tk("RF01", "a", "RF03"), tk("RF02", "a"), tk("RF03", "a")}, []string{"RF02", "RF03", "RF01"}},
		{"a chain", []Task{tk("RF03", "a", "RF02"), tk("RF02", "a", "RF01"), tk("RF01", "a")}, []string{"RF01", "RF02", "RF03"}},
		{"a diamond", []Task{tk("RF04", "a", "RF02", "RF03"), tk("RF03", "a", "RF01"), tk("RF02", "a", "RF01"), tk("RF01", "a")}, []string{"RF01", "RF02", "RF03", "RF04"}},
		{"independent tasks", []Task{tk("RF02", "a"), tk("RF01", "b")}, []string{"RF01", "RF02"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Order(tc.tasks); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Order = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParents(t *testing.T) {
	tasks := []Task{
		tk("RF01", "a"), tk("RF02", "a", "RF01"), tk("RF03", "b", "RF01"),
		tk("RF04", "a"), tk("RF05", "a", "RF04", "RF02"), tk("RF06", "a", "RF03"),
	}
	cases := []struct {
		id   string
		want []string
	}{
		{"RF01", nil},
		{"RF02", []string{"RF01"}},
		{"RF03", nil},
		{"RF05", []string{"RF02", "RF04"}},
		{"RF06", []string{"RF01"}},
	}
	for _, tc := range cases {
		got, err := Parents(tasks, tc.id)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Parents(%s) = %v, %v; want %v", tc.id, got, err, tc.want)
		}
	}
	if _, err := Parents(tasks, "RF99"); err == nil {
		t.Error("unknown ID: want an error")
	}
}

func TestReady(t *testing.T) {
	tasks := []Task{tk("RF01", "a"), tk("RF02", "a", "RF01"), tk("RF03", "b"), tk("RF04", "a", "RF02", "RF03")}
	cases := []struct {
		name   string
		states map[string]TaskStatus
		want   []string
	}{
		{"nothing started", nil, []string{"RF01", "RF03"}},
		{"running and done are skipped", map[string]TaskStatus{"RF01": TaskDone, "RF03": TaskRunning}, []string{"RF02"}},
		{"all dependencies done", map[string]TaskStatus{"RF01": TaskDone, "RF02": TaskDone, "RF03": TaskDone}, []string{"RF04"}},
		{"retryable states", map[string]TaskStatus{"RF01": TaskDone, "RF02": TaskFailed, "RF03": TaskConflict}, []string{"RF02", "RF03"}},
		{"blocked and stopped", map[string]TaskStatus{"RF01": TaskBlocked, "RF03": TaskStopped}, []string{"RF01", "RF03"}},
		{"all done", map[string]TaskStatus{"RF01": TaskDone, "RF02": TaskDone, "RF03": TaskDone, "RF04": TaskDone}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Ready(tasks, tc.states); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Ready = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOrderDoesNotDependOnInputOrder(t *testing.T) {
	a := []Task{tk("RF01", "a"), tk("RF02", "a", "RF01"), tk("RF03", "b", "RF01"), tk("RF04", "a", "RF02", "RF03")}
	b := []Task{a[3], a[2], a[1], a[0]}
	if !reflect.DeepEqual(Order(a), Order(b)) {
		t.Errorf("%v != %v", Order(a), Order(b))
	}
}

func TestOrderLeavesOutWhatCannotRun(t *testing.T) {
	got := Order([]Task{tk("RF01", "a"), tk("RF02", "a", "RF03"), tk("RF03", "a", "RF02"), tk("RF04", "a", "RF09")})
	if !reflect.DeepEqual(got, []string{"RF01"}) {
		t.Errorf("Order = %v", got)
	}
}

func TestValidate(t *testing.T) {
	repos := []string{"gateway", "billing"}
	good := []Task{tk("RF01", "billing"), tk("RF02", "gateway", "RF01"), tk("RF03", "billing", "RF01")}
	if err := Validate(good, repos, 3); err != nil {
		t.Fatalf("a valid story: %v", err)
	}

	cases := []struct {
		name  string
		tasks []Task
		repos []string
		max   int
		want  []string
	}{
		{"no tasks", nil, repos, 5, []string{"no tasks"}},
		{"too many", good, repos, 2, []string{"3 tasks", "story.max_tasks (2)"}},
		{"bad ID", []Task{tk("R1", "billing")}, repos, 5, []string{`"R1"`}},
		{"lowercase ID", []Task{tk("rf01", "billing")}, repos, 5, []string{`"rf01"`}},
		{"duplicate ID", []Task{tk("RF01", "billing"), tk("RF01", "gateway")}, repos, 5, []string{"RF01", "two tasks"}},
		{"two prefixes", []Task{tk("RF01", "billing"), tk("AB02", "billing")}, repos, 5, []string{"share one prefix", "AB02", "RF01"}},
		{"unknown repository", []Task{tk("RF01", "billing"), tk("RF02", "ledger")}, repos, 5, []string{"RF02", `"ledger"`, "billing, gateway"}},
		{"empty repository", []Task{tk("RF01", "")}, repos, 5, []string{"RF01", `""`}},
		{"repository case matters", []Task{tk("RF01", "Billing")}, repos, 5, []string{"RF01", `"Billing"`}},
		{"self dependency", []Task{tk("RF01", "billing", "RF01")}, repos, 5, []string{"RF01 depends on itself"}},
		{"dependency listed twice", []Task{tk("RF01", "billing"), tk("RF02", "billing", "RF01", "RF01")}, repos, 5, []string{"RF02", "RF01 twice"}},
		{"missing dependency", []Task{tk("RF01", "billing"), tk("RF03", "billing", "RF09")}, repos, 5, []string{"RF03 depends on RF09, which is not a task of the story"}},
		{"cycle of three", []Task{tk("RF01", "billing", "RF03"), tk("RF02", "billing", "RF01"), tk("RF03", "billing", "RF02")}, repos, 5, []string{"dependency cycle: RF01 -> RF03 -> RF02 -> RF01"}},
		{"cycle of two", []Task{tk("RF01", "billing"), tk("RF02", "billing", "RF03"), tk("RF03", "billing", "RF02")}, repos, 5, []string{"dependency cycle: RF02 -> RF03 -> RF02"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.tasks, tc.repos, tc.max)
			if err == nil {
				t.Fatal("want an error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestValidateCycleNamesOnlyTheLoop(t *testing.T) {
	// RF01 waits on a loop and RF04 is waited on by it: neither is in the loop
	tasks := []Task{tk("RF01", "a", "RF02"), tk("RF02", "a", "RF03"), tk("RF03", "a", "RF02"), tk("RF04", "a", "RF03")}
	err := Validate(tasks, []string{"a"}, 5)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "RF02 -> RF03 -> RF02") || strings.Contains(err.Error(), "RF01") || strings.Contains(err.Error(), "RF04") {
		t.Errorf("error %q", err)
	}
}

func TestValidateIsDeterministic(t *testing.T) {
	tasks := []Task{tk("RF03", "x"), tk("RF01", "y"), tk("RF02", "z")}
	first := Validate(tasks, []string{"a"}, 5)
	for i := 0; i < 20; i++ {
		if again := Validate(tasks, []string{"a"}, 5); again.Error() != first.Error() {
			t.Fatalf("%q != %q", again, first)
		}
	}
	if !strings.HasPrefix(first.Error(), "RF01:") {
		t.Errorf("the first problem should be the smallest ID's: %v", first)
	}
}

// Several parents are no error: the story merges them on its spec branch.
func TestParentsSeveral(t *testing.T) {
	diamond := []Task{tk("RF01", "billing"), tk("RF02", "billing", "RF01"), tk("RF03", "billing", "RF01"), tk("RF04", "billing", "RF02", "RF03")}
	if got, err := Parents(diamond, "RF04"); err != nil || !reflect.DeepEqual(got, []string{"RF02", "RF03"}) {
		t.Errorf("Parents(RF04) = %v, %v; want RF02 and RF03", got, err)
	}
	// the two branches are met through another repository
	across := []Task{tk("RF01", "billing"), tk("RF02", "billing"), tk("RF03", "gateway", "RF01", "RF02"), tk("RF04", "billing", "RF03")}
	if got, err := Parents(across, "RF04"); err != nil || !reflect.DeepEqual(got, []string{"RF01", "RF02"}) {
		t.Errorf("Parents(RF04) = %v, %v; want RF01 and RF02", got, err)
	}
	// a cycle must not hang Parents
	cyc := []Task{tk("RF01", "a", "RF02"), tk("RF02", "a", "RF01")}
	if _, err := Parents(cyc, "RF01"); err != nil {
		t.Errorf("RF01: %v", err)
	}
}
