//go:build camstream_dev

// camstream-dev is a local-only development host, excluded from normal builds.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/internal/app"
	"github.com/goliatone/go-cam-stream/internal/transport/cli"
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
	logger := glog.NewLogger(glog.WithWriter(os.Stderr)).WithLevel("warn")
	return cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, func(ctx context.Context, path string) error {
		cfg, err := config.Load(ctx, path)
		if err != nil {
			return err
		}
		controller, err := app.NewDevelopment(cfg, logger, app.DevelopmentOptions{
			Go2RTC: os.Getenv("CAMSTREAM_DEV_GO2RTC"), FFmpeg: os.Getenv("CAMSTREAM_DEV_SOURCE_FFMPEG"),
			WebcamDevice: os.Getenv("CAMSTREAM_DEV_WEBCAM_DEVICE"),
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Development server: http://%s (local access without login)\n", cfg.Listen)
		return controller.Run(ctx)
	})
}
