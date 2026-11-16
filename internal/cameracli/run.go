// Package cameracli adapts flags and terminal output to the camera service.
package cameracli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/goliatone/go-cam-stream/internal/maccamera"
)

const usage = `camstream-camera: macOS webcam to authenticated RTSP

  devices                  List webcams (never screens or microphones)
  audio-devices            List microphone IDs and labels without capture
  configure [options]      Create/update private settings and camstream exports
  start                    Capture in foreground; Ctrl-C stops both tools
  export-legacy            Write separate video-only settings for downgrade
  status                   Report starting/streaming/stopped
  stop                     Stop the running instance and confirm cleanup

All commands accept --dir PATH (default: ~/Library/Application Support/camstream-camera).
configure: --device auto|NAME|INDEX --listen 127.0.0.1|0.0.0.0 --host HOST
           --port 8554 --path macbook --width 1280 --height 720 --fps 30
           --audio-enabled=false --audio-device-id UID
Use --listen 0.0.0.0 --host MAC_LAN_IP_OR_DNS for access from the Linux server.
Keep the exported tools/ folder beside this binary. Audio is off by default; audio-on requires runtime-3. No recording.
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
	var startDeadline int64
	var reportError bool
	if args[0] == "__supervise" {
		f.Int64Var(&startDeadline, "start-deadline", 0, "Private startup deadline")
		f.BoolVar(&reportError, "report-error", false, "Private error pipe")
	}
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
	if args[0] != "stop" && args[0] != "status" && args[0] != "export-legacy" {
		s.Tools, err = maccamera.ResolveTools()
		if err != nil {
			return err
		}
	}
	if startDeadline != 0 {
		s.StartupDeadline = time.Unix(0, startDeadline)
	}
	err = dispatch(ctx, args[0], s, c, in, out, stderr)
	if reportError && err != nil {
		report := os.NewFile(3, "capture-result")
		if report != nil {
			_ = json.NewEncoder(report).Encode(maccamera.CaptureFailure(err))
			_ = report.Close()
		}
	}
	return err
}

func configFlags(f *flag.FlagSet, c *maccamera.Config) {
	f.BoolVar(&c.AudioEnabled, "audio-enabled", false, "Enable microphone audio on explicit Start")
	f.StringVar(&c.AudioDeviceID, "audio-device-id", "", "Exact microphone ID from audio-devices")
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
	case "audio-devices", "devices":
		return discover(ctx, action, s, out)
	case "export-legacy":
		path, err := (maccamera.ExportLegacyHandler{Service: s}).Run(ctx, maccamera.ExportLegacy{})
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]string{"configuration": path})
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

func discover(ctx context.Context, action string, s *maccamera.Service, out io.Writer) error {
	switch action {
	case "audio-devices":
		devices, err := s.AudioDevices(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(devices)
	case "devices":
		devices, err := s.Devices(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(devices)
	default:
		return errors.New("unknown discovery method")
	}
}
