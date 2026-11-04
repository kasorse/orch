// Package orchestrator is the Jira listener of github.com/kasorse/orch as a library
// (specs/embed/SPEC.md §4): a Go service starts it as one of its runners instead of running the
// orchestrator binary. Two functions are exported, [Listen] and [Embedded], and the type
// [Options] of the first.
//
// The host's main answers the two commands the listener still starts as executables, through
// os.Executable(), before anything else:
//
//	func main() {
//		if code, ok := orchestrator.Embedded(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); ok {
//			os.Exit(code)
//		}
//		// ... the service: orchestrator.Listen(ctx, orchestrator.Options{Config: "listener.yaml"})
//	}
//
// # What the host must know
//
// The process is the listener's. [Listen] changes the process environment (the git credential
// helper's entries are set; the Jira variables jira.email_env and jira.token_env and the
// parent-session variables are removed once the Jira client is built, so that no process a run
// starts inherits them) and holds a flock in Options.Dir. Call it once per process; run one
// process per Dir, because the flock refuses a second one.
//
// Options.Dir must be a volume. The state, the lock, the task files, the clones of the services
// and their worktrees (.orchestrator/listener/ and listener.services_dir) must survive a restart:
// that is how a ticket interrupted by a stop is resumed.
//
// Everything the listener needs must be on the host: git on PATH; bwrap, unless every service's
// configuration says test.sandbox: none; the toolchains of the services' test commands; no
// provider CLI when the providers are on transport: api; and the environment variables the
// configuration names (jira.*_env, gitlab.token_env, providers.*.api_key_env).
//
// Replicas: one. A second listener on the same volume is refused, and two on different volumes
// claim the same tickets.
//
// A host whose own command line uses one of the orchestrator's command words (run, resume,
// review, validate, version, story, decisions, runs, jira, mcp-verify, git-credential, help) as
// its first argument cannot embed the listener: [Embedded] would answer it.
package orchestrator

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/kasorse/orch/internal/app"
	"github.com/kasorse/orch/internal/exitcode"
)

// Options are the settings of [Listen]: the flags of `orchestrator jira listen`, and the
// directory it works in.
type Options struct {
	// Dir is the listener's working directory: its state, its lock, its task files and a relative
	// listener.services_dir. Default: the process's working directory. It must be a volume.
	Dir string
	// Config is the listener's configuration file; required. A relative path is resolved against
	// the process's working directory, as the command line does.
	Config string
	// Once makes Listen return after one poll and the ticket it dispatches.
	Once bool
	// ClaudeAuth is --claude-auth: "", "auto", "api" or "login".
	ClaudeAuth string
	// CodexAuth is --codex-auth, likewise.
	CodexAuth string
	// Stderr is the log, one line per event. Default: os.Stderr.
	Stderr io.Writer
}

// Listen is `orchestrator jira listen` as a call: the fail-fast chain, the flock, GET /myself,
// the state and the loop are the command's. It returns when ctx ends (nil, nothing
// half-written) or with the error the command would map to exit 2 (a configuration, a credential
// or an authorization stop) or 1; the host decides what to do with it.
//
// It changes the process environment and holds a flock in Options.Dir: one Listen per process,
// one process per Dir (see the package documentation).
func Listen(ctx context.Context, o Options) error {
	if o.Config == "" {
		return app.NewConfigError("orchestrator.Listen: Options.Config is required")
	}
	stderr := o.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	args := []string{"--config", o.Config}
	if o.ClaudeAuth != "" {
		args = append(args, "--claude-auth", o.ClaudeAuth)
	}
	if o.CodexAuth != "" {
		args = append(args, "--codex-auth", o.CodexAuth)
	}
	args = append(args, "jira", "listen")
	if o.Once {
		args = append(args, "--once")
	}
	if o.Dir != "" {
		ctx = app.WithDir(ctx, o.Dir)
	}
	return app.Run(ctx, args, nil, stderr, stderr)
}

// commands are the first words [Embedded] answers.
var commands = map[string]bool{
	"run": true, "resume": true, "review": true, "validate": true, "version": true, "story": true,
	"decisions": true, "runs": true, "jira": true, "mcp-verify": true, "git-credential": true, "help": true,
}

// Embedded runs the orchestrator command args names, for a host binary that is the executable the
// listener starts for git's credential helper (`<exe> --config <path> git-credential get`) and for
// the in-session verify server (`<exe> mcp-verify --dir <path>`). When the first word of args,
// after any leading --config, is one of the orchestrator's commands it runs the command as the
// CLI does, with the CLI's signal handling, and returns its exit code and true; otherwise it
// returns false without touching anything and without reading args any further.
func Embedded(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int, handled bool) {
	if !commands[firstWord(args)] {
		return 0, false
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if stdin == nil {
		stdin = eof{}
	}
	err := app.Run(ctx, args, stdin, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
	}
	return exitcode.FromError(err), true
}

// firstWord is the first argument that is not a leading --config and its value ("" if none).
func firstWord(args []string) string {
	for len(args) > 0 {
		switch {
		case args[0] == "--config" && len(args) > 1:
			args = args[2:]
		case len(args[0]) > len("--config=") && args[0][:len("--config=")] == "--config=":
			args = args[1:]
		default:
			return args[0]
		}
	}
	return ""
}

// eof is the stdin of a host that gave none.
type eof struct{}

func (eof) Read([]byte) (int, error) { return 0, io.EOF }
