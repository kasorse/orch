package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"orchestrator/internal/app"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/logging"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	err := app.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	// On a terminal, a cancellation the operator chose was already shown where they chose
	// it ("✗ cancelled"); repeating it as a red error adds nothing. The exit code still
	// says so, and plain output keeps the message.
	if err != nil && !(errors.Is(err, context.Canceled) && logging.PrettyFor(os.Stderr) && ctx.Err() == nil) {
		fmt.Fprintln(os.Stderr, logging.FormatError(os.Stderr, err))
	}
	os.Exit(exitcode.FromError(err))
}
