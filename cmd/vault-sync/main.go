// Command vault-sync restores project files from a remote secrets provider.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/jordigarciaventura/vault-sync/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
