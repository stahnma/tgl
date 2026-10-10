// Command tgl is a command-line interface for Tangled issues, pull requests and
// repositories.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"tangled.org/tgl/internal/cli"
	"tangled.org/tgl/internal/httpx"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cli.Version = version
	httpx.UserAgent = "tgl/" + version

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := cli.NewApp()
	err := cli.Dispatch(ctx, app, cli.Root(), os.Args[1:])
	if err == nil {
		os.Exit(cli.CodeOK)
	}
	if !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "tgl: %v\n", err)
	}
	os.Exit(cli.CodeOf(err))
}
