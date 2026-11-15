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
	listener, err := s.control(ctx, c, &frames, cancel, dir)
	if err != nil {
		return err
	}
	defer listener.Close() //nolint:errcheck // Owned Unix listener also removes its socket.
	return s.runMedia(ctx, c, configPath, &frames, out)
}

func (s *Service) control(ctx context.Context, c Config, frames *atomic.Int64, cancel context.CancelFunc, dir string) (net.Listener, error) {
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
			serveControl(conn, c, frames, cancel)
		}
	}()
	return listener, nil
}

func serveControl(conn net.Conn, c Config, frames *atomic.Int64, cancel context.CancelFunc) {
	defer conn.Close() //nolint:errcheck // Response is bounded and connection is no longer used.
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return
	}
	var action string
	if err := json.NewDecoder(io.LimitReader(conn, 128)).Decode(&action); err != nil {
		return
	}
	state := "starting"
	if frames.Load() > 0 {
		state = "streaming"
	}
	if action == "stop" {
		state = "stopping"
		cancel()
	} else if action != "status" {
		return
	}
	if err := json.NewEncoder(conn).Encode(Status{State: state, URL: c.URL(), Frames: frames.Load()}); err != nil {
		return
	}
}

func (s *Service) runMedia(ctx context.Context, c Config, config string, frames *atomic.Int64, out io.Writer) error {
	versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	b, err := exec.CommandContext(versionCtx, s.Tools.MediaMTX, "--version").Output()
	cancel()
	if err != nil || strings.TrimSpace(string(b)) != MediaMTXVersion {
		return errors.New("unsupported MediaMTX runtime; use the pinned exported folder")
	}
	serverCmd := exec.Command(s.Tools.MediaMTX, config) //nolint:noctx // Owned child cleanup uses bounded interrupt/kill and reap.
	serverOutput, err := serverCmd.StdoutPipe()
	if err != nil {
		return err
	}
	server, err := startChild(serverCmd)
	if err != nil {
		return errors.New("could not launch MediaMTX")
	}
	defer server.stop()
	ready := make(chan struct{}, 1)
	go func() {
		scanner := bufio.NewScanner(serverOutput)
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
		return err
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
	if _, err := fmt.Fprintln(out, "Camera starting; waiting for published video frames. Ctrl-C or camstream-camera stop ends capture."); err != nil {
		return err
	}
	return monitor(ctx, c, server, producer, frames, out)
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

func monitor(ctx context.Context, c Config, server, producer *child, frames *atomic.Int64, out io.Writer) error {
	startup := time.NewTimer(45 * time.Second)
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
		case <-producer.done:
			return errors.New("camera capture stopped; check macOS camera permission, device availability and resolution/fps")
		case <-startup.C:
			if !ready {
				return errors.New("no video frames within 45 seconds; grant camera access and retry")
			}
		case <-ticker.C:
			if !ready && frames.Load() > 0 {
				ready = true
				if _, err := fmt.Fprintln(out, "Streaming video at "+c.URL()+" (authenticated RTSP/TCP)"); err != nil {
					return err
				}
			}
		}
	}
}
