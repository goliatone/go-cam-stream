package config

import (
	"net/netip"
	"path/filepath"
)

// Management is startup policy. Disabling it does not disable loading an
// existing overlay or its independently persisted creator grants.
type Management struct {
	Enabled          bool     `koanf:"enabled"`
	StateDir         string   `koanf:"state_dir"`
	Principals       []string `koanf:"principals"`
	DestinationCIDRs []string `koanf:"destination_cidrs"`
}

func (m Management) Validate() error {
	if m.StateDir != "" && (!filepath.IsAbs(m.StateDir) || filepath.Clean(m.StateDir) != m.StateDir || m.StateDir == "/") {
		return invalid()
	}
	if m.Enabled && len(m.Principals) == 0 {
		return invalid()
	}
	seen := map[string]bool{}
	for _, p := range m.Principals {
		if !ValidLogin(p) || seen[p] {
			return invalid()
		}
		seen[p] = true
	}
	for _, raw := range m.DestinationCIDRs {
		p, err := netip.ParsePrefix(raw)
		if err != nil || p != p.Masked() {
			return invalid()
		}
	}
	return nil
}
