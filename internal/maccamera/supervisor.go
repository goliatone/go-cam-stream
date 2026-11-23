package maccamera

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type child struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

var errCameraFormat = errors.New("selected webcam could not use a supported video format")
var errCameraNoFrames = errors.New("selected webcam stopped providing video frames")

// Keep only enough private stderr to classify an early failure. Wait joins
// the stderr copy before the supervisor inspects it. Raw output stays private.
type captureDiagnostics struct{ data []byte }

func (d *captureDiagnostics) Write(p []byte) (int, error) {
	d.data = append(d.data, p[:min(len(p), 16*1024-len(d.data))]...)
	return len(p), nil
}

func (d *captureDiagnostics) failure() error {
	for _, marker := range []string{"Camstream video mode unavailable", "Selected video size (", "Selected framerate (", "No such filter: 'pad'"} {
		if bytes.Contains(d.data, []byte(marker)) {
			return errCameraFormat
		}
	}
	for _, marker := range []string{"Selected video identity unavailable", "Selected video identity disconnected", "Video device not found"} {
		if bytes.Contains(d.data, []byte(marker)) {
			return errCameraUnavailable
		}
	}
	return nil
}

func startChild(cmd *exec.Cmd) (*child, error) {
	cmd.Env = cleanEnvironment()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &child{cmd: cmd, done: make(chan struct{})}
	go func() { c.err = cmd.Wait(); close(c.done) }()
	return c, nil
}

func (c *child) stop() {
	select {
	case <-c.done:
		return
	default:
	}
	_ = c.cmd.Process.Signal(os.Interrupt) //nolint:errcheck // Exit races are handled by Wait below.
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		_ = c.cmd.Process.Kill() //nolint:errcheck // Wait confirms process exit.
		<-c.done
	}
}

// Supervise is the private foreground-owner worker. It never detaches from its
// lifetime pipe and it does not accept arbitrary executable paths from clients.
func (s *Service) Supervise(ctx context.Context, lifetime io.Reader, out io.Writer) error {
	started := time.Now()
	deadline := s.StartupDeadline
	if deadline.IsZero() {
		deadline = started.Add(audioStartupTimeout)
	}
	lock, err := s.lock()
	if err != nil {
		return err
	}
	defer lock.Close() //nolint:errcheck // Closing releases the flock after child cleanup.
	previewLock, err := s.namedLock("webcam-preview.lock")
	if err != nil {
		return err
	}
	defer previewLock.Close() //nolint:errcheck // Keep the webcam reserved through child cleanup.
	c, secret, err := load(s.Dir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, lifetime) //nolint:errcheck // EOF or read failure both end ownership.
		cancel()
	}()
	if err = s.audioPreflight(ctx, c, deadline); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	discoveryCtx, stopDiscovery := context.WithDeadline(ctx, deadline)
	devices, err := s.Devices(discoveryCtx)
	stopDiscovery()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		if c.AudioEnabled && errors.Is(err, context.DeadlineExceeded) {
			return audioFailure("audio_startup_timeout")
		}
		return errCameraInventory
	}
	if c.Device, err = selectDevice(c.Device, devices); err != nil {
		return err
	}
	dir, err := s.runtimeDir()
	if err != nil {
		return err
	}
	if err = privateDir(dir); err != nil {
		return err
	}
	config, err := mediaConfig(c, secret)
	if err != nil {
		return err
	}
	configPath := filepath.Join(dir, "mediamtx.json")
	if err = writePrivate(configPath, config); err != nil {
		return err
	}
	defer os.Remove(configPath) //nolint:errcheck // Runtime secrets are also replaced on the next owned start.
	var frames atomic.Int64
	audio := audioProgress{deadline: deadline}
	preview := &previewManager{ctx: ctx, config: c, secret: secret, tool: s.Tools.FFmpeg, dir: dir, frames: &frames}
	listener, err := s.control(ctx, c, &frames, &audio, cancel, dir, preview, func() (Exports, error) {
		if exportErr := s.export(c, secret); exportErr != nil {
			return Exports{}, exportErr
		}
		return s.exportPaths(), nil
	})
	if err != nil {
		return err
	}
	defer func() {
		// A missing status socket must mean every owned reader and secret is gone.
		preview.close()
		_ = os.Remove(configPath) //nolint:errcheck // Also removed on pre-listener failures by the earlier defer.
		_ = listener.Close()      //nolint:errcheck // Close the rendezvous only after owned cleanup.
	}()
	return s.runMedia(ctx, c, secret, dir, configPath, &frames, &audio, deadline, out)
}

