//go:build darwin && camstream_dev

package app

import (
	"context"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/internal/viewer"
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
	var mediaAddress string
	if opts.MediaAddress == "auto" {
		mediaAddress = automaticDevelopmentMediaAddress(net.Interfaces, (*net.Interface).Addrs)
		if logger != nil {
			if mediaAddress == "127.0.0.1" {
				logger.Info("No private IPv4 on an active Mac network interface. WebRTC media uses loopback; Firefox may require --media-address.")
			} else {
				logger.Info("WebRTC media will bind " + mediaAddress + ". HTTP and worker control remain loopback.")
			}
		}
	} else {
		var err error
		mediaAddress, err = developmentMediaAddress(opts.MediaAddress, net.InterfaceAddrs)
		if err != nil {
			return nil, err
		}
	}
	cfg.Backend.MediaAddress, cfg.Backend.CandidateAddress = mediaAddress, mediaAddress
	workers := filepath.Join(cfg.Backend.RuntimeDir, "workers")
	verify := func(ctx context.Context) error { return go2rtc.DevelopmentBoundary(ctx, workers) }
	bc := cfg.BackendConfig(verify)
	bc.RuntimeDir = workers
	// The application has already checked/retired the old generation while owning
	// its installation lock. The backend checks again before taking its own flock.
	bc.AllowExec = opts.FFmpeg != ""
	resolver := viewer.NewManagedSecrets(environment.New(environment.Config{}))
	var encoder cs.SourceEncoder[go2rtc.Input] = rtspencoder.NewEncoder(resolver)
	if opts.FFmpeg != "" {
		encoder = developmentSourceEncoder{base: encoder, ffmpeg: opts.FFmpeg, webcam: opts.WebcamDevice}
	}
	backend, err := go2rtc.New(bc, kasa.NewEncoder(resolver), encoder)
	if err != nil {
		return nil, safe(err)
	}
	return New(cfg, Dependencies{Backend: backend, Runtime: backend, Providers: config.DefaultProviders(), VerifyClean: verify, Logger: logger, Secrets: resolver})
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

// Choose a private IPv4 from an active macOS Wi-Fi or Ethernet interface.
// Ignore loopback, tunnels and virtual bridges during automatic selection.
func automaticDevelopmentMediaAddress(list func() ([]net.Interface, error), addresses func(*net.Interface) ([]net.Addr, error)) string {
	interfaces, err := list()
	if err != nil {
		return "127.0.0.1"
	}
	const active = net.FlagUp | net.FlagRunning
	for _, iface := range interfaces {
		if !strings.HasPrefix(iface.Name, "en") || iface.Flags&active != active {
			continue
		}
		addrs, err := addresses(&iface)
		if err != nil {
			continue
		}
		for _, address := range addrs {
			prefix, err := netip.ParsePrefix(address.String())
			if err == nil && prefix.Addr().Is4() && prefix.Addr().IsPrivate() {
				return prefix.Addr().String()
			}
		}
	}
	return "127.0.0.1"
}

// Validate an explicit media address. HTTP and worker control stay on loopback.
func developmentMediaAddress(value string, interfaces func() ([]net.Addr, error)) (string, error) {
	if value == "" || value == "127.0.0.1" {
		return "127.0.0.1", nil
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || !ip.Is4() || !ip.IsPrivate() {
		return "", cs.NewError(cs.CodeInvalidArgument, nil)
	}
	addresses, err := interfaces()
	if err != nil {
		return "", cs.NewError(cs.CodeInvalidArgument, err)
	}
	for _, address := range addresses {
		prefix, parseErr := netip.ParsePrefix(address.String())
		if parseErr == nil && prefix.Addr() == ip {
			return ip.String(), nil
		}
	}
	return "", cs.NewError(cs.CodeInvalidArgument, nil)
}
