package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/MeltiplyOrg/meltiply-cli/internal/cli"
)

// main binds process cancellation to every request and keeps exit handling out
// of command handlers so their cleanup runs before the process exits.
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], cli.Options{})
	cancel()
	os.Exit(code)
}
