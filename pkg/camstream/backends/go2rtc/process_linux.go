//go:build linux

package go2rtc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type listener struct {
	network string
	port    int
}

// ContainerBoundary requires this controller to be the direct child of the
// container init. Launch it as the container command, not via an un-exec'd shell.
// The host must use the tested dedicated --init container and restart ordering.
func ContainerBoundary(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	init, err := os.Readlink("/proc/1/exe")
	if err != nil {
		return err
	}
	name := filepath.Base(init)
	if os.Getppid() != 1 || (name != "docker-init" && name != "tini") {
		return fmt.Errorf("controller must be direct child of container init")
	}
	return nil
}
func lockDirectory(dir string) (*os.File, error) {
	fd, err := syscall.Open(filepath.Join(dir, ".lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "runtime-lock")
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}
func processGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func processStat(pid int) (state string, group int, err error) {
	body, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", 0, err
	}
	fields := strings.Fields(string(body)[strings.LastIndex(string(body), ")")+1:])
	if len(fields) < 3 {
		return "", 0, fmt.Errorf("incomplete process observation")
	}
	group, err = strconv.Atoi(fields[2])
	return fields[0], group, err
}
func processExited(pid int) bool {
	state, _, err := processStat(pid)
	return os.IsNotExist(err) || state == "Z" || state == "X"
}
func groupAlive(group int) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true, err
	}
	for _, e := range entries {
		pid, parseErr := strconv.Atoi(e.Name())
		if parseErr != nil {
			continue
		}
		state, pg, readErr := processStat(pid)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return true, readErr
		}
		if pg == group && state != "Z" && state != "X" {
			return true, nil
		}
	}
	return false, nil
}

// Do not reap the leader until the entire group is dead. Its unreaped child PID
// cannot be reused while signaling the group. Never signal this group after Wait.
func stopGroup(ctx context.Context, cmd *exec.Cmd, grace time.Duration) error {
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
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
			waitErr := cmd.Wait()
			var exited *exec.ExitError
			if waitErr != nil && !errors.As(waitErr, &exited) {
				return waitErr
			}
			return nil
		}
		if !killed && time.Now().After(deadline) {
			if err = syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				return err
			}
			killed = true
		}
		if err = pause(ctx, 10*time.Millisecond); err != nil {
			return err
		}
	}
}
func ownsListeners(pid int, checks []listener) error {
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return err
	}
	sockets := make(map[string]bool)
	for _, fd := range fds {
		link, e := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, fd.Name()))
		if e == nil {
			sockets[link] = true
		}
	}
	for _, check := range checks {
		if !ownsPort(sockets, check) {
			return fmt.Errorf("assigned listener not owned by worker")
		}
	}
	return nil
}
func ownsPort(sockets map[string]bool, check listener) bool {
	for _, suffix := range []string{"", "6"} {
		body, err := os.ReadFile("/proc/net/" + check.network + suffix)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(body), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) < 10 {
				continue
			}
			_, portHex, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			port, err := strconv.ParseInt(portHex, 16, 32)
			if err != nil || int(port) != check.port {
				continue
			}
			if check.network == "tcp" && fields[3] != "0A" {
				continue
			}
			if sockets["socket:["+fields[9]+"]"] {
				return true
			}
		}
	}
	return false
}
