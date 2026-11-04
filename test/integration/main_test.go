package integration

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/kasorse/orch/internal/app"
	"github.com/kasorse/orch/internal/exitcode"
)

// storyChildEnv, set to "1" by the story run scenarios, makes this test binary answer as the
// orchestrator itself: story run starts its children as `<own executable> run --story-task ...`,
// and in these tests the own executable is the test binary.
const storyChildEnv = "ORCHE_TEST_ORCHESTRATOR"

// TestMain lets this test binary stand in for the orchestrator in the two execs that remain of the
// listener (specs/embed/SPEC.md O3): the verify server the fake claude starts (the implement step
// names its own executable in the MCP config, `<exe> mcp-verify --dir <dir>`) and git's credential
// helper (`<exe> git-credential --config <path>`, the listener's entries in git's environment). In
// these tests the executable is the test binary, so it answers the hidden subcommands here, before
// the testing package parses flags. It answers the children of a story run the same way, under
// storyChildEnv, with SIGINT and SIGTERM cancelling the command as main.go does. A ticket of the
// Jira listener is not a child: it runs in the test's process.
func TestMain(m *testing.M) {
	if isExecOfTheListener(os.Args) {
		err := app.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitcode.FromError(err))
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Getenv(storyChildEnv) == "1" {
		os.Exit(runAsOrchestrator())
	}
	os.Exit(runWithoutHostOrgConfig(m))
}

// isExecOfTheListener reports whether args, a command line of this binary, is one of the two execs
// the listener still makes: `mcp-verify --dir <dir>`, or git's call of the credential helper the
// listener names in its environment, `--config <path> git-credential get` (withListenerGit).
func isExecOfTheListener(args []string) bool {
	switch {
	case len(args) > 1 && args[1] == "mcp-verify":
		return true
	case len(args) > 3 && args[1] == "--config" && args[3] == "git-credential":
		return true
	}
	return false
}

// runAsOrchestrator is a child of a story run: the test binary run as `orchestrator <args>`.
func runAsOrchestrator() int {
	// The machine-wide org file of the host must not change a child's outcome.
	app.SetOrgConfigEtcPath(filepath.Join(os.TempDir(), "orche-no-such-org.yaml"))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitcode.FromError(err)
	}
	return 0
}

// runWithoutHostOrgConfig runs the tests with the machine-wide org file pointed at a path that
// does not exist, so a /etc/orchestrator/org.yaml on the host cannot change any outcome. The
// org scenarios that need an /etc file name their own (useEtcOrgConfig).
func runWithoutHostOrgConfig(m *testing.M) int {
	dir, err := os.MkdirTemp("", "orche-no-etc-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	absentEtcOrgConfig = filepath.Join(dir, "org.yaml")
	app.SetOrgConfigEtcPath(absentEtcOrgConfig)
	return m.Run()
}
