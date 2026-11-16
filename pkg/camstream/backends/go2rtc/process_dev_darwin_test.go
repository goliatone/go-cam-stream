//go:build darwin && camstream_dev

package go2rtc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if handled, code := RunDevelopmentWorker(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestDevelopmentWorkerOwnerDeath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "helper")
	marker := filepath.Join(dir, "started")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf ready > \"$2\"\nsleep 60 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := lockDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, lock)
	cmd := exec.CommandContext(context.Background(), helper, "-config", marker)
	lifetime, err := launchProcess(cmd, lock)
	if err != nil {
		t.Fatal(err)
	}
	reaped := false
	defer func() {
		closeTestResource(t, lifetime)
		if !reaped {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := stopGroup(ctx, cmd, 20*time.Millisecond); err != nil {
				t.Error(err)
			}
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Unrelated process must survive cleanup of the owned worker group.
	unrelated := exec.CommandContext(context.Background(), "/bin/sleep", "60")
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unrelated.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Error(err)
		}
		waitTestChild(t, unrelated)
	}()
	// The inherited flock survives closing the controller's copy.
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if f, err := lockDirectory(dir); err == nil {
		closeTestResource(t, f)
		t.Fatal("worker did not retain runtime lock")
	}
	if err := lifetime.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := DevelopmentBoundary(ctx, dir); err != nil {
		t.Fatal("owner death did not release workers", err)
	}
	for {
		alive, err := groupAlive(cmd.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if !alive {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("owned descendants survived")
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitTestChild(t, cmd)
	reaped = true
	if processExited(unrelated.Process.Pid) {
		t.Fatal("unrelated process was stopped")
	}
}

func TestDevelopmentBoundaryWaitsForWorkers(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := lockDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := DevelopmentBoundary(ctx, dir); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("expected bounded refusal", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := DevelopmentBoundary(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
}

func TestDevelopmentArtifactPin(t *testing.T) {
	body, err := os.ReadFile("../../../../data/runtime/go2rtc.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var pin struct {
		Artifacts map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &pin); err != nil {
		t.Fatal(err)
	}
	if pin.Artifacts["darwin/arm64"].SHA256 != DarwinARM64SHA256 {
		t.Fatal("Darwin pin drift")
	}
}

func TestDevelopmentNativeListenerOwnership(t *testing.T) {
	tcp, err := new(net.ListenConfig).Listen(context.Background(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, tcp)
	udp, err := new(net.ListenConfig).ListenPacket(context.Background(), "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, udp)
	tcpAddr, tcpOK := tcp.Addr().(*net.TCPAddr)
	udpAddr, udpOK := udp.LocalAddr().(*net.UDPAddr)
	if !tcpOK || !udpOK {
		t.Fatal("unexpected listener address type")
	}
	checks := []listener{{"tcp", tcpAddr.Port}, {"udp", udpAddr.Port}}
	if err := ownsListeners(syscall.Getpgrp(), checks); err != nil {
		t.Fatal(err)
	}
	if nativeListenerOwned(syscall.Getpgrp()+1000000, os.Getpid(), checks[0]) {
		t.Fatal("foreign group accepted")
	}
	if err := tcp.Close(); err != nil {
		t.Fatal(err)
	}
	if nativeListenerOwned(syscall.Getpgrp(), os.Getpid(), checks[0]) {
		t.Fatal("closed listener accepted")
	}
}

func closeTestResource(t *testing.T, closer io.Closer) {
	t.Helper()
	if err := closer.Close(); err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, net.ErrClosed) {
		t.Error(err)
	}
}
func waitTestChild(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	err := cmd.Wait()
	var exited *exec.ExitError
	if err != nil && !errors.As(err, &exited) {
		t.Error(err)
	}
}
