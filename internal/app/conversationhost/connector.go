package conversationhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

var (
	ErrConnectorSetup       = errors.New("dedicated call connector setup required")
	ErrConnectorUnavailable = errors.New("dedicated call connector unavailable")
)

// Connector controls one enrolled launchd job and its loopback readiness URL.
// It has no provider API, DNS, or ability to control a different job label.
type Connector struct {
	profile                                       config.CallConnector
	dir, domain, label, plist, token, cloudConfig string
	launch                                        func(context.Context, ...string) ([]byte, error)
	readiness                                     func(context.Context) string
}

func NewConnector(dir string, settings config.Conversations) (*Connector, error) {
	if settings.Connector == nil {
		return nil, nil
	}
	if settings.Connector.Validate(settings.Ingress) != nil || !filepath.IsAbs(dir) {
		return nil, ErrConnectorSetup
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, ErrConnectorSetup
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrConnectorSetup
	}
	p := *settings.Connector
	label := "com.goliatone.camstream.call-connector." + p.TunnelID
	c := &Connector{
		profile: p, dir: dir, domain: "gui/" + strconv.Itoa(os.Getuid()), label: label,
		plist: filepath.Join(dir, label+".plist"), token: filepath.Join(dir, "conversation-connector.token"),
		cloudConfig: filepath.Join(dir, "conversation-connector-cloudflared.yml"),
		launch: func(ctx context.Context, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, "/bin/launchctl", args...).Output()
		},
	}
	c.readiness = c.readyID
	return c, nil
}

func (c *Connector) Status(ctx context.Context) api.CallConnectorState {
	if c == nil {
		return api.CallConnectorExternal
	}
	if err := c.validateFiles(false); err != nil {
		return api.CallConnectorSetup
	}
	loaded, running, err := c.inspect(ctx)
	if err != nil {
		return api.CallConnectorSetup
	}
	if !loaded {
		return api.CallConnectorStopped
	}
	if running && c.readiness(ctx) != "" {
		return api.CallConnectorConnected
	}
	return api.CallConnectorConnecting
}

// Ensure starts an unloaded job and waits for Cloudflare registration. Reconnect
// restarts the exact job and waits for a different connector's readiness ID.
func (c *Connector) Ensure(ctx context.Context) error    { return c.start(ctx, false) }
func (c *Connector) Reconnect(ctx context.Context) error { return c.start(ctx, true) }

func (c *Connector) start(ctx context.Context, reconnect bool) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if c == nil {
		return ErrConnectorSetup
	}
	if err := c.validateFiles(false); err != nil {
		return err
	}
	loaded, running, err := c.inspect(ctx)
	if err != nil {
		return err
	}
	previousID := ""
	if loaded && running {
		previousID = c.readiness(ctx)
		if previousID != "" && !reconnect {
			return nil
		}
	}
	if !loaded {
		if err := c.bootstrap(ctx); err != nil {
			return err
		}
	} else if reconnect || !running {
		args := []string{"kickstart"}
		if reconnect {
			args = append(args, "-k")
		}
		if _, err := c.launch(ctx, append(args, c.target())...); err != nil {
			return ErrConnectorUnavailable
		}
	}
	return c.awaitRegistration(ctx, previousID)
}

func (c *Connector) bootstrap(ctx context.Context) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", c.profile.Metrics)
	if err != nil {
		return ErrConnectorSetup
	}
	if err := listener.Close(); err != nil {
		return ErrConnectorSetup
	}
	if err := c.validateFiles(true); err != nil {
		return err
	}
	if _, err := c.launch(ctx, "bootstrap", c.domain, c.plist); err != nil {
		return ErrConnectorUnavailable
	}
	return nil
}

func (c *Connector) awaitRegistration(ctx context.Context, previousID string) error {
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		if id := c.readiness(ctx); id != "" && id != previousID {
			loaded, running, err := c.inspect(ctx)
			if err != nil {
				return err
			}
			if loaded && running {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ErrConnectorUnavailable
		case <-tick.C:
		}
	}
}

