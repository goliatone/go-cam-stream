package rtsp_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
)

func TestInvalidHosts(t *testing.T) {
	for _, host := range []string{
		"", "rtsp://camera", "user@camera", "camera:554", "camera/path", "camera?x", "camera#x", " camera",
		"camera ", "a..b", "-a", "a-", "camera..", "[::1]", "fe80::1%en0", "0.0.0.0", "::", "224.0.0.1",
		"ff02::1", "::ffff:0.0.0.0", "::ffff:224.0.0.1", "0.0.0.0.", "224.0.0.1.", "::1.", "bad\x00host", "nonascii-é", "bad\xff",
		strings.Repeat("a", 64) + ".invalid", strings.Repeat("label.", 43) + strings.Repeat("z", 40),
	} {
		t.Run(host, func(t *testing.T) {
			cfg := config()
			cfg.Host = host
			assertCode(t, cfg.Validate(), camstream.CodeInvalidArgument)
			if endpoint, err := cfg.Endpoint(); endpoint != "" || camstream.DescribeError(err).Code != camstream.CodeInvalidArgument {
				t.Fatal("invalid host produced endpoint")
			}
		})
	}
}

func TestInvalidProfiles(t *testing.T) {
	changes := map[string]func(*rtsp.Config){
		"none":          func(c *rtsp.Config) { c.Profiles = nil },
		"duplicate":     func(c *rtsp.Config) { c.Profiles[1].ID = c.Profiles[0].ID },
		"empty-id":      func(c *rtsp.Config) { c.Profiles[0].ID = "" },
		"long-id":       func(c *rtsp.Config) { c.Profiles[0].ID = camstream.StreamID(strings.Repeat("a", rtsp.MaxLabelBytes+1)) },
		"space-id":      func(c *rtsp.Config) { c.Profiles[0].ID = " main" },
		"nul-id":        func(c *rtsp.Config) { c.Profiles[0].ID = "main\x00" },
		"utf8-id":       func(c *rtsp.Config) { c.Profiles[0].ID = "bad\xff" },
		"name":          func(c *rtsp.Config) { c.Profiles[0].Name = "\n" },
		"name-padding":  func(c *rtsp.Config) { c.Profiles[0].Name = " " },
		"long-name":     func(c *rtsp.Config) { c.Profiles[0].Name = strings.Repeat("a", rtsp.MaxLabelBytes+1) },
		"long-path":     func(c *rtsp.Config) { c.Profiles[0].Path = "/" + strings.Repeat("a", rtsp.MaxPathBytes) },
		"long-query":    func(c *rtsp.Config) { c.Profiles[0].Query = strings.Repeat("a", rtsp.MaxQueryBytes+1) },
		"username-only": func(c *rtsp.Config) { c.UsernameRef = reference(t, "env://U") },
		"password-only": func(c *rtsp.Config) { c.PasswordRef = reference(t, "env://P") },
		"too-many":      func(c *rtsp.Config) { c.Profiles = profiles(rtsp.MaxProfiles + 1) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			cfg := config()
			change(&cfg)
			assertCode(t, cfg.Validate(), camstream.CodeInvalidArgument)
		})
	}
	for _, path := range []string{"", "relative", "rtsp://camera/live", "exec:ffmpeg", "/live?channel=1", "/live#media=audio", "/live\n", "/live\x7f", "/bad\xff"} {
		cfg := config()
		cfg.Profiles[0].Path = path
		assertCode(t, cfg.Validate(), camstream.CodeInvalidArgument)
	}
	for _, query := range []string{"x=%", "x=%GG", "x=%0", "x=one two", "x=é", "x=1#backchannel=1", "x=1;y=2", "x=\n", "x=%0a", "%00=x", "x=%7f", "x=%ff", "x=%C2%85", "x=\\"} {
		cfg := config()
		cfg.Profiles[0].Query = query
		assertCode(t, cfg.Validate(), camstream.CodeInvalidArgument)
	}
}

func profiles(count int) []rtsp.Profile {
	out := make([]rtsp.Profile, count)
	for i := range out {
		out[i] = rtsp.Profile{ID: camstream.StreamID(fmt.Sprintf("profile-%d", i)), Path: "/live"}
	}
	return out
}

func TestBoundsAndRawURLSemantics(t *testing.T) {
	cfg := config()
	cfg.Host = strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	cfg.Port = 65535
	cfg.Profiles = profiles(rtsp.MaxProfiles)
	cfg.Profiles[0].ID = camstream.StreamID(strings.Repeat("i", rtsp.MaxLabelBytes))
	cfg.Profiles[0].Name = strings.Repeat("n", rtsp.MaxLabelBytes)
	cfg.Profiles[0].Path = "/" + strings.Repeat("p", rtsp.MaxPathBytes-1)
	cfg.Profiles[0].Query = "q=" + strings.Repeat("q", rtsp.MaxQueryBytes-2)
	if err := cfg.Validate(); err != nil {
		t.Fatal("boundary values rejected", err)
	}
	for _, pair := range [][2]string{
		{"/cam//../日本語 space", "a=1&a=2&flag&x=%2f&x=%2F&name=hello+world"},
		{"/literal%20percent", "x=%23&value=a%3Bb&name=%E6%97%A5"},
		{"//live/./camera", "path=/one?two&symbols=!$'()*,:@"},
		{"/", ""},
	} {
		cfg = config()
		cfg.Profiles[0].Path, cfg.Profiles[0].Query = pair[0], pair[1]
		selected := source(t, resolve(t, camera(cfg), "main")).Config().Profiles[0]
		if selected.Path != pair[0] || selected.Query != pair[1] {
			t.Fatal("provider rewrote path/query")
		}
		// Exercise the documented decoded-path contract an encoder consumes.
		u := url.URL{Scheme: "rtsp", Host: "camera.invalid:554", Path: selected.Path, RawQuery: selected.Query}
		parsed, err := url.Parse(u.String())
		if err != nil || parsed.Path != pair[0] || parsed.RawQuery != pair[1] || parsed.Fragment != "" {
			t.Fatal("URL roundtrip changed camera route", err)
		}
	}
}
