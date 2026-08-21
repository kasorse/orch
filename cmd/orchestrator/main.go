package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"orchestrator/internal/app"
	"orchestrator/internal/exitcode"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	err := app.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(exitcode.FromError(err))
}
