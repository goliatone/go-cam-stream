// Package rtsp describes explicitly configured RTSP video profiles without
// network I/O, secret resolution, process creation or backend dependencies.
package rtsp

import (
	"net"
	"slices"
	"strconv"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	secrets "github.com/goliatone/go-secrets"
)

const (
	ID            camstream.StrategyID = "rtsp"
	SourceKind    camstream.SourceKind = "goliatone.com/camstream/rtsp"
	DefaultPort   uint16               = 554
	MaxProfiles                        = 32
	MaxPathBytes                       = 2048
	MaxQueryBytes                      = 4096
	MaxLabelBytes                      = 256
)

// Config is a caller-owned input value. Normalize and strategy methods copy its
// profile slice; callers must not mutate input concurrently with those calls.
// Zero Port selects 554. Authentication requires both references or neither.
// The host owns provider-specific reference policy (for example env-only).
// Confidential fields are excluded from JSON; this is not a wire config DTO.
type Config struct {
	Host        string            `json:"-"`
	Port        uint16            `json:"-"`
	UsernameRef secrets.Reference `json:"-"`
	PasswordRef secrets.Reference `json:"-"`
	Profiles    []Profile         `json:"-"`
}

// Profile describes a video source. Path is a decoded absolute path; Query is
// an already escaped raw query, preserved exactly. Neither may contain secrets.
// Audio is opt-in and declares expected tracks, not browser codec compatibility.
type Profile struct {
	ID    camstream.StreamID
	Name  string
	Path  string `json:"-"`
	Query string `json:"-"`
	Audio bool
}

func (Config) StrategyID() camstream.StrategyID { return ID }
func (Config) String() string                   { return "[rtsp config]" }
func (Config) GoString() string                 { return "[rtsp config]" }
func (Profile) String() string                  { return "[rtsp profile]" }
func (Profile) GoString() string                { return "[rtsp profile]" }

// Validate checks syntax only, including all profiles. It performs no I/O.
func (c Config) Validate() error {
	_, err := c.Normalize()
	return err
}

// Normalize returns a validated independent copy with canonical host/port and
// default display names. It preserves profile order, paths and raw queries.
func (c Config) Normalize() (Config, error) {
	host, ok := normalizeHost(c.Host)
	if !ok || !validReferences(c.UsernameRef, c.PasswordRef) || len(c.Profiles) == 0 || len(c.Profiles) > MaxProfiles {
		return Config{}, invalid()
	}
	c.Host = host
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	c.Profiles = slices.Clone(c.Profiles)
	seen := make(map[camstream.StreamID]bool, len(c.Profiles))
	for i, profile := range c.Profiles {
		if !validProfile(profile) || seen[profile.ID] {
			return Config{}, invalid()
		}
		seen[profile.ID] = true
		if profile.Name == "" {
			c.Profiles[i].Name = string(profile.ID)
		}
	}
	return c, nil
}

// Endpoint returns the private canonical device endpoint for host duplicate-
// identity checks. It excludes profiles and credentials and must not be logged.
func (c Config) Endpoint() (string, error) {
	normalized, err := c.Normalize()
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(normalized.Host, strconv.Itoa(int(normalized.Port))), nil
}

func invalid() error { return camstream.NewError(camstream.CodeInvalidArgument, nil) }
