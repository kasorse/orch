package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/internal/app"
	"orchestrator/internal/exitcode"
)

// TestMain lets this test binary stand in for the orchestrator when the fake claude starts the
// verify server. The implement step names its own executable in the MCP config
// (`<exe> mcp-verify --dir <dir>`), and in these tests that executable is the test binary, so
// it answers the hidden subcommand here, before the testing package parses flags.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "mcp-verify" {
		err := app.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitcode.FromError(err))
		}
		os.Exit(0)
	}
	os.Exit(runWithoutHostOrgConfig(m))
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
