package config_test

import (
	"testing"

	"github.com/goliatone/go-cam-stream/config"
)

func TestSharingExternalOriginContract(t *testing.T) {
	defaults := (config.Sharing{}).WithDefaults()
	if defaults.Ingress.Mode != config.SharingIngressDisabled || defaults.Relay.Mode != config.SharingRelayDisabled || defaults.Validate() != nil {
		t.Fatal("unset sharing must be disabled")
	}
	for raw, want := range map[string]string{
		"https://View.Example.test:443/": "https://view.example.test",
		"https://view.example.test:8443": "https://view.example.test:8443",
		"https://[::1]:8443":             "https://[::1]:8443",
		"https://[::ffff:192.0.2.1]":     "https://[::ffff:c000:201]",
		"https://127.0.0.1":              "https://127.0.0.1",
		"https://XN--BCHER-KVA.test":     "https://xn--bcher-kva.test",
		"https://127.1":                  "",
		"https://127.00.00.1":            "",
		"https://0x7f000001":             "",
		"https://abc.123":                "",
		"https://view.example.test]":     "",
		"https://[::1]extra":             "",
		"https://xn--a.test":             "",
		"https://K.test":                 "",
		"http://view.example.test":       "",
		"https://user@view.example.test": "",
		"https://view.example.test/?":    "",
		"https://view.example.test/#":    "",
		"https://view.example.test/site": "",
		"https://view.example.test:":     "",
		"https://[view.example.test]":    "",
	} {
		settings := config.Sharing{Ingress: config.SharingIngress{Mode: config.SharingIngressExternal, PublicBaseURL: raw, GuestListen: "127.0.0.1:8890"}}
		origin, err := settings.Ingress.PublicOrigin()
		if origin != want || (err == nil) != (want != "") || (settings.Validate() == nil) != (want != "") {
			t.Errorf("incorrect origin classification for %q", raw)
		}
	}
	unsafe := config.Sharing{Ingress: config.SharingIngress{Mode: config.SharingIngressExternal, PublicBaseURL: "https://view.example.test", GuestListen: "0.0.0.0:8890"}}
	if unsafe.Validate() == nil {
		t.Fatal("network-facing guest listener accepted")
	}
}

func TestSharingRelayConfigIsIndependent(t *testing.T) {
	settings := config.Sharing{Relay: config.SharingRelay{
		Mode: config.SharingRelayCoturn, CredentialRef: "relay-key", URLs: []string{"turn:relay.example.test", "turns:relay.example.test:443?transport=tcp"},
	}}
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
	settings.Relay.URLs = []string{"turns:relay.example.test?transport=udp"}
	if settings.Validate() == nil {
		t.Fatal("invalid secure TURN transport accepted")
	}
	settings.Relay.URLs = []string{"turn:relay.example.test]"}
	if settings.Validate() == nil {
		t.Fatal("malformed TURN authority accepted")
	}
}
