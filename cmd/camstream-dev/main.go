//go:build camstream_dev

// camstream-dev is a local-only development host, excluded from normal builds.
package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/internal/app"
	"github.com/goliatone/go-cam-stream/internal/transport/cli"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	"github.com/goliatone/go-logger/glog"
)

func main() {
	if handled, code := go2rtc.RunDevelopmentWorker(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(run())
}
func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logger := glog.NewLogger(glog.WithWriter(os.Stderr)).WithLevel("info")
	return cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, func(ctx context.Context, path string) error {
		cfg, err := config.Load(ctx, path)
		if err != nil {
			return err
		}
		if err = overridePort(&cfg, os.Getenv("CAMSTREAM_DEV_PORT")); err != nil {
			return err
		}
		controller, err := app.NewDevelopment(cfg, logger, app.DevelopmentOptions{
			Go2RTC: os.Getenv("CAMSTREAM_DEV_GO2RTC"), FFmpeg: os.Getenv("CAMSTREAM_DEV_SOURCE_FFMPEG"),
			WebcamDevice: os.Getenv("CAMSTREAM_DEV_WEBCAM_DEVICE"),
			MediaAddress: os.Getenv("CAMSTREAM_DEV_MEDIA_ADDRESS"),
		})
		if err != nil {
			return err
		}
		return controller.Run(ctx)
	}, app.PrepareManagedRollback)
}

// The task passes its port override only to this development executable. Keep
// the configured loopback host and validate the resulting config normally.
func overridePort(cfg *config.Config, port string) error {
	if port == "" {
		return nil
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return cs.NewError(cs.CodeInvalidArgument, nil)
		}
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	host, _, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	cfg.Listen = net.JoinHostPort(host, strconv.FormatUint(n, 10))
	return nil
}
