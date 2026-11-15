package maccamera

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This opt-in uses synthetic video only. All other paths are the production
// supervisor, RTSP authentication, preview service, decoder and frame socket.
func TestPreviewActualRuntime(t *testing.T) {
	tools := os.Getenv("CAMSTREAM_PREVIEW_RUNTIME")
	if tools == "" {
		t.Skip("set CAMSTREAM_PREVIEW_RUNTIME to verified tools")
	}
	s := testService(t)
	c, secret, err := load(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("not TCP")
	}
	c.Port = addr.Port
	closePreviewIO(listener)
	if err = writeJSON(filepath.Join(s.Dir, "camera.json"), c); err != nil {
		t.Fatal(err)
	}
	ffmpeg := filepath.Join(tools, "ffmpeg")
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'" }
	wrapper := filepath.Join(t.TempDir(), "synthetic-capture")
	script := "#!/bin/sh\ncase \" $* \" in\n*' -f avfoundation '*)\nfor arg do last=$arg; done\nexec " + quote(ffmpeg) + " -hide_banner -loglevel error -re -f lavfi -i testsrc2=size=1280x720:rate=30 -an -c:v libx264 -preset ultrafast -tune zerolatency -pix_fmt yuv420p -g 30 -progress pipe:1 -f rtsp -rtsp_transport tcp \"$last\";;\n*) exec " + quote(ffmpeg) + " \"$@\";;\nesac\n"
	if err = os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s.Tools = Tools{FFmpeg: wrapper, MediaMTX: filepath.Join(tools, "mediamtx")}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer closePreviewIO(reader)
	defer closePreviewIO(writer)
	done := make(chan error, 1)
	go func() { done <- s.Supervise(ctx, reader, io.Discard) }()
	defer func() {
		cancel()
		closePreviewIO(writer)
		select {
		case runErr := <-done:
			if runErr != nil {
				t.Error(runErr)
			}
		case <-time.After(8 * time.Second):
			t.Error("supervisor cleanup timed out")
		}
	}()
	waitStreaming(t, ctx, s)
	e, err := s.AttachPreview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "unix", e.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer closePreviewIO(conn)
	first := readPreviewFrame(t, conn, e.Generation)
	second := readPreviewFrame(t, conn, e.Generation)
	if string(first) == string(second) {
		t.Fatal("synthetic preview frozen")
	}
	if err = s.DetachPreview(ctx, e.Generation); err != nil {
		t.Fatal(err)
	}
	// Subscriber failure must leave an authenticated remote-style reader working.
	source := filepath.Join(s.Dir, "probe.ffconcat")
	value := fmt.Sprintf("ffconcat version 1.0\nfile 'rtsp://%s:%s@127.0.0.1:%d/%s'\noption rtsp_transport tcp\n", secret.Username, secret.Password, c.Port, c.Path)
	if err = writePrivate(source, []byte(value)); err != nil {
		t.Fatal(err)
	}
	args := previewArgs(source, 640, 360)
	args = append(args[:len(args)-1], "-frames:v", "3", "pipe:1")
	output, err := exec.CommandContext(ctx, ffmpeg, args...).Output()
	if err != nil || len(output) != 3*previewMaxBytes {
		t.Fatal("RTSP playback failed after detach")
	}
	e, err = s.AttachPreview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conn2, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "unix", e.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer closePreviewIO(conn2)
	readPreviewFrame(t, conn2, e.Generation)
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	waitStopped(t, ctx, s)
	if err = conn2.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, conn2) //nolint:errcheck // Drain buffered frames to the expected disconnect.
	t.Log("synthetic H.264, actual preview frames, independent reader and supervisor owner-loss passed")
}