// Close waits for any in-flight export before the instance lock is released.
type controlListener struct {
	net.Listener
	done chan struct{}
}

func (l *controlListener) Close() error {
	err := l.Listener.Close()
	<-l.done
	return err
}

func (s *Service) control(ctx context.Context, c Config, frames *atomic.Int64, audio *audioProgress, cancel context.CancelFunc, dir string, preview *previewManager, export func() (Exports, error)) (net.Listener, error) {
	path := filepath.Join(dir, "control.sock")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	owned := &controlListener{Listener: listener, done: make(chan struct{})}
	go func() {
		defer close(owned.done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			serveControl(conn, c, frames, audio, cancel, preview, export)
		}
	}()
	return owned, nil
}

func serveControl(conn net.Conn, c Config, frames *atomic.Int64, audio *audioProgress, cancel context.CancelFunc, preview *previewManager, export func() (Exports, error)) {
	defer conn.Close() //nolint:errcheck // Response is bounded and connection is no longer used.
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return
	}
	var action string
	if err := json.NewDecoder(io.LimitReader(conn, 128)).Decode(&action); err != nil {
		return
	}
	state := "starting"
	if mediaReady(c, frames.Load(), audio) {
		state = "streaming"
	}
	var endpoint *PreviewEndpoint
	var exports *Exports
	switch {
	case action == "export":
		if result, err := export(); err == nil {
			exports = &result
		}
	case action == "preview_attach":
		if state == "streaming" {
			endpoint, _ = preview.attach() //nolint:errcheck // Missing endpoint is the sanitized unavailable response.
		}
	case strings.HasPrefix(action, "preview_detach:"):
		preview.detach(strings.TrimPrefix(action, "preview_detach:"))
	case action == "stop":
		state = "stopping"
		cancel()
	case action != "status":
		return
	}
	if err := json.NewEncoder(conn).Encode(Status{State: state, URL: c.URL(), Frames: frames.Load(), Preview: endpoint, Exports: exports, Audio: audio.status(c, state)}); err != nil {
		return
	}
}

func (s *Service) runMedia(ctx context.Context, c Config, secret credentials, dir, config string, frames *atomic.Int64, audio *audioProgress, deadline time.Time, out io.Writer) error {
	startupCtx := ctx
	stopStartup := func() {}
	if c.AudioEnabled {
		startupCtx, stopStartup = context.WithDeadline(ctx, deadline)
	}
	defer stopStartup()
	server, err := s.startServer(startupCtx, config)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if c.AudioEnabled && startupCtx.Err() != nil {
			return audioFailure("audio_startup_timeout")
		}
		return err
	}
	defer server.stop()

	if ctx.Err() != nil {
		return nil
	}
	if c.AudioEnabled && !time.Now().Before(deadline) {
		return audioFailure("audio_startup_timeout")
	}
	cmd := exec.Command(s.Tools.FFmpeg, ffmpegArgs(c)...) //nolint:noctx // Owned child cleanup uses bounded interrupt/kill and reap.
	diagnostics := &captureDiagnostics{}
	cmd.Stderr = diagnostics
	progress, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	producer, err := startChild(cmd)
	if err != nil {
		return errors.New("could not launch FFmpeg")
	}
	defer producer.stop()
	go readProgress(progress, frames)
	if _, err := fmt.Fprintln(out, "Capture starting; waiting for published media. Ctrl-C or camstream-camera stop ends capture."); err != nil {
		return err
	}
	return s.monitor(ctx, c, secret, dir, server, producer, frames, audio, deadline, out, diagnostics)
}

