package rtsp_test

import (
	"context"
	"fmt"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	encoderpkg "github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/rtsp"
	strategy "github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
)

func ExampleNewEncoder() {
	camera := camstream.Camera{ID: "workshop", PhysicalID: "pi-1", Config: strategy.Config{
		Host: "camera.invalid", Port: 8554, Profiles: []strategy.Profile{{ID: "default", Path: "/camera"}},
	}}
	source, err := (strategy.Strategy{}).Resolve(context.Background(), camera, "default")
	if err != nil {
		panic(err)
	}
	encoder := encoderpkg.NewEncoder(nil) // Anonymous sources need no secret resolver.
	// Inject encoder into go2rtc.New(config, encoder). EnsureStream calls Encode
	// at the trusted boundary; never log or return its credential-bearing Input.
	_, err = encoder.Encode(context.Background(), source.Source)
	fmt.Println(encoder.Kind() == source.Source.Kind(), err == nil)
	// Output: true true
}
