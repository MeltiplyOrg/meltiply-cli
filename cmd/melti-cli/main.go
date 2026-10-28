// Command melti-cli is the terminal client for a Meltiply installation.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/MeltiplyOrg/meltiply-cli/internal/cli"
)

// Build metadata is injected with -ldflags by the release build. Defaults keep
// `go run` and source builds identifiable without pretending to be a release.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// main binds process cancellation to every request and keeps exit handling out
// of command handlers so their cleanup runs before the process exits.
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], cli.Options{
		Version: cli.Version{Version: version, Commit: commit, Date: date},
	})
	cancel()
	os.Exit(code)
}
