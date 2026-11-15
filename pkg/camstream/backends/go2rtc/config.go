// Package go2rtc implements camstream contracts with isolated stock go2rtc
// workers. Constructors perform no I/O; Start requires a verified crash boundary.
package go2rtc

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

const Version = "1.9.15"
const LinuxARM64SHA256 = "af416f22b21a24ab83b8d78642190b4932ae695b4e386853f11d1b0319781763"

// Input is private backend syntax returned by a provider-owned encoder. It must
// never be serialized to an application client or included in diagnostic logs.
// Encode may resolve secrets but must honor context and perform no media dialing.
type Input struct{ URL string }
type PortRange struct{ First, Last int }

// Config is concrete-adapter configuration, not part of the generic API.
// VerifyBoundary must establish exclusive crash containment and clean restart
// ordering. ContainerBoundary supports the tested direct-child-of-init layout;
// native systemd verification remains a host/deployment responsibility (T09).
type Config struct {
	Executable, RuntimeDir                                                  string
	PrivatePorts, MediaPorts                                                PortRange
	MediaAddress, CandidateAddress                                          string // explicit IPv4 addresses; no auto/STUN discovery
	Network                                                                 string // "tcp", "udp", or "tcp+udp"
	StartupTimeout, FailureTimeout, CleanupTimeout, TermGrace, PollInterval time.Duration
	MaxSources, MaxSessions, MaxSessionRecords                              int
	AllowExec                                                               bool // only trusted, separately verified helpers which stay in their worker group
	VerifyBoundary                                                          func(context.Context) error
}

func (c *Config) defaults() {
	if c.Network == "" {
		c.Network = "tcp+udp"
	}
	if c.StartupTimeout == 0 {
		c.StartupTimeout = 10 * time.Second
	}
	if c.FailureTimeout == 0 {
		c.FailureTimeout = 10 * time.Second
	}
	if c.CleanupTimeout == 0 {
		c.CleanupTimeout = 5 * time.Second
	}
	if c.TermGrace == 0 {
		c.TermGrace = time.Second
	}
	if c.PollInterval == 0 {
		c.PollInterval = 250 * time.Millisecond
	}
	if c.MaxSources == 0 {
		c.MaxSources = 16
	}
	if c.MaxSessions == 0 {
		c.MaxSessions = 32
	}
	if c.MaxSessionRecords == 0 {
		c.MaxSessionRecords = 10000
	}
}
func (c Config) valid() bool {
	return filepath.IsAbs(c.Executable) && filepath.IsAbs(c.RuntimeDir) && c.validPorts() && c.validTiming() && c.validNetwork() && c.VerifyBoundary != nil && c.MaxSources > 0 && c.MaxSessions > 0 && c.MaxSessionRecords >= c.MaxSessions
}
func (c Config) validPorts() bool {
	valid := func(r PortRange) bool { return r.First > 1024 && r.Last >= r.First && r.Last <= 65535 }
	return valid(c.PrivatePorts) && valid(c.MediaPorts) && (c.PrivatePorts.Last < c.MediaPorts.First || c.MediaPorts.Last < c.PrivatePorts.First)
}
func (c Config) validNetwork() bool {
	valid := func(s string) bool { ip := net.ParseIP(s); return ip != nil && ip.To4() != nil && !ip.IsUnspecified() }
	return valid(c.MediaAddress) && valid(c.CandidateAddress) && (c.Network == "tcp" || c.Network == "udp" || c.Network == "tcp+udp")
}
func (c Config) validTiming() bool {
	return c.StartupTimeout > 0 && c.FailureTimeout > 0 && c.CleanupTimeout > 0 && c.TermGrace > 0 && c.TermGrace < c.CleanupTimeout && c.PollInterval > 0 && c.PollInterval < c.FailureTimeout
}
func (c Config) validInput(in Input) bool {
	if in.URL == "" || len(in.URL) > 16384 || strings.ContainsAny(in.URL, "\x00\r\n") {
		return false
	}
	return c.AllowExec || (!strings.HasPrefix(in.URL, "exec:") && !strings.HasPrefix(in.URL, "ffmpeg:"))
}
func failure(code camstream.ErrorCode, err error) error { return camstream.NewError(code, err) }