func (c *Connector) target() string { return c.domain + "/" + c.label }

func (c *Connector) inspect(ctx context.Context) (loaded, running bool, err error) {
	out, err := c.launch(ctx, "print", c.target())
	if err != nil {
		if ctx.Err() != nil {
			return false, false, ErrConnectorUnavailable
		}
		return false, false, nil
	}
	var owned bool
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		owned = owned || line == "path = "+c.plist
		running = running || line == "state = running"
	}
	if !owned || !matchesPrivate(c.plist, c.definition()) || !matchesPrivate(c.cloudConfig, []byte("{}\n")) {
		return false, false, ErrConnectorSetup
	}
	return true, running, nil
}

func (c *Connector) readyID(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 700*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+c.profile.Metrics+"/ready", nil)
	if err != nil {
		return ""
	}
	response, err := connectorHTTP.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close() //nolint:errcheck // Bounded read from local metrics only.
	var reply struct {
		ConnectorID      string `json:"connectorId"`      //nolint:tagliatelle // cloudflared response field.
		ReadyConnections int    `json:"readyConnections"` //nolint:tagliatelle // cloudflared response field.
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&reply) != nil || reply.ReadyConnections < 1 {
		return ""
	}
	return reply.ConnectorID
}

var connectorHTTP = http.Client{
	Transport:     &http.Transport{DisableKeepAlives: true}, // No ambient proxy for loopback control.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (c *Connector) validateFiles(install bool) error {
	info, err := os.Stat(c.profile.Cloudflared)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 {
		return ErrConnectorSetup
	}
	token, err := privateContents(c.token)
	if err != nil {
		return ErrConnectorSetup
	}
	defer clear(token)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(token)))
	if err != nil {
		return ErrConnectorSetup
	}
	defer clear(decoded)
	var identity struct {
		Tunnel string `json:"t"`
	}
	if json.Unmarshal(decoded, &identity) != nil || identity.Tunnel != c.profile.TunnelID {
		return ErrConnectorSetup
	}
	if install {
		if installPrivate(c.cloudConfig, []byte("{}\n")) != nil || installPrivate(c.plist, c.definition()) != nil {
			return ErrConnectorSetup
		}
	}
	return nil
}

func (c *Connector) definition() []byte {
	args := []string{"/usr/bin/env", "-i", "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=" + c.dir,
		c.profile.Cloudflared, "tunnel", "--no-autoupdate", "--protocol", "http2", "--config", c.cloudConfig,
		"--metrics", c.profile.Metrics, "run", "--token-file", c.token}
	var out strings.Builder
	out.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>`)
	out.WriteString(c.label)
	out.WriteString(`</string><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>5</integer><key>ProgramArguments</key><array>`)
	for _, arg := range args {
		out.WriteString("<string>")
		_ = xml.EscapeText(&out, []byte(arg)) //nolint:errcheck // strings.Builder writes cannot fail.
		out.WriteString("</string>")
	}
	out.WriteString(`</array></dict></plist>`)
	return []byte(out.String())
}

func privateContents(name string) ([]byte, error) {
	file, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck // Read-only file.
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 8192 {
		return nil, ErrConnectorSetup
	}
	return io.ReadAll(io.LimitReader(file, 8193))
}

func matchesPrivate(path string, expected []byte) bool {
	current, err := privateContents(path)
	return err == nil && bytes.Equal(current, expected)
}

// Install atomically if absent, preserving any file installed by an operator.
func installPrivate(path string, data []byte) error {
	if matchesPrivate(path, data) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".call-connector-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // Private temporary file.
	_, err = tmp.Write(data)
	if err = errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	if err = os.Link(tmp.Name(), path); err != nil && !matchesPrivate(path, data) {
		return ErrConnectorSetup
	}
	return nil
}
