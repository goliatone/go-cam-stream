package config

import (
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
)

// Conversations configures an independent room listener and its relay policy.
// The host supplies credentials through the same private resolver as sharing.
type Conversations struct {
	Ingress        SharingIngress `koanf:"ingress" json:"ingress"`
	Relay          SharingRelay   `koanf:"relay" json:"relay"`
	RelayOnly      bool           `koanf:"relay_only" json:"relay_only,omitempty"`
	Loopback       bool           `koanf:"loopback" json:"loopback,omitempty"`
	TrustedOrigins []string       `koanf:"trusted_origins" json:"trusted_origins,omitempty"`
	Connector      *CallConnector `koanf:"connector" json:"connector,omitempty"`
}

// CallConnector describes one already provisioned public tunnel binding.
// The connector credential lives in a separate private file.
type CallConnector struct {
	TunnelID     string `json:"tunnel_id"`
	PublicOrigin string `json:"public_origin"`
	Upstream     string `json:"upstream"`
	Metrics      string `json:"metrics"`
	Cloudflared  string `json:"cloudflared"`
}

var tunnelUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (c CallConnector) Validate(ingress SharingIngress) error {
	origin, err := ingress.PublicOrigin()
	metrics, metricsErr := netip.ParseAddrPort(c.Metrics)
	if err != nil || origin != c.PublicOrigin || c.Upstream != ingress.GuestListen ||
		metricsErr != nil || metrics.Addr().String() != "127.0.0.1" || metrics.Port() == 0 ||
		c.Metrics == c.Upstream || !tunnelUUID.MatchString(c.TunnelID) ||
		!filepath.IsAbs(c.Cloudflared) || filepath.Clean(c.Cloudflared) != c.Cloudflared {
		return invalid()
	}
	return nil
}

func (c Conversations) Validate() error {
	network := (Sharing{Ingress: c.Ingress, Relay: c.Relay}).WithDefaults()
	if c.Loopback && network.Ingress.Mode != SharingIngressDisabled {
		return invalid()
	}
	if c.Connector != nil && (c.Loopback || c.Connector.Validate(c.Ingress) != nil) {
		return invalid()
	}
	if c.RelayOnly && network.Relay.Mode == SharingRelayDisabled {
		return invalid()
	}
	if len(c.TrustedOrigins) > 32 {
		return invalid()
	}
	for _, origin := range c.TrustedOrigins {
		if _, err := ConversationOrigin(origin, c.Loopback); err != nil {
			return err
		}
	}
	return network.Validate()
}

// ConversationOrigin permits explicit development origins on the IPv4
// loopback interface. Operator-configured remote origins always use HTTPS.
func ConversationOrigin(raw string, loopback bool) (string, error) {
	if loopback {
		if authority, ok := strings.CutPrefix(raw, "http://"); ok {
			addr, err := netip.ParseAddrPort(authority)
			if err != nil || addr.Addr().String() != "127.0.0.1" || addr.Port() == 0 || addr.String() != authority {
				return "", invalid()
			}
			return raw, nil
		}
	}
	return (SharingIngress{Mode: SharingIngressExternal, PublicBaseURL: raw}).PublicOrigin()
}
