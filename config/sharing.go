package config

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

type SharingIngressMode string
type SharingRelayMode string

const (
	SharingIngressDisabled SharingIngressMode = "disabled"
	SharingIngressExternal SharingIngressMode = "external_https"
	SharingRelayDisabled   SharingRelayMode   = "disabled"
	SharingRelayCloudflare SharingRelayMode   = "cloudflare_turn"
	SharingRelayCoturn     SharingRelayMode   = "coturn"
)

type Sharing struct {
	Ingress SharingIngress `koanf:"ingress" json:"ingress"`
	Relay   SharingRelay   `koanf:"relay" json:"relay"`
}

type SharingIngress struct {
	Mode          SharingIngressMode `koanf:"mode" json:"mode"`
	PublicBaseURL string             `koanf:"public_base_url" json:"public_base_url,omitempty"`
	GuestListen   string             `koanf:"guest_listen" json:"guest_listen,omitempty"`
}

type SharingRelay struct {
	Mode          SharingRelayMode `koanf:"mode" json:"mode"`
	CredentialRef string           `koanf:"credential_ref" json:"credential_ref,omitempty"`
	URLs          []string         `koanf:"urls" json:"urls,omitempty"`
}

func (s Sharing) WithDefaults() Sharing {
	if s.Ingress.Mode == "" {
		s.Ingress.Mode = SharingIngressDisabled
	}
	if s.Relay.Mode == "" {
		s.Relay.Mode = SharingRelayDisabled
	}
	return s
}

func (s Sharing) Validate() error {
	return s.validate(false)
}

// ValidateSetup permits the native owner to submit settings before the host
// resolves the saved or newly supplied credential to a private reference.
func (s Sharing) ValidateSetup() error {
	return s.validate(true)
}

func (s Sharing) validate(allowUnbound bool) error {
	s = s.WithDefaults()
	if err := s.Ingress.validate(); err != nil {
		return err
	}
	return s.Relay.validate(allowUnbound)
}

func (i SharingIngress) validate() error {
	switch i.Mode {
	case SharingIngressDisabled:
		if i.PublicBaseURL != "" || i.GuestListen != "" {
			return invalid()
		}
	case SharingIngressExternal:
		if _, err := i.PublicOrigin(); err != nil {
			return err
		}
		addr, err := netip.ParseAddrPort(i.GuestListen)
		if err != nil || !addr.Addr().IsLoopback() || addr.Addr().Zone() != "" || addr.Port() == 0 {
			return invalid()
		}
	default:
		return invalid()
	}
	return nil
}

func (r SharingRelay) validate(allowUnbound bool) error {
	validRef := ValidID(r.CredentialRef) || (allowUnbound && r.CredentialRef == "")
	switch r.Mode {
	case SharingRelayDisabled:
		if r.CredentialRef != "" || len(r.URLs) != 0 {
			return invalid()
		}
	case SharingRelayCloudflare:
		if !validRef || len(r.URLs) != 0 {
			return invalid()
		}
	case SharingRelayCoturn:
		if !validRef || len(r.URLs) < 1 || len(r.URLs) > 8 {
			return invalid()
		}
		for _, raw := range r.URLs {
			if !validSharingRelayURL(raw) {
				return invalid()
			}
		}
	default:
		return invalid()
	}
	return nil
}

// PublicOrigin canonicalizes a configured HTTPS origin before the guest
// transport compares Host and Origin. The reverse proxy owns actual TLS.
func (i SharingIngress) PublicOrigin() (string, error) {
	u, err := url.Parse(i.PublicBaseURL)
	if i.Mode != SharingIngressExternal || err != nil || u.Scheme != "https" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawPath != "" || strings.ContainsAny(i.PublicBaseURL, "?#") {
		return "", invalid()
	}
	host, valid := canonicalSharingAuthority(u.Host, "443")
	if !valid {
		return "", invalid()
	}
	return "https://" + host, nil
}

func canonicalSharingAuthority(authority, defaultPort string) (string, bool) {
	bracketed := strings.HasPrefix(authority, "[")
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		host = authority
		port = defaultPort
		if bracketed && strings.HasSuffix(authority, "]") {
			host = authority[1 : len(authority)-1]
		}
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 || strconv.FormatUint(number, 10) != port {
		return "", false
	}
	host, valid := canonicalSharingHost(host, bracketed)
	if !valid {
		return "", false
	}
	if port != defaultPort {
		host += ":" + port
	}
	return host, true
}

var (
	sharingDNSLabel        = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	sharingIPv4NumberLabel = regexp.MustCompile(`^(?:[0-9]+|0x[0-9a-f]*)$`)
)

func canonicalSharingHost(host string, bracketed bool) (string, bool) {
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" || ip.Is6() != bracketed {
			return "", false
		}
		if ip.Is4In6() {
			// Browsers serialize mapped IPv6 addresses with hexadecimal pieces.
			v4 := ip.As4()
			return fmt.Sprintf("[::ffff:%x:%x]", binary.BigEndian.Uint16(v4[:2]), binary.BigEndian.Uint16(v4[2:])), true
		}
		if ip.Is6() {
			return "[" + ip.String() + "]", true
		}
		return ip.String(), true
	}
	if bracketed || len(host) > 253 {
		return "", false
	}
	for label := range strings.SplitSeq(host, ".") {
		if !sharingDNSLabel.MatchString(label) {
			return "", false
		}
	}
	host = strings.ToLower(host)
	// Browsers parse a numeric final label as IPv4, including legacy hex
	// and shortened forms. Only the canonical IPv4 accepted above is supported.
	if sharingIPv4NumberLabel.MatchString(host[strings.LastIndexByte(host, '.')+1:]) {
		return "", false
	}
	ascii, err := idna.Lookup.ToASCII(host)
	return host, err == nil && ascii == host
}

func validSharingRelayURL(raw string) bool {
	scheme, authority, present := strings.Cut(raw, ":")
	if !present || (scheme != "turn" && scheme != "turns") {
		return false
	}
	address, query, hasQuery := strings.Cut(authority, "?")
	validTransport := query == "transport=tcp" || (scheme == "turn" && query == "transport=udp")
	if hasQuery && !validTransport {
		return false
	}
	port := "3478"
	if scheme == "turns" {
		port = "5349"
	}
	_, valid := canonicalSharingAuthority(address, port)
	return valid
}
