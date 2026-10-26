package logging

import (
	"fmt"
	"os"
	"strings"
)

// The lines of story plan (specs/story/SPEC.md §6, §7.1, §7.4): the services found, the
// planning calls, where the story's files are, and how it ended - a spec on disk, or one task
// and the command that runs it.

// StoryService is one of a story's services as the Services line shows it.
type StoryService struct {
	Name string
	Path string
}

// userHomeDir is where "~/" points in a path the lines show; a variable so tests can fix it.
var userHomeDir = os.UserHomeDir

// StoryServices prints the services the task names, before the first call (§7.1):
// "Services: billing (~/src/billing), gateway (~/src/gateway)".
func (r *ConsoleReporter) StoryServices(services []StoryService) {
	parts := make([]string, len(services))
	for i, s := range services {
		parts[i] = s.Name + " (" + tildePath(s.Path) + ")"
	}
	list := strings.Join(parts, ", ")
	if r.Pretty {
		r.stopSpin()
		r.bullet(r.st().blue, "Services", list)
		return
	}
	fmt.Fprintf(r.Out, "Services: %s\n", list)
}

// StoryPlanDir names the directory that holds the story's ticket, artifacts and logs.
func (r *ConsoleReporter) StoryPlanDir(dir string) {
	if r.Pretty {
		r.detail(r.st().gray("story directory " + dir))
		return
	}
	fmt.Fprintf(r.Out, "Story directory: %s\n", dir)
}

// StorySurvey announces the survey of one service.
func (r *ConsoleReporter) StorySurvey(service string) {
	if r.Pretty {
		r.stopSpin()
		r.bullet(r.st().accent, "Survey", service)
		return
	}
	fmt.Fprintf(r.Out, "\nSurvey of %s\n", service)
}

// StorySpecify announces the specify call.
func (r *ConsoleReporter) StorySpecify() {
	if r.Pretty {
		r.stopSpin()
		r.bullet(r.st().accent, "Specify", "the spec and its tasks")
		return
	}
	fmt.Fprintf(r.Out, "\nSpecify\n")
}

// StoryPlanKept says where a plan that did not reach a spec left its prompts, answers and
// logs.
func (r *ConsoleReporter) StoryPlanKept(dir string) {
	r.stopSpin()
	text := "The plan's prompts, answers and logs are kept in " + dir
	if r.Pretty {
		fmt.Fprintln(r.Out, r.st().yellow("⚠ "+text))
		return
	}
	fmt.Fprintln(r.Out, text)
}

// StoryOneTask reports an answer of one task (§7.4): it is not a story, nothing is written
// under the spec directory, and the command that runs it. The title is a model's and the path
// is the operator's, so both are quoted for the shell in Go.
func (r *ConsoleReporter) StoryOneTask(repo, repoPath, title string) {
	r.stopSpin()
	cmd := "cd " + shellWord(repoPath) + " && orchestrator run " + shellQuote(title)
	if r.Pretty {
		st := r.st()
		fmt.Fprintln(r.Out)
		r.bullet(st.green, "One task", "not a story: no spec is written")
		r.detail(repo + ": " + title)
		r.detail("run it with  " + st.bold(cmd))
		return
	}
	fmt.Fprintf(r.Out, "\nThe answer is one task, not a story: no spec is written.\nTask (%s): %s\nRun it with:\n  %s\n", repo, title, cmd)
}

// StorySpecWritten reports the spec on disk and what comes next.
func (r *ConsoleReporter) StorySpecWritten(dir string, tasks int) {
	r.stopSpin()
	next := "read it, edit it, commit it, then: orchestrator story run " + shellWord(dir)
	if r.Pretty {
		fmt.Fprintln(r.Out)
		r.bullet(r.st().green, "Spec written", fmt.Sprintf("%s · %d tasks", dir, tasks))
		r.detail(next)
		return
	}
	fmt.Fprintf(r.Out, "\n✓ Spec written to %s (%d tasks)\nNext: %s\n", dir, tasks, next)
}

// The lines of story run (specs/story/SPEC.md §10): the plan printed at the first start, the
// header above each child's output, what became of each task, and the table at the end.

// StoryPlanTask is one task of the plan story run prints at the first start.
type StoryPlanTask struct {
	ID     string
	Repo   string
	Base   string // what the task starts from: the story base branch or the branch it is stacked on
	Branch string
	Jira   string // the key, or ""
}

