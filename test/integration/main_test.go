package integration

import (
	"context"
	"fmt"
	"os"
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
	os.Exit(m.Run())
}
