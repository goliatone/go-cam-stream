package maccamera

import (
	"bufio"
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
	defer preview.close()
	listener, err := s.control(ctx, c, &frames, &audio, cancel, dir, preview)
	if err != nil {
		return err
	}
	defer listener.Close() //nolint:errcheck // Owned Unix listener also removes its socket.
	return s.runMedia(ctx, c, secret, dir, configPath, &frames, &audio, deadline, out)
}

func (s *Service) control(ctx context.Context, c Config, frames *atomic.Int64, audio *audioProgress, cancel context.CancelFunc, dir string, preview *previewManager) (net.Listener, error) {
	path := filepath.Join(dir, "control.sock")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			serveControl(conn, c, frames, audio, cancel, preview)
		}
	}()
	return listener, nil
}

func serveControl(conn net.Conn, c Config, frames *atomic.Int64, audio *audioProgress, cancel context.CancelFunc, preview *previewManager) {
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
	if action == "preview_attach" && state == "streaming" {
		endpoint, _ = preview.attach() //nolint:errcheck // Missing endpoint is the sanitized unavailable response.
	} else if after, ok := strings.CutPrefix(action, "preview_detach:"); ok {
		preview.detach(after)
	} else if action == "stop" {
		state = "stopping"
		cancel()
	} else if action != "status" {
		return
	}
	if err := json.NewEncoder(conn).Encode(Status{State: state, URL: c.URL(), Frames: frames.Load(), Preview: endpoint, Audio: audio.status(c, state)}); err != nil {
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
	return s.monitor(ctx, c, secret, dir, server, producer, frames, audio, deadline, out)
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

func (s *Service) monitor(ctx context.Context, c Config, secret credentials, dir string, server, producer *child, frames *atomic.Int64, audio *audioProgress, deadline time.Time, out io.Writer) error {
	wait := 45 * time.Second
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
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-server.done:
			return errors.New("RTSP server stopped; camera capture has been stopped")
		case err := <-watch.observed:
			return err
		case <-producer.done:
			return producerFailure(c)
		case <-startup.C:
			if !ready && !(mediaReady(c, frames.Load(), audio)) {
				return startupFailure(c)
			}
		case <-ticker.C:
			if err := watch.tick(frames.Load()); err != nil {
				return err
			}
			if !ready && mediaReady(c, frames.Load(), audio) {
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
	return errors.New("no video frames within 45 seconds; grant camera access and retry")
}