// StoryEndRow is one task of the table that ends a completed story (§10.4).
type StoryEndRow struct {
	ID        string
	Repo      string
	Branch    string
	Target    string // the branch its merge request targets
	Commit    string
	Jira      string
	Conflicts string // what the conflict check found, "" for none
}

func storyCount(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func shortCommit(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// StoryRunPlan prints the plan at the first start: the order, and for each task its repository,
// branch and base, and Jira key.
func (r *ConsoleReporter) StoryRunPlan(tasks []StoryPlanTask, repos int) {
	r.stopSpin()
	head := storyCount(len(tasks), "task", "tasks") + " in " + storyCount(repos, "repository", "repositories")
	lines := make([]string, len(tasks))
	for i, t := range tasks {
		lines[i] = fmt.Sprintf("%s · %s · %s · from %s", t.ID, t.Repo, t.Branch, t.Base)
		if t.Jira != "" {
			lines[i] += " · " + t.Jira
		}
	}
	if r.Pretty {
		fmt.Fprintln(r.Out)
		r.bullet(r.st().accent, "Story plan", head)
		for _, l := range lines {
			r.detail(l)
		}
		return
	}
	fmt.Fprintf(r.Out, "\nStory plan: %s\n", head)
	for _, l := range lines {
		fmt.Fprintf(r.Out, "  %s\n", l)
	}
}

// StoryConfirm asks the one question of the first start and leaves the cursor after it; the
// answer is read by the caller.
func (r *ConsoleReporter) StoryConfirm(tasks, repos int) {
	r.stopSpin()
	fmt.Fprintf(r.Out, "\nRun %s in %s? [Y/n] ", storyCount(tasks, "task", "tasks"), storyCount(repos, "repository", "repositories"))
}

// StoryNote prints a one-line remark that is not a step: why something is skipped, or what was
// done on the way.
func (r *ConsoleReporter) StoryNote(text string) {
	r.stopSpin()
	fmt.Fprintf(r.Out, "Note: %s\n", text)
}

// StoryCancelled says the operator answered no, and that nothing was written.
func (r *ConsoleReporter) StoryCancelled() {
	fmt.Fprintln(r.Out, "Cancelled: nothing was started and nothing was written.")
}

// StoryChecking announces the pre-run checks of one repository, whose orchestrator validate runs
// next with its output under this line.
func (r *ConsoleReporter) StoryChecking(repo, path string) {
	r.stopSpin()
	if r.Pretty {
		r.bullet(r.st().blue, "Checking "+repo, tildePath(path))
		return
	}
	fmt.Fprintf(r.Out, "\nChecking %s (%s)\n", repo, tildePath(path))
}

// StoryTaskHeader is the line above a child's output: "── RF02 · gateway · 2/6 ──".
func (r *ConsoleReporter) StoryTaskHeader(id, repo string, n, total int) {
	r.stopSpin()
	line := fmt.Sprintf("── %s · %s · %d/%d ──", id, repo, n, total)
	if r.Pretty {
		line = r.st().bold(r.st().accent(line))
	}
	fmt.Fprintf(r.Out, "\n%s\n", line)
}

// StoryTaskStarted is the one line of a task started beside others, whose output goes to a log
// file instead of the console: "▶ RF02 · gateway · 2/6 · log <path>".
func (r *ConsoleReporter) StoryTaskStarted(id, repo string, n, total int, log string) {
	r.stopSpin()
	line := fmt.Sprintf("▶ %s · %s · %d/%d · log %s", id, repo, n, total, log)
	if r.Pretty {
		line = r.st().bold(r.st().accent(line))
	}
	fmt.Fprintf(r.Out, "%s\n", line)
}

// StoryResume says that the run of a failed or stopped task is continued instead of a new one
// being started.
func (r *ConsoleReporter) StoryResume(id, runID string) {
	r.stopSpin()
	fmt.Fprintf(r.Out, "%s: resuming run %s\n", id, runID)
}

// StoryRetry says that --retry left the run of a task behind and which branch the next attempt
// will be on.
func (r *ConsoleReporter) StoryRetry(id, runID string, attempt int, branch string) {
	r.stopSpin()
	run := "its run"
	if runID != "" {
		run = "run " + runID
	}
	fmt.Fprintf(r.Out, "%s: %s is abandoned (its branch stays); attempt %d goes on %s\n", id, run, attempt, branch)
}

// StoryTaskResult says how a task ended: its status and, after a colon, the detail - the
// commit, the exit code of a child that left no run, or the reason a run did not finish.
func (r *ConsoleReporter) StoryTaskResult(id, status, detail string) {
	r.stopSpin()
	text := id + ": " + status
	if detail != "" {
		text += " (" + detail + ")"
	}
	if r.Pretty {
		color := r.st().yellow
		switch status {
		case "done":
			color = r.st().green
		case "failed":
			color = r.st().red
		}
		fmt.Fprintf(r.Out, "\n%s\n", color(text))
		return
	}
	fmt.Fprintf(r.Out, "\n%s\n", text)
}

// StoryHint prints what the operator does next after a task that did not finish, one line each.
func (r *ConsoleReporter) StoryHint(lines ...string) {
	for _, l := range lines {
		if r.Pretty {
			r.detail(l)
			continue
		}
		fmt.Fprintf(r.Out, "  %s\n", l)
	}
}

// StoryConflict says that the parents of a task do not merge cleanly on the spec branch, so the
// task is not dispatched: the conflicting files, then one line each of hints - the manual merge
// and the command that goes on.
func (r *ConsoleReporter) StoryConflict(id string, parents, files []string, specBranch string, hints ...string) {
	r.stopSpin()
	text := fmt.Sprintf("%s: conflict (%s do not merge cleanly on %s)", id, strings.Join(parents, " + "), specBranch)
	if r.Pretty {
		fmt.Fprintf(r.Out, "\n%s\n", r.st().yellow(text))
		if len(files) > 0 {
			r.detail("conflicting files: " + strings.Join(files, ", "))
		}
	} else {
		fmt.Fprintf(r.Out, "\n%s\n", text)
		if len(files) > 0 {
			fmt.Fprintf(r.Out, "  Conflicting files:\n")
			for _, f := range files {
				fmt.Fprintf(r.Out, "    %s\n", f)
			}
		}
	}
	r.StoryHint(hints...)
}

// StoryEnd prints the table that ends a completed story (§10.4): per task the repository, the
// branch, the branch its merge request targets, the commit, the Jira key and the conflicts.
func (r *ConsoleReporter) StoryEnd(rows []StoryEndRow) {
	r.stopSpin()
	head := "all " + storyCount(len(rows), "task", "tasks") + " done"
	if r.Pretty {
		fmt.Fprintln(r.Out)
		r.bullet(r.st().green, "Story completed", head)
	} else {
		fmt.Fprintf(r.Out, "\n✓ Story completed: %s\n", head)
	}
	for _, row := range rows {
		conflicts := row.Conflicts
		if conflicts == "" {
			conflicts = "none"
		}
		line := fmt.Sprintf("%s · %s · %s → %s · %s · %s · conflicts: %s",
			row.ID, row.Repo, row.Branch, row.Target, orDash(shortCommit(row.Commit)), orDash(row.Jira), conflicts)
		if r.Pretty {
			r.detail(line)
			continue
		}
		fmt.Fprintf(r.Out, "  %s\n", line)
	}
	next := "Push the branches and open the merge requests, each to its target."
	if r.Pretty {
		r.detail(next)
		return
	}
	fmt.Fprintf(r.Out, "%s\n", next)
}

// StoryStatusTable prints the README table story status shows, as it is.
func (r *ConsoleReporter) StoryStatusTable(readme string) {
	fmt.Fprint(r.Out, readme)
}

// tildePath shows a path under the user's home directory as "~/...".
func tildePath(p string) string {
	home, err := userHomeDir()
	if err != nil || home == "" || home == "/" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, strings.TrimSuffix(home, "/")+"/"); ok {
		return "~/" + rest
	}
	return p
}

// shellQuote is s as one POSIX shell word in single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellWord is shellWord for a command another package prints for the operator to copy.
func ShellWord(s string) string { return shellWord(s) }

// shellWord is s as it is when the shell would read it as one word unchanged, and quoted
// otherwise.
func shellWord(s string) string {
	if s == "" {
		return "''"
	}
	for _, c := range s {
		safe := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("_./@%+=:,-", c)
		if !safe {
			return shellQuote(s)
		}
	}
	return s
}
