//go:build linux

package app

import (
	"context"
	"os"
	"strconv"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
)

// A dedicated private PID namespace must be empty except for init/controller.
// Deployment additionally owns serial container stop/start for this installation;
// namespaces sharing a runtime volume must never overlap. Native systemd is T09.
func verifyCleanContainer(ctx context.Context) error {
	if err := go2rtc.ContainerBoundary(ctx); err != nil {
		return cs.NewError(cs.CodeBackendUnavailable, nil)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	for _, e := range entries {
		pid, parseErr := strconv.Atoi(e.Name())
		if parseErr == nil && pid != 1 && pid != os.Getpid() {
			return cs.NewError(cs.CodeCleanupIncomplete, nil)
		}
	}
	return ctx.Err()
}
