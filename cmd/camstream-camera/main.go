package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/goliatone/go-cam-stream/internal/cameracli"
)

func main() { os.Exit(run()) }
func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return cameracli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}
