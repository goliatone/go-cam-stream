package maccamera

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Opt-in, synthetic only. Every AVFoundation capture/list invocation is replaced
// or rejected by the wrapper; help is metadata. No camera/microphone is opened.
// The actual helper -> worker -> runtime -> observer ownership chain is retained.
func TestAudioActualRuntime(t *testing.T) {
	tools := os.Getenv("CAMSTREAM_AUDIO_RUNTIME")
	if tools == "" {
		t.Skip("set CAMSTREAM_AUDIO_RUNTIME to verified runtime-3 tools")
	}
	root := t.TempDir()
	helper := filepath.Join(root, "camstream-camera")
	build := exec.Command("go", "build", "-o", helper, "../../cmd/camstream-camera")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	for _, mode := range []string{"silence-stop", "owner-loss", "missing", "stall", "observer-exit", "removed", "video-only"} {
		t.Run(mode, func(t *testing.T) { actualAudioSession(t, root, helper, tools, mode) })
	}
}

func actualAudioSession(t *testing.T, root, helper, tools, mode string) {
	t.Helper()
	s := testService(t)
	c, _, err := load(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.Port = listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	c.AudioEnabled, c.AudioDeviceID = mode != "video-only", "synthetic: mic"
	if err = writeJSON(filepath.Join(s.Dir, "camera.json"), c); err != nil {
		t.Fatal(err)
	}
	runtimeDir, _ := s.runtimeDir()
	fixtureTools := filepath.Join(root, "tools")
	if err = os.MkdirAll(fixtureTools, 0700); err != nil {
		t.Fatal(err)
	}
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'" }
	capture := "echo $$ > " + quote(filepath.Join(s.Dir, "producer.pid")) + "\nfor arg do last=$arg; done\nexec " + quote(filepath.Join(tools, "ffmpeg")) + " -hide_banner -loglevel error -nostdin -nostats -progress pipe:1 -re -f lavfi -i testsrc2=size=320x240:rate=30 "
	if mode != "missing" && mode != "video-only" {
		capture += "-re -f lavfi -i anullsrc=r=48000:cl=mono,aformat=sample_fmts=s16 -map 0:v:0 -map 1:a:0 -c:a libopus -ac 1 -ar 48000 -b:a 64k -frame_duration 20 "
	} else {
		capture += "-an "
	}
	capture += "2>" + quote(filepath.Join(s.Dir, "synthetic-stderr")) + " -c:v libx264 -pix_fmt yuv420p -preset ultrafast -tune zerolatency -g 30 -b:v 500k -f rtsp -rtsp_transport tcp \"$last\""
	if mode == "removed" {
		capture = "exit 91"
	}
	script := "#!/bin/sh\ncase \" $* \" in\n*' -h demuxer=avfoundation '*) exec " + quote(filepath.Join(tools, "ffmpeg")) + " \"$@\";;\n*' -camstream_list_audio_devices '*) printf '%s\\n' '{\"version\":1,\"devices\":[{\"id\":\"synthetic: mic\",\"name\":\"Synthetic microphone\"}]}'; exit 251;;\n*' -f avfoundation '*) " + capture + ";;\n*) exec " + quote(filepath.Join(tools, "ffmpeg")) + " \"$@\";;\nesac\n"
	if err = os.WriteFile(filepath.Join(fixtureTools, "ffmpeg"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"ffprobe", "mediamtx"} {
		// PID files are test-only metadata; argv and stderr never persist media/URLs.
		body := "#!/bin/sh\nif [ \"$1\" != '-version' ] && [ \"$1\" != '--version' ]; then echo $$ > " + quote(filepath.Join(s.Dir, tool+".pid")) + "; fi\nexec " + quote(filepath.Join(tools, tool)) + " \"$@\"\n"

		if err = os.WriteFile(filepath.Join(fixtureTools, tool), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 32*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, helper, "app-session", "--dir", s.Dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	replies := make(chan AppReply, 32)
	go func() {
		defer close(replies)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var r AppReply
			if json.Unmarshal(scanner.Bytes(), &r) == nil {
				replies <- r
			}
		}
	}()
	finished := false
	defer func() {
		_ = stdin.Close()
		if !finished {
			select {
			case <-done:
			case <-time.After(12 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				t.Error("helper cleanup timeout")
			}
		}
	}()
	receive := func() AppReply {
		select {
		case r, ok := <-replies:
			if !ok {
				t.Fatalf("helper closed: %s", stderr.String())
			}
			return r
		case <-ctx.Done():
			t.Fatal("helper timed out")
			return AppReply{}
		}
	}
	send := func(method string) {
		if err := json.NewEncoder(stdin).Encode(AppRequest{Version: 1, ID: method, Method: method}); err != nil {
			t.Fatal(err)
		}
	}
	if r := receive(); r.Event != "ready" {
		t.Fatal(r)
	}
	start := time.Now()
	send("start")
	if r := receive(); r.ID != "start" || r.Error != nil {
		t.Fatal(r)
	}
	if mode == "missing" || mode == "removed" {
		r := receive()
		if r.Event != "capture_stopped" || r.Error == nil || (r.Error.Code != "audio_startup_timeout" && r.Error.Code != "audio_observer_failed" && r.Error.Code != "audio_capture_failed") {
			t.Fatal(r)
		}
		if time.Since(start) > 23*time.Second {
			t.Fatal("startup bound exceeded")
		}
		t.Logf("%s fails closed in %s (%s)", mode, time.Since(start).Round(time.Millisecond), r.Error.Code)
	} else {
		for {
			select {
			case r := <-replies:
				t.Fatalf("capture ended before readiness: %+v error=%+v", r, r.Error)
			default:
			}
			status, err := s.Status(ctx)
			if err == nil && status.State == "streaming" {
				break
			}
			if ctx.Err() != nil {
				t.Fatal("never streamed")
			}
			time.Sleep(25 * time.Millisecond)
		}
		status, err := s.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if c.AudioEnabled && (status.Audio.State != "live" || status.Audio.Packets == 0) {
			t.Fatal(status)
		}
		t.Logf("%s ready in %s, audio packets=%d", mode, time.Since(start).Round(time.Millisecond), status.Audio.Packets)
		if mode == "silence-stop" {
			endpoint, err := s.AttachPreview(ctx)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := net.Dial("unix", endpoint.Socket)
			if err != nil {
				t.Fatal(err)
			}
			readPreviewFrame(t, conn, endpoint.Generation)
			_ = conn.Close()
			if err = s.DetachPreview(ctx, endpoint.Generation); err != nil {
				t.Fatal(err)
			}
			status, err = s.Status(ctx)
			if err != nil || status.State != "streaming" || status.Audio.Packets == 0 {
				t.Fatal("preview interrupted audio", status, err)
			}
		}
		if mode == "stall" || mode == "observer-exit" {
			name := "producer"
			signal := syscall.SIGSTOP
			if mode == "observer-exit" {
				name = "ffprobe"
				signal = syscall.SIGKILL
			}
			pid := runtimePID(t, s.Dir, name)
			if err = syscall.Kill(pid, signal); err != nil {
				t.Fatal(err)
			}
			interrupted := time.Now()
			r := receive()
			if r.Event != "capture_stopped" || r.Error == nil || (r.Error.Code != "audio_stalled" && r.Error.Code != "audio_observer_failed") {
				t.Fatal(r)
			}
			if time.Since(interrupted) > 12*time.Second {
				t.Fatal("stall cleanup exceeded bound")
			}
			t.Logf("%s reaped in %s (%s)", mode, time.Since(interrupted).Round(time.Millisecond), r.Error.Code)
		}
	}
	if mode == "owner-loss" {
		_ = cmd.Process.Kill()
	} else {
		send("stop")
		if r := receive(); r.ID != "stop" || r.Error != nil {
			t.Fatal(r)
		}
		send("quit")
		if r := receive(); r.ID != "quit" {
			t.Fatal(r)
		}
	}
	select {
	case err = <-done:
		finished = true
		if err != nil && mode != "owner-loss" {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("helper not reaped")
	}
	// Killing the parent asynchronously closes the worker's lifetime pipe.
	cleanupDeadline := time.Now().Add(12 * time.Second)
	for {
		alive := false
		for _, name := range []string{"producer", "ffprobe", "mediamtx"} {
			b, readErr := os.ReadFile(filepath.Join(s.Dir, name+".pid"))
			if readErr != nil {
				continue
			}
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			if syscall.Kill(pid, 0) == nil {
				alive = true
			}
		}
		if !alive {
			break
		}
		if time.Now().After(cleanupDeadline) {
			t.Fatal("owned runtime process survived")
		}
		time.Sleep(25 * time.Millisecond)
	}
	for _, name := range []string{"audio.ffconcat", "mediamtx.json", "control.sock"} {
		if _, err = os.Stat(filepath.Join(runtimeDir, name)); !os.IsNotExist(err) {
			t.Fatal("private runtime resource retained", name, err)
		}
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", c.Port), 200*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatal("listener survived")
	}
	if mode == "video-only" {
		if _, err = os.Stat(filepath.Join(s.Dir, "ffprobe.pid")); !os.IsNotExist(err) {
			t.Fatal("audio observer launched when disabled")
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected helper diagnostics: %s", stderr.String())
	}
}

func runtimePID(t *testing.T, dir, name string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name+".pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}
