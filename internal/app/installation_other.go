//go:build !linux && !darwin

package app

import cs "github.com/goliatone/go-cam-stream/pkg/camstream"

type installationLock struct{}

func acquireInstallation(string, string) (*installationLock, error) {
	return nil, cs.NewError(cs.CodeUnsupported, nil)
}
func (*installationLock) retireWorkers() error { return cs.NewError(cs.CodeUnsupported, nil) }
func (*installationLock) Close() error         { return nil }
