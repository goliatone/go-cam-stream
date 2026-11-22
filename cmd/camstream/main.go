package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/internal/app"
	"github.com/goliatone/go-cam-stream/internal/transport/cli"
	"github.com/goliatone/go-logger/glog"
)

func main() { os.Exit(run()) }
func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logger := glog.NewLogger(glog.WithWriter(os.Stderr)).WithLevel("warn")
	return cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, func(ctx context.Context, path string) error {
		cfg, err := config.Load(ctx, path)
		if err != nil {
			return err
		}
		controller, err := app.NewDefault(cfg, logger)
		if err != nil {
			return err
		}
		return controller.Run(ctx)
	}, app.PrepareManagedRollback)
}
