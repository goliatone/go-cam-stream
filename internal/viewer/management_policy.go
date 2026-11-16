package viewer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/pkg/camstream/strategies/rtsp"
)

type ManagementPolicy struct {
	enabled      bool
	principals   map[string]bool
	ranges       []netip.Prefix
	installation string
}

func NewManagementPolicy(cfg config.Management, installation string) (*ManagementPolicy, error) {
	if cfg.Validate() != nil || !config.ValidID(installation) {
		return nil, invalid()
	}
	p := &ManagementPolicy{enabled: cfg.Enabled, principals: map[string]bool{}, installation: installation}
	for _, login := range cfg.Principals {
		p.principals[login] = true
	}
	for _, raw := range cfg.DestinationCIDRs {
		prefix, _ := netip.ParsePrefix(raw)
		p.ranges = append(p.ranges, prefix)
	}
	return p, nil
}
func (p *ManagementPolicy) CanManage(ctx context.Context) bool {
	return p != nil && p.enabled && p.principals[principal(ctx)]
}
func (p *ManagementPolicy) Require(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !p.CanManage(ctx) {
		return denied()
	}
	return nil
}
func (p *ManagementPolicy) RecoveryScope(ctx context.Context) string {
	if !config.ValidLogin(principal(ctx)) {
		return ""
	}
	sum := sha256.Sum256([]byte(p.installation + "\x00" + principal(ctx)))
	return hex.EncodeToString(sum[:])
}

var tailscaleRange = netip.MustParsePrefix("100.64.0.0/10")

func (p *ManagementPolicy) AllowsAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.Zone() != "" || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return false
	}
	if addr.IsLoopback() || addr.IsPrivate() || tailscaleRange.Contains(addr) {
		return true
	}
	for _, prefix := range p.ranges {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// AddressBinding is private persisted identity. Paths and credentials never
// distinguish physical devices. Pins and all admitted aliases survive restart.
type AddressBinding struct {
	Host      string   `json:"host"`
	Port      uint16   `json:"port"`
	Selected  string   `json:"selected"`
	Addresses []string `json:"addresses"`
}

func (AddressBinding) String() string   { return "[address binding]" }
func (AddressBinding) GoString() string { return "[address binding]" }
func (b AddressBinding) IdentityKeys() ([]string, error) {
	if b.Host == "" || b.Port == 0 || len(b.Addresses) == 0 || len(b.Addresses) > 16 {
		return nil, invalid()
	}
	if _, err := (rtsp.Config{Host: b.Host, Port: b.Port, Profiles: []rtsp.Profile{{ID: "default", Path: "/"}}}).Normalize(); err != nil {
		return nil, invalid()
	}
	keys := []string{"host:" + net.JoinHostPort(strings.ToLower(strings.TrimSuffix(b.Host, ".")), strconv.Itoa(int(b.Port)))}
	selected := false
	for _, raw := range b.Addresses {
		addr, err := netip.ParseAddr(raw)
		if err != nil || addr.Zone() != "" || addr.String() != raw || addr.Is4In6() || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() {
			return nil, invalid()
		}
		if raw == b.Selected {
			selected = true
		}
		identity := addr.String()
		if addr.IsLoopback() {
			identity = "loopback"
		}
		keys = append(keys, "addr:"+net.JoinHostPort(identity, strconv.Itoa(int(b.Port))))
	}
	if !selected {
		return nil, invalid()
	}
	// A logical localhost alias overlaps literal v4/v6 loopback even before DNS.
	if strings.EqualFold(strings.TrimSuffix(b.Host, "."), "localhost") {
		keys = append(keys, "addr:"+net.JoinHostPort("loopback", strconv.Itoa(int(b.Port))))
	}
	slices.Sort(keys)
	return slices.Compact(keys), nil
}
func BindingOverlap(a, b AddressBinding) bool {
	ak, ae := a.IdentityKeys()
	bk, be := b.IdentityKeys()
	if ae != nil || be != nil {
		return true
	}
	for _, k := range ak {
		if slices.Contains(bk, k) {
			return true
		}
	}
	return false
}
func validateBinding(b AddressBinding) error {
	if _, err := b.IdentityKeys(); err != nil {
		return err
	}
	return nil
}
func duplicateCamera() error { return api.ManagementFailure(api.CodeDuplicateCamera) }

func (p *ManagementPolicy) Managers() []string {
	out := []string{}
	if p != nil && p.enabled {
		for login := range p.principals {
			out = append(out, login)
		}
	}
	slices.Sort(out)
	return out
}
