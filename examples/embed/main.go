// Command embed is the Jira listener as a service of your own (specs/embed/SPEC.md §5, MVP.md §53,
// README.md next to this file). It uses the standard library only; on the platform the same two
// calls are one runner of the platform's application:
//
//	app := bootstrap.New(...)
//	app.AddRunnerFunc(func() error { return orchestrator.Listen(app.Context(), opts) })
//
// The corporate service replaces main below with that, keeping the Embedded call first.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	orchestrator "github.com/kasorse/orch"
)

func main() {
	// First, before anything else: git starts `<this binary> --config <path> git-credential get`
	// and the MCP client `<this binary> mcp-verify --dir <path>`; Embedded answers both and
	// returns false for every other first argument.
	if code, ok := orchestrator.Embedded(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}
	os.Exit(run())
}

func run() int {
	// SIGTERM (the platform's stop) and SIGINT end Listen with nil; a step in flight is cut off
	// and the ticket is resumed on the next start.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := orchestrator.Options{
		Dir:    getenv("LISTENER_DIR", "/data"), // the persistent volume
		Config: getenv("LISTENER_CONFIG", "/etc/orchestrator/listener.yaml"),
	}
	if err := orchestrator.Listen(ctx, opts); err != nil {
		fmt.Fprintln(os.Stderr, "listener:", err)
		return 1
	}
	return 0
}

func getenv(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
