package app

// helpText is what `orchestrator help`, --help and -h print: the commands, the run modes and
// the flags an operator types. The hidden flags and commands the orchestrator starts itself
// (--story-task, --story-no-push, mcp-verify, git-credential) are left out.
const helpText = `orchestrator - takes one engineering task, decides which stages it needs and runs them
with the AI CLIs (claude, codex, gemini) against the git repository in the current directory.

Usage:
  orchestrator [global flags] <command> [command flags] [arguments]

Commands:
  run "<task>"                 decide the workflow for the task and run it
  run --jira <KEY|URL> ["notes"]
                               take the task from a Jira ticket
  resume <run-id>              continue a failed, stopped or planned run where it stopped
                               (the full ID or a unique prefix of 8+ characters)
  review --base <ref>          review a change someone else wrote (test and review only)
  revise <KEY|MR-URL|run-id|branch> "<requirement>"
                               add a requirement to work that opened a merge request: push a
                               commit to its branch, or open a new one when it was merged
  story plan "<task>"          split a task that spans services into a spec of ordered tasks
  story run <spec-dir>         run the tasks of a story spec, each its own run
  story status <spec-dir>      show the state of a story's tasks
  jira listen                  poll Jira for tickets labelled ai-run and run them one at a time
  validate                     check the config, the provider CLIs and the credentials; runs nothing
  runs report                  what the runs cost and how they ended
  runs clean                   remove old run worktrees, or the run directories this version cannot read
  decisions report             compare the two complexity questions over past decisions
  decisions calibrate          propose threshold cuts from past runs (writes nothing)
  version                      print the version
  help                         print this help (also --help, -h)

Run modes (--mode, for run, resume, review and story run):
  plan                         decide and show the workflow, run nothing (resume runs it later)
  manual                       confirm every step before it runs
  accept-edits                 approve the workflow once, then run every step without asking
  auto                         ask nothing: no prompts at all; also commits, pushes and opens the
                               merge request without asking (needed without a terminal)
  (none)                       show the decided workflow for approval, then ask for a mode

Global flags:
  --config <path>              the config file (default orchestrator.yaml)
  --mode <mode>                the run mode, see above
  --claude-auth auto|api|login the claude credential: api = the API key from the environment,
                               login = the CLI's own login, auto = api when the key is set, else
                               login (default: providers.claude.auth, else auto)
  --codex-auth auto|api|login  the same for codex
  --output json                run, resume, review: one JSON result line on stdout, the rest on stderr
  --verbose, -v                the stage table, each step's authority and the providers' live output
  --help, -h                   print this help

run:
  --stage <name>               run exactly these stages, no decision call (repeatable):
                               research, investigation, development, testing, review
  --jira <KEY|URL>             the task is the Jira ticket; text after it is your notes
  --inplace                    run in the current working tree instead of a git worktree
  --worktree                   run in a git worktree even if execution.isolation is inplace
  --allow-red-tests            start even though the tests fail before any change

review:
  --base <ref>                 the commit the change starts from (required)
  --head <ref>                 the end of the change (default HEAD)
  --task "<text>"              what the change is meant to do
  --jira <KEY|URL>             take what it is meant to do from a Jira ticket

story plan:
  --jira <KEY|URL>             plan the story from a Jira issue
  --repo <name>                a service of the story, instead of those the task names (repeatable)

story run:
  --retry <ID>                 abandon that task's run and start it again on its next attempt
  --parallel <N>               run up to N ready tasks of different services at once (1..8)

jira listen:
  --once                       poll once, run what was found, and exit
  --claude-auth auto|api|login checked at start and passed to every run it starts
  --codex-auth auto|api|login  the same for codex

runs report:
  --dir <path>                 read every run.json under this directory (default .orchestrator/runs)

runs clean:
  --older-than <N>d            remove the worktrees of runs older than N days (default 7d)
  --unreadable                 remove the run directories this version cannot read
  --dry-run                    list what would be removed, change nothing

decisions calibrate:
  --runs <dir>                 the runs to read (default .orchestrator/runs)
  --min-runs <N>               the fewest runs a bucket needs before anything is said (default 10)
  --size-target S/M/C          the target share of simple/medium/complex (default 50/35/15)
  --simple-max-hard <F>        the share of hard runs the simple level may hold (default 0.2)

Exit codes: 0 done, 1 unexpected error, 2 config or usage, 3 provider failed, 4 workflow failed
or a limit stopped it, 5 cancelled, 6 completed but needs a person. More: README.md, MVP.md.
`
