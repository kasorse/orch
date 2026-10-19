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
