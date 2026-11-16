//go:build darwin && camstream_dev

package app

import (
	"context"
	"path/filepath"
	"strconv"

	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-cam-stream/config"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/kasa"
	rtspencoder "github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/rtsp"
	rtspstrategy "github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
	"github.com/goliatone/go-secrets/providers/environment"
)

func newDevelopmentController(cfg config.Config, logger admin.Logger, opts DevelopmentOptions) (*Controller, error) {
	if err := validateDevelopmentSource(opts); err != nil {
		return nil, err
	}
	if opts.Go2RTC != "" {
		cfg.Backend.Executable = opts.Go2RTC
	}
	cfg.Backend.MediaAddress, cfg.Backend.CandidateAddress = "127.0.0.1", "127.0.0.1"
	workers := filepath.Join(cfg.Backend.RuntimeDir, "workers")
	verify := func(ctx context.Context) error { return go2rtc.DevelopmentBoundary(ctx, workers) }
	bc := cfg.BackendConfig(verify)
	bc.RuntimeDir = workers
	// The application has already checked/retired the old generation while owning
	// its installation lock. The backend checks again before taking its own flock.
	bc.AllowExec = opts.FFmpeg != ""
	resolver := environment.New(environment.Config{})
	var encoder cs.SourceEncoder[go2rtc.Input] = rtspencoder.NewEncoder(resolver)
	if opts.FFmpeg != "" {
		encoder = developmentSourceEncoder{base: encoder, ffmpeg: opts.FFmpeg, webcam: opts.WebcamDevice}
	}
	backend, err := go2rtc.New(bc, kasa.NewEncoder(resolver), encoder)
	if err != nil {
		return nil, safe(err)
	}
	return New(cfg, Dependencies{Backend: backend, Runtime: backend, Providers: config.DefaultProviders(), VerifyClean: verify, Logger: logger})
}

type developmentSourceEncoder struct {
	base   cs.SourceEncoder[go2rtc.Input]
	ffmpeg string
	webcam string
}

func validateDevelopmentSource(opts DevelopmentOptions) error {
	if opts.FFmpeg != "" && !filepath.IsAbs(opts.FFmpeg) {
		return cs.NewError(cs.CodeInvalidArgument, nil)
	}
	if opts.WebcamDevice != "" {
		_, err := strconv.ParseUint(opts.WebcamDevice, 10, 16)
		if err != nil || opts.FFmpeg == "" {
			return cs.NewError(cs.CodeInvalidArgument, nil)
		}
		for _, c := range opts.WebcamDevice {
			if c < '0' || c > '9' {
				return cs.NewError(cs.CodeInvalidArgument, nil)
			}
		}
	}
	return nil
}

func (e developmentSourceEncoder) Kind() cs.SourceKind { return rtspstrategy.SourceKind }
func (e developmentSourceEncoder) Encode(ctx context.Context, source cs.Source) (go2rtc.Input, error) {
	input, err := e.base.Encode(ctx, source)
	if err != nil {
		return input, err
	}
	s, ok := source.(rtspstrategy.Source)
	if !ok {
		return input, nil
	}
	c := s.Config()
	if c.Host != "127.0.0.1" || c.Port != 8554 || c.UsernameRef.URI() != "" || c.PasswordRef.URI() != "" || len(c.Profiles) != 1 || c.Profiles[0].Path != "/cam" || c.Profiles[0].Query != "" {
		return input, nil
	}
	// Fixed inputs only; YAML cannot supply capture devices or arbitrary commands.
	inputArgs := " -re -f lavfi -i testsrc2=size=1280x720:rate=30"
	if e.webcam != "" {
		inputArgs = " -f avfoundation -framerate 30 -video_size 1280x720 -i " + strconv.Quote(e.webcam+":none")
	}
	return go2rtc.Input{URL: "exec:" + strconv.Quote(e.ffmpeg) + " -hide_banner -loglevel error -nostdin" + inputArgs + " -an -c:v libx264 -pix_fmt yuv420p -preset ultrafast -tune zerolatency -profile:v baseline -g 30 -bf 0 -rtsp_transport tcp -f rtsp {output}"}, nil
}
