package go2rtc_test

import (
	"context"
	"fmt"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
)

type source string

func (source) Kind() camstream.SourceKind { return "example/rtsp" }

type encoder struct{}

func (encoder) Kind() camstream.SourceKind { return "example/rtsp" }
func (encoder) Encode(ctx context.Context, s camstream.Source) (go2rtc.Input, error) {
	if err := ctx.Err(); err != nil {
		return go2rtc.Input{}, err
	}
	v, ok := s.(source)
	if !ok {
		return go2rtc.Input{}, camstream.NewError(camstream.CodeUnsupported, nil)
	}
	return go2rtc.Input{URL: string(v)}, nil
}
func ExampleNew() {
	backend, err := go2rtc.New(go2rtc.Config{
		Executable: "/opt/camstream/go2rtc", RuntimeDir: "/run/camstream/backend",
		PrivatePorts: go2rtc.PortRange{First: 20000, Last: 20100}, MediaPorts: go2rtc.PortRange{First: 21000, Last: 21031},
		MediaAddress: "100.64.0.10", CandidateAddress: "100.64.0.10", VerifyBoundary: go2rtc.ContainerBoundary,
	}, encoder{})
	fmt.Println(backend != nil, err)
	// Construction does no I/O. A verified Linux arm64 host explicitly calls Start,
	// EnsureStream and CreateSession, then Close with its bounded cleanup context.

	// Output: true <nil>
}
