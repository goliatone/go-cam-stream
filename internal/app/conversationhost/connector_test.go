package conversationhost

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

func testConnector(t *testing.T) *Connector {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	metrics := listener.Addr().String()
	_ = listener.Close()
	ingress := config.SharingIngress{Mode: config.SharingIngressExternal, PublicBaseURL: "https://mac.example.test", GuestListen: "127.0.0.1:18894"}
	const id = "356af671-6769-4c51-b6f1-3cd77fb7b76f"
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c, err := NewConnector(dir, config.Conversations{Ingress: ingress, Connector: &config.CallConnector{
		TunnelID: id, Metrics: metrics, Cloudflared: "/bin/sh", PublicOrigin: ingress.PublicBaseURL, Upstream: ingress.GuestListen,
	}})
	if err != nil {
		t.Fatal(err)
	}
	token := base64.StdEncoding.EncodeToString([]byte(`{"t":"` + id + `","s":"private-fixture"}`))
	if err = os.WriteFile(c.token, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConnectorBootstrapAndReconnectAreScoped(t *testing.T) {
	c := testConnector(t)
	loaded, id := false, ""
	var changes [][]string
	c.readiness = func(context.Context) string { return id }
	c.launch = func(_ context.Context, args ...string) ([]byte, error) {
		switch {
		case slices.Equal(args, []string{"print", c.target()}):
			if !loaded {
				return nil, errors.New("job absent")
			}
			return []byte("\n\tpath = " + c.plist + "\n\tstate = running\n"), nil
		case slices.Equal(args, []string{"bootstrap", c.domain, c.plist}):
			loaded, id = true, "first"
		case slices.Equal(args, []string{"kickstart", "-k", c.target()}):
			id = "second"
		default:
			t.Fatalf("unexpected launchd operation: %q", args)
		}
		changes = append(changes, args)
		return nil, nil
	}
	if got := c.Status(t.Context()); got != api.CallConnectorStopped {
		t.Fatalf("status %q", got)
	}
	if err := c.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.Ensure(t.Context()); err != nil || len(changes) != 1 {
		t.Fatalf("healthy ensure restarted the connector: %v", err)
	}
	if err := c.Reconnect(t.Context()); err != nil || len(changes) != 2 {
		t.Fatalf("reconnect: %v", err)
	}
	if got := c.Status(t.Context()); got != api.CallConnectorConnected {
		t.Fatalf("status %q", got)
	}
	definition := string(c.definition())
	if !strings.Contains(definition, "<string>http2</string>") || !strings.Contains(definition, "<string>--token-file</string>") ||
		strings.Contains(definition, "private-fixture") || strings.Contains(definition, "t06") {
		t.Fatal("job does not use HTTP/2 and a private credential path")
	}
}

func TestConnectorRefusesUnexpectedJobOrToken(t *testing.T) {
	c := testConnector(t)
	c.launch = func(_ context.Context, args ...string) ([]byte, error) {
		if !slices.Equal(args, []string{"print", c.target()}) {
			t.Fatalf("unexpected mutation: %q", args)
		}
		return []byte("path = /tmp/foreign.plist\nstate = running\n"), nil
	}
	if err := c.Reconnect(t.Context()); !errors.Is(err, ErrConnectorSetup) {
		t.Fatalf("foreign job: %v", err)
	}
	bad := base64.StdEncoding.EncodeToString([]byte(`{"t":"b5fda499-f3f1-43d6-ab30-619e2f4b243c"}`))
	if err := os.WriteFile(c.token, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	c.launch = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("launchctl called with a foreign tunnel")
		return nil, nil
	}
	if err := c.Ensure(t.Context()); !errors.Is(err, ErrConnectorSetup) {
		t.Fatalf("foreign token: %v", err)
	}
}
