//go:build !darwin || !camstream_dev

package go2rtc

import (
	"os"
	"os/exec"
	"runtime"
)

func supportedPlatform() bool  { return runtime.GOOS == "linux" && runtime.GOARCH == "arm64" }
func executableSHA256() string { return LinuxARM64SHA256 }
func launchProcess(cmd *exec.Cmd, _ *os.File) (*os.File, error) {
	processGroup(cmd)
	return nil, cmd.Start()
}
