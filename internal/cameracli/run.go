// Package cameracli adapts flags and terminal output to the camera service.
package cameracli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"runtime"

	"github.com/goliatone/go-cam-stream/internal/maccamera"
)

const usage = `camstream-camera: macOS webcam to authenticated RTSP

  devices                  List webcams (never screens or microphones)
  configure [options]      Create/update private settings and camstream exports
  start                    Capture in foreground; Ctrl-C stops both tools
  status                   Report starting/streaming/stopped
  stop                     Stop the running instance and confirm cleanup

All commands accept --dir PATH (default: ~/Library/Application Support/camstream-camera).
configure: --device auto|NAME|INDEX --listen 127.0.0.1|0.0.0.0 --host HOST
           --port 8554 --path macbook --width 1280 --height 720 --fps 30
Use --listen 0.0.0.0 --host MAC_LAN_IP_OR_DNS for access from the Linux server.
Keep the exported tools/ folder beside this binary. No recording or microphone capture.
`

func Run(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		if _, err := fmt.Fprint(out, usage); err != nil {
			return 1
		}
		return 0
	}
	if runtime.GOOS != "darwin" {
		_, _ = fmt.Fprintln(stderr, "camera capture requires macOS") //nolint:errcheck // Exit code still reports failure if stderr is closed.
		return 1
	}
	if err := run(ctx, args, in, out, stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintln(stderr, err) //nolint:errcheck // Exit code still reports failure if stderr is closed.
		return 1
	}
	return 0
}

func run(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer) error {
	dir, err := maccamera.DefaultDir()
	if err != nil {
		return err
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(stderr)
	f.StringVar(&dir, "dir", dir, "Private camera state directory")
	c := maccamera.DefaultConfig()
	if args[0] == "configure" {
		configFlags(f, &c)
	}
	if err = f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	s := &maccamera.Service{Dir: dir}
	if args[0] != "stop" && args[0] != "status" {
		s.Tools, err = maccamera.ResolveTools()
		if err != nil {
			return err
		}
	}
	return dispatch(ctx, args[0], s, c, in, out, stderr)
}

func configFlags(f *flag.FlagSet, c *maccamera.Config) {
	f.StringVar(&c.Device, "device", c.Device, "Webcam name/index; auto selects the built-in camera")
	f.StringVar(&c.Listen, "listen", c.Listen, "127.0.0.1 (local) or 0.0.0.0 (network)")
	f.StringVar(&c.Host, "host", c.Host, "Mac address reachable by camstream")
	f.IntVar(&c.Port, "port", c.Port, "RTSP TCP port")
	f.StringVar(&c.Path, "path", c.Path, "Stream path and camera ID")
	f.IntVar(&c.Width, "width", c.Width, "Capture width")
	f.IntVar(&c.Height, "height", c.Height, "Capture height")
	f.IntVar(&c.FPS, "fps", c.FPS, "Capture frames per second")
}

func dispatch(ctx context.Context, action string, s *maccamera.Service, c maccamera.Config, in io.Reader, out, stderr io.Writer) error {
	switch action {
	case "devices":
		devices, err := s.Devices(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(devices)
	case "configure":
		if err := (maccamera.ConfigureHandler{Service: s}).Execute(ctx, maccamera.Configure{Config: c}); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "Configured %s\nSettings: %s\nMerge camstream-camera.yaml and privately transfer camstream-camera.env to the Linux server.\n", c.URL(), s.Dir)
		return err
	case "start":
		return (maccamera.StartHandler{Service: s, Out: out, Err: stderr}).Execute(ctx, maccamera.Start{})
	case "stop":
		if err := (maccamera.StopHandler{Service: s}).Execute(ctx, maccamera.Stop{}); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Camera stopped.")
		return err
	case "status":
		status, err := s.Status(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(status)
	case "__supervise":
		return s.Supervise(ctx, in, out)
	case "app-session":
		pipe, ok := in.(io.ReadCloser)
		if !ok {
			return errors.New("app-session requires a lifetime pipe")
		}
		return s.AppSession(ctx, pipe, out)
	default:
		return errors.New("unknown command; run camstream-camera help")
	}
}