func waitServer(ctx context.Context, server *child, ready <-chan struct{}) error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-server.done:
		return errors.New("RTSP server exited; check for a port conflict or incompatible runtime")
	case <-timer.C:
		return errors.New("RTSP listener did not start")
	case <-ready:
		return nil
	}
}

func readProgress(r io.Reader, frames *atomic.Int64) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		if value, ok := strings.CutPrefix(s.Text(), "frame="); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				frames.Store(n)
			}
		}
	}
}

func (s *Service) monitor(ctx context.Context, c Config, secret credentials, dir string, server, producer *child, frames *atomic.Int64, audio *audioProgress, deadline time.Time, out io.Writer, diagnostics *captureDiagnostics) error {
	wait := 20 * time.Second
	if c.AudioEnabled {
		wait = time.Until(deadline)
	}
	startup := time.NewTimer(wait)
	watch := &audioWatch{service: s, config: c, secret: secret, dir: dir, progress: audio}
	defer watch.close()
	defer startup.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	ready := false
	var lastFrame int64
	var lastVideo time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-server.done:
			return errors.New("RTSP server stopped; camera capture has been stopped")
		case err := <-watch.observed:
			return err
		case <-producer.done:
			if err := diagnostics.failure(); err != nil {
				return err
			}
			return producerFailure(c)
		case <-startup.C:
			if !ready && !(mediaReady(c, frames.Load(), audio)) {
				if frames.Load() == 0 {
					return errCameraNoFrames
				}
				return startupFailure(c)
			}
		case now := <-ticker.C:
			frame := frames.Load()
			if frame > lastFrame {
				lastFrame, lastVideo = frame, now
			}
			if ready && now.Sub(lastVideo) >= 8*time.Second {
				return errCameraNoFrames
			}
			if err := watch.tick(frame); err != nil {
				return err
			}
			if !ready && mediaReady(c, frame, audio) {
				ready = true
				if _, err := fmt.Fprintln(out, "Streaming configured media at "+c.URL()+" (authenticated RTSP/TCP)"); err != nil {
					return err
				}
			}
		}
	}
}

func (s *Service) startServer(ctx context.Context, config string) (*child, error) {
	versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	b, err := boundedOutput(versionCtx, s.Tools.MediaMTX, 16384, "--version")
	cancel()
	if err != nil || strings.TrimSpace(string(b)) != MediaMTXVersion {
		return nil, errors.New("unsupported MediaMTX runtime; use the pinned exported folder")
	}
	cmd := exec.Command(s.Tools.MediaMTX, config) //nolint:noctx // Caller owns bounded interrupt/kill and reap.
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	server, err := startChild(cmd)
	if err != nil {
		closePreviewIO(output)
		return nil, errors.New("could not launch MediaMTX")
	}
	ready := make(chan struct{}, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "[RTSP] started with listeners") {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		}
	}()
	if err = waitServer(ctx, server, ready); err != nil {
		server.stop()
		return nil, err
	}
	return server, nil
}

func mediaReady(c Config, frames int64, audio *audioProgress) bool {
	return frames > 0 && (!c.AudioEnabled || audio.ready())
}
func producerFailure(c Config) error {
	if c.AudioEnabled {
		return audioFailure("audio_capture_failed")
	}
	return errors.New("camera capture stopped; check macOS camera permission, device availability and resolution/fps")
}
func startupFailure(c Config) error {
	if c.AudioEnabled {
		return audioFailure("audio_startup_timeout")
	}
	return errCameraNoFrames
}
