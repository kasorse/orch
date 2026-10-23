package story

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

func indexTasks(tasks []Task) map[string]Task {
	byID := make(map[string]Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	return byID
}

func sortedIDs(byID map[string]Task) []string {
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Validate checks a story's tasks as a whole (SPEC §7.4, §9.1): at most maxTasks of them, IDs
// of the right shape and unique, one prefix, every repo one of repos (the story's services),
// every dependency a task of the story and not the task itself, and no cycle. The first
// problem found is returned, checked in that order and, within a check, in the order of the
// IDs, so the same tasks always give the same message.
func Validate(tasks []Task, repos []string, maxTasks int) error {
	if len(tasks) == 0 {
		return errors.New("the story has no tasks")
	}
	if len(tasks) > maxTasks {
		return fmt.Errorf("the story has %d tasks, more than story.max_tasks (%d)", len(tasks), maxTasks)
	}

	byID := make(map[string]Task, len(tasks))
	for _, t := range tasks {
		if !ValidID(t.ID) {
			return fmt.Errorf("task ID %q: want two capital letters and two digits, like RF01", t.ID)
		}
		if _, dup := byID[t.ID]; dup {
			return fmt.Errorf("task ID %s is used by two tasks", t.ID)
		}
		byID[t.ID] = t
	}
	ids := sortedIDs(byID)

	prefix := ids[0][:2]
	for _, id := range ids {
		if id[:2] != prefix {
			return fmt.Errorf("task IDs must share one prefix: %s has %s, %s has %s", ids[0], prefix, id, id[:2])
		}
	}

	services := slices.Clone(repos)
	sort.Strings(services)
	for _, id := range ids {
		if !slices.Contains(services, byID[id].Repo) {
			return fmt.Errorf("%s: repository %q is not a service of the story (services: %s)", id, byID[id].Repo, strings.Join(services, ", "))
		}
	}

	for _, id := range ids {
		seen := map[string]bool{}
		for _, dep := range byID[id].DependsOn {
			switch {
			case dep == id:
				return fmt.Errorf("%s depends on itself", id)
			case seen[dep]:
				return fmt.Errorf("%s lists %s twice in depends_on", id, dep)
			}
			seen[dep] = true
			if _, ok := byID[dep]; !ok {
				return fmt.Errorf("%s depends on %s, which is not a task of the story", id, dep)
			}
		}
	}

	if _, stuck := kahn(byID); len(stuck) > 0 {
		cycle := findCycle(byID, stuck)
		return fmt.Errorf("dependency cycle: %s (each task depends on the next)", strings.Join(cycle, " -> "))
	}
	return nil
}

// kahn orders the tasks: among the tasks whose dependencies are all placed, the smallest ID
// goes next. Tasks that never become ready (a cycle, or a dependency on a task that does not
// exist, and whatever depends on those) are returned as stuck, in ID order.
func kahn(byID map[string]Task) (order, stuck []string) {
	ids := sortedIDs(byID)
	placed := make(map[string]bool, len(ids))
	for len(order) < len(ids) {
		next := ""
		for _, id := range ids {
			if placed[id] {
				continue
			}
			ready := true
			for _, dep := range byID[id].DependsOn {
				if !placed[dep] {
					ready = false
					break
				}
			}
			if ready {
				next = id
				break
			}
		}
		if next == "" {
			break
		}
		placed[next] = true
		order = append(order, next)
	}
	for _, id := range ids {
		if !placed[id] {
			stuck = append(stuck, id)
		}
	}
	return order, stuck
}

// findCycle walks the dependencies of the stuck tasks from the smallest ID, always to the
// smallest stuck dependency, until a task repeats, and returns that loop starting at its
// smallest ID and closed by repeating it: RF02, RF03, RF02.
func findCycle(byID map[string]Task, stuck []string) []string {
	inStuck := make(map[string]bool, len(stuck))
	for _, id := range stuck {
		inStuck[id] = true
	}
	at := map[string]int{}
	var path []string
	cur := stuck[0]
	for {
		if i, seen := at[cur]; seen {
			loop := slices.Clone(path[i:])
			low := 0
			for j, id := range loop {
				if id < loop[low] {
					low = j
				}
			}
			loop = append(loop[low:], loop[:low]...)
			return append(loop, loop[0])
		}
		at[cur] = len(path)
		path = append(path, cur)
		deps := slices.Clone(byID[cur].DependsOn)
		sort.Strings(deps)
		next := ""
		for _, dep := range deps {
			if inStuck[dep] {
				next = dep
				break
			}
		}
		if next == "" {
			return path
		}
		cur = next
	}
}

// Order is the order in which a story runs its tasks (SPEC §9.1): Kahn's algorithm over
// depends_on, the smallest ID first among the tasks that are ready. It is a pure function of
// the tasks. Call Validate first: a task that can never become ready is left out.
func Order(tasks []Task) []string {
	order, _ := kahn(indexTasks(tasks))
	return order
}

// ancestors is every task id depends on, directly or through others, in any repository.
func ancestors(byID map[string]Task, id string) map[string]bool {
	seen := map[string]bool{}
	stack := slices.Clone(byID[id].DependsOn)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		stack = append(stack, byID[cur].DependsOn...)
	}
	delete(seen, id)
	return seen
}

// Base is the task a task's branch is stacked on (SPEC §9.2). Of the task's ancestors,
// transitively and across repositories, it takes those in the task's own repository and drops
// each one that another of them depends on, directly or through others. None left: "", the
// repository's story base. One left: that task. More: an error, because the orchestrator
// does not decide how to merge branches.
func Base(tasks []Task, id string) (string, error) {
	keep, err := Parents(tasks, id)
	if err != nil {
		return "", err
	}
	switch len(keep) {
	case 0:
		return "", nil
	case 1:
		return keep[0], nil
	}
	return "", fmt.Errorf("%s needs %s in %s on different branches; make one depend on the other", id, joinAnd(keep), indexTasks(tasks)[id].Repo)
}

// Ready is the tasks to dispatch now, in Order: those whose every dependency is TaskDone and
// whose own state is neither TaskDone nor TaskRunning. A task with no entry in states is pending.
func Ready(tasks []Task, states map[string]TaskStatus) []string {
	byID := indexTasks(tasks)
	var ready []string
	for _, id := range Order(tasks) {
		if s := states[id]; s == TaskDone || s == TaskRunning {
			continue
		}
		ok := true
		for _, dep := range byID[id].DependsOn {
			if states[dep] != TaskDone {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, id)
		}
	}
	return ready
}

// Parents are the tasks a task starts from in its repository: its ancestors there,
// transitively and across repositories, minus each one that another of them depends on, sorted.
// The error is only for an unknown ID.
func Parents(tasks []Task, id string) ([]string, error) {
	byID := indexTasks(tasks)
	t, ok := byID[id]
	if !ok {
		return nil, fmt.Errorf("task %s is not in the story", id)
	}

	var same []string
	for a := range ancestors(byID, id) {
		if byID[a].Repo == t.Repo {
			same = append(same, a)
		}
	}
	sort.Strings(same)

	var keep []string
	for _, a := range same {
		needed := false
		for _, b := range same {
			if b != a && ancestors(byID, b)[a] {
				needed = true
				break
			}
		}
		if !needed {
			keep = append(keep, a)
		}
	}

	return keep, nil
}

func joinAnd(ids []string) string {
	if len(ids) == 1 {
		return ids[0]
	}
	return strings.Join(ids[:len(ids)-1], ", ") + " and " + ids[len(ids)-1]
}
