// Package ec70 describes the Kasa EC70 default mixed-stream profile. It performs
// no network I/O or secret resolution; hardware compatibility is deployment evidence.
package ec70

import (
	"net/netip"
	"strings"
	"unicode"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	secrets "github.com/goliatone/go-secrets"
)

const (
	ID            camstream.StrategyID = "ec70"
	DefaultStream camstream.StreamID   = "default"
	SourceKind    camstream.SourceKind = "goliatone.com/camstream/ec70-kasa"
	DefaultPort   uint16               = 19443
)

// Config takes a bare DNS name or IP address, never a URL or embedded port.
// Zero Port selects 19443. References identify the TP-Link account username and
// original password (not an already base64-encoded value).
type Config struct {
	Host        string
	Port        uint16
	UsernameRef secrets.Reference
	PasswordRef secrets.Reference
}

func (Config) StrategyID() camstream.StrategyID { return ID }

// Validate checks syntax only. Provider availability and values are checked by
// the backend encoder. A valid configuration does not prove camera compatibility.
func (c Config) Validate() error {
	_, err := c.normalized()
	return err
}

func (c Config) normalized() (Config, error) {
	host, ok := normalizeHost(c.Host)
	if !ok || c.UsernameRef.URI() == "" || c.PasswordRef.URI() == "" {
		return Config{}, camstream.NewError(camstream.CodeInvalidArgument, nil)
	}
	c.Host = host
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	return c, nil
}

func normalizeHost(host string) (string, bool) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Unmap().String(), addr.Zone() == "" && !addr.IsUnspecified() && !addr.IsMulticast()
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if len(host) == 0 || len(host) > 253 {
		return "", false
	}
	for _, label := range strings.Split(host, ".") {
		if !validLabel(label) {
			return "", false
		}
	}
	return host, true
}

func validLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, ch := range label {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func validID(id string) bool {
	return id != "" && len(id) <= 256 && strings.TrimSpace(id) == id && !strings.ContainsFunc(id, unicode.IsControl)
}
