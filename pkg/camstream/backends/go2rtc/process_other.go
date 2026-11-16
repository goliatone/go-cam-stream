//go:build !linux && (!darwin || !camstream_dev)

package go2rtc

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

type listener struct {
	network string
	port    int
}

func ContainerBoundary(context.Context) error { return failure(camstream.CodeUnsupported, nil) }
func lockDirectory(string) (*os.File, error)  { return nil, failure(camstream.CodeUnsupported, nil) }
func processGroup(*exec.Cmd)                  {}
func processExited(int) bool                  { return true }
func stopGroup(context.Context, *exec.Cmd, time.Duration) error {
	return failure(camstream.CodeUnsupported, nil)
}
func ownsListeners(int, []listener) error { return failure(camstream.CodeUnsupported, nil) }
