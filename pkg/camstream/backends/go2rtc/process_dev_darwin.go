//go:build darwin && camstream_dev

package go2rtc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const DarwinARM64SHA256 = "d69780b0ed222866f2a9a8160537bae9936887ff467626ccba00b8840c609266"

func supportedPlatform() bool  { return runtime.GOARCH == "arm64" && nativeSocketInspection }
func executableSHA256() string { return DarwinARM64SHA256 }

func ContainerBoundary(context.Context) error {
	return fmt.Errorf("linux containment unavailable on Darwin")
}
func lockDirectory(dir string) (*os.File, error) {
	fd, err := unix.Open(filepath.Join(dir, ".lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "runtime-lock")
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

// DevelopmentBoundary waits for inherited worker locks before the application
// retires a crashed generation. It never reads or signals a persisted PID.
func DevelopmentBoundary(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("private development runtime directory required")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, err := lockDirectory(dir)
		if err == nil {
			return f.Close()
		}
		if !errors.Is(err, unix.EWOULDBLOCK) || time.Now().After(deadline) {
			return err
		}
		if err := pause(ctx, 20*time.Millisecond); err != nil {
			return err
		}
	}
}

func processGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func launchProcess(cmd *exec.Cmd, lock *os.File) (*os.File, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	args := append([]string{exe, "--dev-go2rtc-worker", cmd.Path}, cmd.Args[1:]...)
	cmd.Path, cmd.Args = exe, args
	cmd.ExtraFiles = []*os.File{reader, lock}
	processGroup(cmd)
	err = cmd.Start()
	closeErr := reader.Close()
	if err != nil {
		return nil, errors.Join(err, closeErr, writer.Close())
	}
	return writer, nil
}

// RunDevelopmentWorker handles the private child entry point. The still-live
// group leader can kill its own group without PID-reuse risk. The inherited
// runtime flock covers the watcher and media process through owner death.
func RunDevelopmentWorker(args []string) (bool, int) {
	if len(args) == 0 || args[0] != "--dev-go2rtc-worker" {
		return false, 0
	}
	if len(args) != 4 || args[2] != "-config" || unix.Getpgrp() != os.Getpid() {
		return true, 2
	}
	lifetime := os.NewFile(3, "owner-lifetime")
	lock := os.NewFile(4, "worker-lock")
	info, err := lifetime.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return true, 2
	}
	info, err = lock.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return true, 2
	}
	killGroup := func() {
		if err := unix.Kill(-os.Getpid(), unix.SIGKILL); err != nil {
			os.Exit(1)
		}
	}
	go func() {
		_, _ = io.Copy(io.Discard, lifetime) //nolint:errcheck // EOF or any read failure ends ownership.
		killGroup()
	}()
	cmd := exec.Command(args[1], args[2:]...) //nolint:noctx // The lifetime watcher terminates the complete owned group.
	cmd.Env = os.Environ()
	cmd.ExtraFiles = []*os.File{lock}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	_ = cmd.Run() //nolint:errcheck // Any child exit ends the group; no error is exposed to clients.
	killGroup()
	return true, 1
}

func processExited(pid int) bool {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return errors.Is(unix.Kill(pid, 0), unix.ESRCH)
	}
	return info.Proc.P_stat == 5 // SZOMB: retained until group cleanup and Wait.
}
func groupAlive(group int) (bool, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return true, err
	}
	for _, p := range all {
		if int(p.Eproc.Pgid) == group && p.Proc.P_stat != 5 {
			return true, nil
		}
	}
	return false, nil
}
func stopGroup(ctx context.Context, cmd *exec.Cmd, grace time.Duration) error {
	pid := cmd.Process.Pid
	if err := unix.Kill(-pid, unix.SIGTERM); err != nil && !errors.Is(err, unix.ESRCH) {
		return err
	}
	deadline := time.Now().Add(grace)
	killed := false
	for {
		alive, err := groupAlive(pid)
		if err != nil {
			return err
		}
		if !alive {
			err := cmd.Wait()
			var exited *exec.ExitError
			if err != nil && !errors.As(err, &exited) {
				return err
			}
			return nil
		}
		if !killed && time.Now().After(deadline) {
			if err := unix.Kill(-pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
				return err
			}
			killed = true
		}
		if err := pause(ctx, 10*time.Millisecond); err != nil {
			return err
		}
	}
}
func ownsListeners(group int, checks []listener) error {
	members, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return err
	}
	for _, check := range checks {
		owned := false
		for _, p := range members {
			if int(p.Eproc.Pgid) == group && p.Proc.P_stat != 5 && nativeListenerOwned(group, int(p.Proc.P_pid), check) {
				owned = true
				break
			}
		}
		if !owned {
			return fmt.Errorf("assigned listener not owned by worker")
		}
	}
	return nil
}
