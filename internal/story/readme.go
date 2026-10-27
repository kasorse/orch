package story

import (
	"fmt"
	"strings"
)

// TaskStatus is where a task stands in a story run (SPEC §10.2). pending: not dispatched;
// running: dispatched and its run not finished; done: its run completed with a commit;
// failed, stopped: its run ended so and the next story run resumes it; blocked: its run
// completed without a commit; conflict: its parents do not merge cleanly on the spec branch,
// so it is not dispatched.
type TaskStatus string

const (
	TaskPending  TaskStatus = "pending"
	TaskRunning  TaskStatus = "running"
	TaskDone     TaskStatus = "done"
	TaskFailed   TaskStatus = "failed"
	TaskBlocked  TaskStatus = "blocked"
	TaskStopped  TaskStatus = "stopped"
	TaskConflict TaskStatus = "conflict"
)

// Status is the story's own status line (SPEC §14): planned until the first story run, then
// running, and completed once every task is done; blocked, failed or stopped when a task is.
type Status string

const (
	StatusPlanned   Status = "planned"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusBlocked   Status = "blocked"
	StatusFailed    Status = "failed"
	StatusStopped   Status = "stopped"
)

// TaskState is what the story state knows about a task and the task file does not.
type TaskState struct {
	Status TaskStatus
	Branch string
	// MRIID and MRURL are the task's merge request, 0 and "" for none.
	MRIID int
	MRURL string
}

// cell makes s fit in one cell of a Markdown table.
func cell(s string) string {
	return strings.ReplaceAll(oneLine(s), "|", `\|`)
}

// RenderREADME is the generated README.md of a spec directory (SPEC §8): the title, the
// status line, the task table in the story's order with a tick for every done task, and how
// to run it. states is keyed by task ID; a task without an entry is pending. The output is a
// pure function of the arguments.
func RenderREADME(title string, status Status, tasks []Task, states map[string]TaskState) string {
	byID := indexTasks(tasks)
	order := Order(tasks)
	listed := make(map[string]bool, len(order))
	for _, id := range order {
		listed[id] = true
	}
	var rest []string
	for _, id := range sortedIDs(byID) {
		if !listed[id] {
			rest = append(rest, id)
		}
	}
	order = append(order, rest...)

	done := 0
	var rows strings.Builder
	for _, id := range order {
		t := byID[id]
		st := states[id]
		if st.Status == "" {
			st.Status = TaskPending
		}
		mark := "[ ]"
		if st.Status == TaskDone {
			mark = "[x]"
			done++
		}
		deps := "—"
		if len(t.DependsOn) > 0 {
			deps = strings.Join(t.DependsOn, ", ")
		}
		jira := "—"
		if t.Jira != "" && t.Jira != JiraNone {
			jira = t.Jira
		}
		branch := "—"
		if st.Branch != "" {
			branch = "`" + st.Branch + "`"
			if st.MRIID > 0 && st.MRURL != "" {
				branch += fmt.Sprintf(" · [!%d](%s)", st.MRIID, st.MRURL)
			}
		}
		fmt.Fprintf(&rows, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			mark, id, cell(t.Title), cell(t.Repo), cell(deps), cell(jira), cell(string(st.Status)), cell(branch))
	}

	var b strings.Builder
	b.WriteString("# " + oneLine(title) + "\n\n")
	fmt.Fprintf(&b, "Status: **%s** (%d of %d tasks done)\n\n", status, done, len(order))
	b.WriteString("| done | task | title | repository | depends on | jira | status | branch |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	b.WriteString(rows.String())
	b.WriteString("\n")
	b.WriteString("Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.\n")
	b.WriteString("This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.\n")
	return b.String()
}
