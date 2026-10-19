package integration

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"

	"orchestrator/internal/app"
	"orchestrator/internal/exitcode"
)

// storyChildEnv, set to "1" by the story run scenarios, makes this test binary answer as the
// orchestrator itself: story run starts its children as `<own executable> run --story-task ...`,
// and in these tests the own executable is the test binary.
const storyChildEnv = "ORCHE_TEST_ORCHESTRATOR"

// TestMain lets this test binary stand in for the orchestrator when the fake claude starts the
// verify server. The implement step names its own executable in the MCP config
// (`<exe> mcp-verify --dir <dir>`), and in these tests that executable is the test binary, so
// it answers the hidden subcommand here, before the testing package parses flags. It answers the
// children of a story run the same way, under storyChildEnv, with SIGINT and SIGTERM cancelling
// the command as main.go does.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "mcp-verify" {
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
