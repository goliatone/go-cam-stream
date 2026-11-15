// Package config owns startup-only reference-application configuration. It does
// not construct servers, start backends or resolve credential values.
package config

import (
	"context"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	gc "github.com/goliatone/go-config/config"
)

type Config struct {
	InstallationID   string        `koanf:"installation_id"`
	Origin           string        `koanf:"origin"`
	Listen           string        `koanf:"listen"`
	OperationTimeout time.Duration `koanf:"operation_timeout"`
	Limits           Limits        `koanf:"limits"`
	Backend          Backend       `koanf:"backend"`
	Cameras          []Camera      `koanf:"cameras"`
	Access           []Access      `koanf:"access"`
}
type Limits struct {
	MaxViewers         int           `koanf:"max_viewers"`
	PerSourceViewers   int           `koanf:"per_source_viewers"`
	MaxSources         int           `koanf:"max_sources"`
	MaxSessionRecords  int           `koanf:"max_session_records"`
	NegotiationTimeout time.Duration `koanf:"negotiation_timeout"`
	CleanupTimeout     time.Duration `koanf:"cleanup_timeout"`
	PollInterval       time.Duration `koanf:"poll_interval"`
	ObservationMaxAge  time.Duration `koanf:"observation_max_age"`
}
type Ports struct {
	First int `koanf:"first"`
	Last  int `koanf:"last"`
}
type Backend struct {
	Driver           string        `koanf:"driver"`
	Executable       string        `koanf:"executable"`
	RuntimeDir       string        `koanf:"runtime_dir"`
	PrivatePorts     Ports         `koanf:"private_ports"`
	MediaPorts       Ports         `koanf:"media_ports"`
	MediaAddress     string        `koanf:"media_address"`
	CandidateAddress string        `koanf:"candidate_address"`
	Network          string        `koanf:"network"`
	StartupTimeout   time.Duration `koanf:"startup_timeout"`
	FailureTimeout   time.Duration `koanf:"failure_timeout"`
	CleanupTimeout   time.Duration `koanf:"cleanup_timeout"`
	TermGrace        time.Duration `koanf:"term_grace"`
	PollInterval     time.Duration `koanf:"poll_interval"`
}
type Camera struct {
	ID         cs.CameraID         `koanf:"id"`
	Name       string              `koanf:"name"`
	PhysicalID cs.PhysicalCameraID `koanf:"physical_id"`
	Strategy   cs.StrategyID       `koanf:"strategy"`
	Config     map[string]any      `koanf:"config"` // raw host decoding only; never a core domain value
	Streams    []cs.StreamID       `koanf:"streams"`
}
type Access struct {
	Login  string  `koanf:"login"`
	Grants []Grant `koanf:"grants"`
}
type Grant struct {
	CameraID  cs.CameraID   `koanf:"camera_id"`
	Streams   []cs.StreamID `koanf:"streams"`
	View      bool          `koanf:"view"`
	Diagnose  bool          `koanf:"diagnose"`
	Reconnect bool          `koanf:"reconnect"`
}

func Defaults() Config {
	return Config{Listen: "127.0.0.1:8080", OperationTimeout: 15 * time.Second,
		Limits:  Limits{MaxViewers: 32, PerSourceViewers: 8, MaxSources: 16, MaxSessionRecords: 10000, NegotiationTimeout: 10 * time.Second, CleanupTimeout: 6 * time.Second, PollInterval: 250 * time.Millisecond, ObservationMaxAge: 5 * time.Second},
		Backend: Backend{Driver: "go2rtc", Executable: "/usr/local/bin/go2rtc", RuntimeDir: "/var/lib/camstream/runtime", PrivatePorts: Ports{35000, 35099}, MediaPorts: Ports{35100, 35131}, Network: "tcp+udp", StartupTimeout: 10 * time.Second, FailureTimeout: 10 * time.Second, CleanupTimeout: 5 * time.Second, TermGrace: time.Second, PollInterval: 250 * time.Millisecond}}
}

// Load reads exactly the requested file. Default go-config expression/URI/env
// solvers and string transformations are disabled; references remain references.
// Errors deliberately discard parser/provider causes which can include input.
func Load(ctx context.Context, path string) (Config, error) {
	raw := Defaults()
	loader := gc.New(&raw).WithSolvers().WithDefaultTransformers(false).WithStrictDecode(true).WithLogger(quietLogger{}).
		WithProvider(gc.FileProvider[*Config](path))
	if err := loader.Load(ctx); err != nil {
		if ctx.Err() != nil {
			return Config{}, ctx.Err()
		}
		return Config{}, invalid()
	}
	return raw, nil
}

type quietLogger struct{}

