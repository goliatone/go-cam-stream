//go:build darwin && camstream_dev

package app

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	rtspencoder "github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/rtsp"
	strategy "github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
)

func TestDevelopmentSourcesPreserveCustomSources(t *testing.T) {
	for _, device := range []string{"", "0", "2"} {
		t.Run("device="+device, func(t *testing.T) { testDevelopmentSource(t, device) })
	}
}

func testDevelopmentSource(t *testing.T, device string) {
	t.Helper()
	encoder := developmentSourceEncoder{base: rtspencoder.NewEncoder(nil), ffmpeg: "/tools with spaces/ffmpeg", webcam: device}
	for _, input := range []struct {
		host, path, query string
		pattern           bool
	}{
		{"127.0.0.1", "/cam", "", true},
		{"192.0.2.10", "/cam", "", false},
		{"127.0.0.1", "/other", "", false},
		{"127.0.0.1", "/cam", "channel=1", false},
	} {
		cfg := strategy.Config{Host: input.host, Port: 8554, Profiles: []strategy.Profile{{ID: "default", Name: "test", Path: input.path, Query: input.query}}}
		spec, err := (strategy.Strategy{}).Resolve(context.Background(), cs.Camera{ID: "camera", PhysicalID: "camera", Config: cfg}, "default")
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := encoder.Encode(context.Background(), spec.Source)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(encoded.URL, "exec:") != input.pattern {
			t.Fatalf("unexpected encoder selection for %s%s", input.host, input.path)
		}
		if !input.pattern {
			continue
		}
		if !strings.Contains(encoded.URL, `"/tools with spaces/ffmpeg"`) || !strings.Contains(encoded.URL, " -an ") {
			t.Fatal("quoted executable or video-only encoding missing")
		}
		if device == "" {
			if !strings.Contains(encoded.URL, "testsrc2=") || strings.Contains(encoded.URL, "avfoundation") {
				t.Fatal("default must remain synthetic")
			}
		} else {
			if !strings.Contains(encoded.URL, " -f avfoundation ") || !strings.Contains(encoded.URL, `-i "`+device+`:none"`) || strings.Contains(encoded.URL, "testsrc2=") || strings.Contains(encoded.URL, " -re ") {
				t.Fatal("webcam must select only the video device in real time")
			}
		}
	}
}

func TestValidateDevelopmentSource(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  DevelopmentOptions
		valid bool
	}{
		{"custom", DevelopmentOptions{}, true},
		{"pattern", DevelopmentOptions{FFmpeg: "/bin/ffmpeg"}, true},
		{"webcam", DevelopmentOptions{FFmpeg: "/bin/ffmpeg", WebcamDevice: "0"}, true},
		{"second", DevelopmentOptions{FFmpeg: "/bin/ffmpeg", WebcamDevice: "2"}, true},
		{"missing executable", DevelopmentOptions{WebcamDevice: "0"}, false},
		{"relative executable", DevelopmentOptions{FFmpeg: "ffmpeg", WebcamDevice: "0"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateDevelopmentSource(tc.opts); (err == nil) != tc.valid {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
	for _, device := range []string{"-1", "+1", "0:0", "FaceTime", "0 -f lavfi", "65536", "1\n"} {
		t.Run(device, func(t *testing.T) {
			if err := validateDevelopmentSource(DevelopmentOptions{FFmpeg: "/bin/ffmpeg", WebcamDevice: device}); err == nil {
				t.Fatal("unsafe or invalid device accepted")
			}
		})
	}
}

func TestDevelopmentMediaAddress(t *testing.T) {
	interfaces := func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.70"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	for _, tc := range []struct{ input, want string }{
		{"", "127.0.0.1"}, {"127.0.0.1", "127.0.0.1"}, {"192.168.1.70", "192.168.1.70"},
		{"0.0.0.0", ""}, {"8.8.8.8", ""}, {"192.168.1.71", ""}, {"224.0.0.1", ""},
		{"localhost", ""}, {"::1", ""}, {"::ffff:192.168.1.70", ""}, {"192.168.1.70:35100", ""},
		{"169.254.1.2", ""}, {"127.0.0.2", ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := developmentMediaAddress(tc.input, interfaces)
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
	if _, err := developmentMediaAddress("192.168.1.70", func() ([]net.Addr, error) { return nil, errors.New("inspection failed") }); err == nil {
		t.Fatal("inspection failure accepted")
	}
}
