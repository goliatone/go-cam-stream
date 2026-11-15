package rtsp_test

import (
	"context"
	"fmt"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
)

func ExampleStrategy() {
	camera := camstream.Camera{
		ID: "workshop", PhysicalID: "pi-camera-001",
		Config: rtsp.Config{Host: "camera.invalid", Port: 8554, Profiles: []rtsp.Profile{
			{ID: "default", Name: "Pi Camera", Path: "/camera"},
		}},
	}
	strategy := rtsp.Strategy{}
	streams, err := strategy.Streams(context.Background(), camera)
	if err != nil {
		panic(err)
	}
	spec, err := strategy.Resolve(context.Background(), camera, streams[0].ID)
	if err != nil {
		panic(err)
	}
	fmt.Println(streams[0].Name)
	fmt.Println(spec.Capabilities.Video, spec.Capabilities.Audio)
	fmt.Println(spec.Constraints.MaxActiveSources)
	// Output:
	// Pi Camera
	// true false
	// 1
}