func (quietLogger) Debug(string, ...any) {}
func (quietLogger) Info(string, ...any)  {}
func (quietLogger) Error(string, ...any) {}
func invalid() error                     { return cs.NewError(cs.CodeInvalidArgument, nil) }

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func ValidID(value string) bool { return identifier.MatchString(value) }
func ValidLogin(value string) bool {
	return value != "" && value != "*" && len(value) <= 320 && utf8.ValidString(value) && !strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

func (c Config) Validate() error {
	if !ValidID(c.InstallationID) || c.OperationTimeout <= 0 {
		return invalid()
	}
	if !validOrigin(c.Origin) {
		return invalid()
	}
	p, err := listenerPort(c.Listen)
	if err != nil {
		return err
	}
	if err = c.validateLimits(); err != nil {
		return err
	}
	if c.Backend.Driver != "go2rtc" || !cleanPath(c.Backend.Executable) || !cleanPath(c.Backend.RuntimeDir) {
		return invalid()
	}
	// Constructor validation only, with a verifier that always fails closed.
	_, err = go2rtc.New(c.BackendConfig(func(context.Context) error { return cs.NewError(cs.CodeBackendUnavailable, nil) }))
	if err != nil {
		return invalid()
	}
	return c.validateCapacity(p)
}
func (c Config) validateLimits() error {
	l := c.Limits
	if l.MaxViewers < 1 || l.MaxViewers > 65535 || l.PerSourceViewers < 1 || l.MaxSources < 1 || l.MaxSources > 65535 || l.MaxSessionRecords < l.MaxViewers {
		return invalid()
	}
	return c.validateTiming()
}
func (c Config) validateTiming() error {
	l := c.Limits
	if l.NegotiationTimeout < c.Backend.StartupTimeout || l.CleanupTimeout < c.Backend.CleanupTimeout || l.PollInterval <= 0 || l.ObservationMaxAge < l.PollInterval || l.ObservationMaxAge < c.Backend.PollInterval {
		return invalid()
	}
	if c.Backend.StartupTimeout <= 0 || c.Backend.FailureTimeout <= 0 || c.Backend.CleanupTimeout <= 0 || c.Backend.TermGrace <= 0 || c.Backend.PollInterval <= 0 || c.OperationTimeout < l.CleanupTimeout {
		return invalid()
	}
	return nil
}
func (c Config) validateCapacity(listenPort int) error {
	b, l := c.Backend, c.Limits
	if b.PrivatePorts.Last-b.PrivatePorts.First+1 < 2*min(l.MaxSources, l.MaxViewers)+l.MaxViewers || b.MediaPorts.Last-b.MediaPorts.First+1 < l.MaxViewers {
		return invalid()
	}
	for _, r := range []Ports{b.PrivatePorts, b.MediaPorts} {
		if listenPort >= r.First && listenPort <= r.Last {
			return invalid()
		}
	}
	return nil
}
func (c Config) ServiceOptions() cs.ServiceOptions {
	l := c.Limits
	return cs.ServiceOptions{MaxViewers: l.MaxViewers, PerSourceViewers: l.PerSourceViewers, MaxSources: l.MaxSources, MaxSessionRecords: l.MaxSessionRecords, NegotiationTimeout: l.NegotiationTimeout, CleanupTimeout: l.CleanupTimeout, PollInterval: l.PollInterval, ObservationMaxAge: l.ObservationMaxAge}
}

// BackendConfig requires the future composition root's real boundary verifier.
// It never enables exec/ffmpeg input or manufactures successful containment.
func (c Config) BackendConfig(verify func(context.Context) error) go2rtc.Config {
	b, l := c.Backend, c.Limits
	return go2rtc.Config{Executable: b.Executable, RuntimeDir: b.RuntimeDir, PrivatePorts: go2rtc.PortRange{First: b.PrivatePorts.First, Last: b.PrivatePorts.Last}, MediaPorts: go2rtc.PortRange{First: b.MediaPorts.First, Last: b.MediaPorts.Last}, MediaAddress: b.MediaAddress, CandidateAddress: b.CandidateAddress, Network: b.Network, StartupTimeout: b.StartupTimeout, FailureTimeout: b.FailureTimeout, CleanupTimeout: b.CleanupTimeout, TermGrace: b.TermGrace, PollInterval: b.PollInterval, MaxSources: l.MaxSources, MaxSessions: l.MaxViewers, MaxSessionRecords: l.MaxSessionRecords, VerifyBoundary: verify}
}

func cleanPath(value string) bool { return filepath.IsAbs(value) && filepath.Clean(value) == value }
func validOrigin(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	if u.Port() != "" {
		port, parseErr := strconv.Atoi(u.Port())
		if parseErr != nil || port < 1 || port > 65535 {
			return false
		}
	}
	return u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Opaque == ""
}
func listenerPort(value string) (int, error) {
	host, port, err := net.SplitHostPort(value)
	ip := net.ParseIP(host)
	p, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || ip == nil || !ip.IsLoopback() || p < 1024 || p > 65535 {
		return 0, invalid()
	}
	return p, nil
}
