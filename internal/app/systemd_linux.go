//go:build linux

package app

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

const systemdGroup = "/system.slice/camstream.service"

// Select a verified deployment boundary, never an environment-variable bypass.
// Native support requires the shipped nondelegated system service on cgroup v2.
func verifyCleanDeployment(ctx context.Context) error {
	membership, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return cs.NewError(cs.CodeBackendUnavailable, nil)
	}
	if strings.TrimSpace(string(membership)) != "0::"+systemdGroup {
		return verifyCleanContainer(ctx)
	}
	return verifyCleanSystemd(ctx)
}
func verifyCleanSystemd(ctx context.Context) error {
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	properties, err := exec.CommandContext(probe, "/usr/bin/systemctl", "--system", "show", "--no-pager",
		"--property=MainPID,ControlGroup,KillMode,SendSIGKILL,Restart,Delegate,TimeoutStopUSec,User,Type,ExitType,ActiveState",
		"camstream.service").Output()
	if err != nil || !systemdProperties(string(properties), os.Getpid()) {
		return cs.NewError(cs.CodeBackendUnavailable, nil)
	}
	var stat syscall.Statfs_t
	if err = syscall.Statfs("/sys/fs/cgroup", &stat); err != nil || stat.Type != 0x63677270 {
		return cs.NewError(cs.CodeBackendUnavailable, nil)
	}
	if err = cleanCgroup(os.DirFS("/sys/fs/cgroup"+systemdGroup), os.Getpid()); err != nil {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	return ctx.Err()
}
func systemdProperties(body string, pid int) bool {
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return false
		}
		if _, duplicate := values[key]; duplicate {
			return false
		}
		values[key] = value
	}
	expected := map[string]string{"MainPID": strconv.Itoa(pid), "ControlGroup": systemdGroup, "KillMode": "control-group", "SendSIGKILL": "yes", "Restart": "on-failure", "Delegate": "no", "TimeoutStopUSec": "1min", "User": "camstream", "Type": "exec", "ExitType": "main"}
	for key, value := range expected {
		if values[key] != value {
			return false
		}
	}
	return values["ActiveState"] == "active" || values["ActiveState"] == "activating"
}

// Walk subgroups too: lingering helpers must block retirement/readmission.
func cleanCgroup(root fs.FS, pid int) error {
	seen := false
	err := fs.WalkDir(root, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fs.ErrInvalid
		}
		if entry.IsDir() || filepath.Base(name) != "cgroup.procs" {
			return nil
		}
		body, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		for _, word := range strings.Fields(string(body)) {
			value, err := strconv.Atoi(word)
			if err != nil || value != pid {
				return fs.ErrInvalid
			}
			seen = true
		}
		return nil
	})
	if err == nil && !seen {
		return fs.ErrInvalid
	}
	return err
}
