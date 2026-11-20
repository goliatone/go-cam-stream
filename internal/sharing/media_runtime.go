package sharing

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
)

type MediaRuntimeOptions struct {
	MediaMTX   string
	FFmpeg     string
	Supervisor string
	// LocalOnly limits the media socket and candidates for loopback fixtures.
	LocalOnly bool
	OnLive    func(ViewerAttempt)
	OnLost    func(ViewerAttempt)
}

type MediaRuntime struct {
	opts   MediaRuntimeOptions
	ctx    context.Context
	client *http.Client
	mu     sync.Mutex
	shares map[string]*runtimeSource
	peers  map[string]*runtimePeer
}

type runtimeSource struct {
	address, dir, token string
	server, encoder     *mediaProcess
	users               int
}

type runtimePeer struct {
	worker                            *mediaProcess
	source                            *runtimeSource
	scope                             ViewerAttempt
	address, dir, token, resource, id string
}

type mediaProcess struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func NewMediaRuntime(ctx context.Context, opts MediaRuntimeOptions) (*MediaRuntime, error) {
	if !filepath.IsAbs(opts.MediaMTX) || !filepath.IsAbs(opts.FFmpeg) {
		return nil, fault(api.ShareInvalidRequest)
	}
	return &MediaRuntime{opts: opts, ctx: ctx,
		client: &http.Client{Transport: &http.Transport{DisableKeepAlives: true},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		shares: make(map[string]*runtimeSource), peers: make(map[string]*runtimePeer)}, nil
}

func runMediaProcess(ctx context.Context, dir, tool string, args ...string) (*mediaProcess, error) {
	return launchMediaProcess(ctx, dir, "", tool, args...)
}

func launchMediaProcess(ctx context.Context, dir, supervisor, tool string, args ...string) (*mediaProcess, error) {
	ctx, cancel := context.WithCancel(ctx)
	command, arguments := tool, args
	if supervisor != "" {
		command = supervisor
		arguments = append([]string{"__sharing-media", filepath.Base(tool)}, args...)
	}
	cmd := exec.CommandContext(ctx, command, arguments...)
	var lifetime io.WriteCloser
	if supervisor != "" {
		var err error
		lifetime, err = cmd.StdinPipe()
		if err != nil {
			cancel()
			return nil, fault(api.ShareMediaUnreachable)
		}
		cmd.Cancel = lifetime.Close
		cmd.WaitDelay = 1800 * time.Millisecond
	}
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, io.Discard, io.Discard
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	if err := cmd.Start(); err != nil {
		cancel()
		if lifetime != nil {
			_ = lifetime.Close()
		}
		return nil, fault(api.ShareMediaUnreachable)
	}
	p := &mediaProcess{cancel: cancel, done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(p.done) }()
	return p, nil
}

// SuperviseMediaProcess ties a known bundled runtime tool to the helper's
// lifetime pipe, including when the helper disappears without cleanup.
func SuperviseMediaProcess(ctx context.Context, lifetime io.Reader, tool string, args []string) error {
	dir, err := os.Getwd()
	if err != nil {
		return fault(api.ShareMediaUnreachable)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, lifetime); cancel() }()
	filename := "mediamtx.json"
	if filepath.Base(tool) == "ffmpeg" {
		filename = "camera.ffconcat"
	}
	defer func() {
		if strings.HasPrefix(filepath.Base(dir), "camstream-sharing-") {
			_ = os.Remove(filepath.Join(dir, filename))
			_ = os.Remove(dir)
		}
	}()
	worker, err := runMediaProcess(ctx, dir, tool, args...)
	if err != nil {
		return err
	}
	<-worker.done
	return nil
}

func (p *mediaProcess) stop() {
	if p == nil {
		return
	}
	p.cancel()
	<-p.done
}

func reserveTCP(ctx context.Context) (string, error) {
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	return l.Addr().String(), l.Close()
}

func privateSource(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "rtsp" || u.RawQuery != "" || u.Fragment != "" || u.Path == "" || strings.ContainsAny(raw, "'\r\n") {
		return nil, false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return u, err == nil && ip.IsLoopback() && u.Port() != ""
}

func mediaDefaults() map[string]any {
	return map[string]any{"logLevel": "error", "rtsp": false, "rtmp": false, "hls": false, "srt": false, "moq": false,
		"webrtc": false, "api": false, "metrics": false, "pprof": false, "playback": false, "authMethod": "internal"}
}

type mediaAuth struct {
	User        string              `json:"user"`
	Pass        string              `json:"pass,omitempty"`
	IPs         []string            `json:"ips"`
	Permissions []map[string]string `json:"permissions"`
}

func mediaUser(user, pass, action, path string) mediaAuth {
	perm := map[string]string{"action": action}
	if path != "" {
		perm["path"] = path
	}
	return mediaAuth{User: user, Pass: pass, IPs: []string{"127.0.0.1"}, Permissions: []map[string]string{perm}}
}

func (m *MediaRuntime) startServer(dir string, settings map[string]any) (*mediaProcess, error) {
	data, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	name := filepath.Join(dir, "mediamtx.json")
	if err = os.WriteFile(name, data, 0600); err != nil {
		return nil, err
	}
	return launchMediaProcess(m.ctx, dir, m.opts.Supervisor, m.opts.MediaMTX, name)
}

func (m *MediaRuntime) source(ctx context.Context, o MediaOffer) (*runtimeSource, error) {
	u, valid := privateSource(o.SourceID)
	if !valid {
		return nil, fault(api.ShareMediaUnreachable)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.shares[o.Attempt.ShareID]; existing != nil {
		existing.users++
		return existing, nil
	}
	dir, err := os.MkdirTemp("", "camstream-sharing-source-")
	if err != nil {
		return nil, fault(api.ShareMediaUnreachable)
	}
	source := &runtimeSource{dir: dir, token: handle(), users: 1}
	inited := false
	defer func() {
		if !inited {
			source.stop()
		}
	}()
	source.address, err = reserveTCP(ctx)
	if err != nil {
		return nil, fault(api.ShareMediaUnreachable)
	}

	settings := mediaDefaults()
	settings["rtsp"], settings["rtspAddress"], settings["rtspTransports"] = true, source.address, []string{"tcp"}
	settings["rtspAuthMethods"] = []string{"basic"}
	settings["paths"] = map[string]any{source.token: map[string]any{"source": "publisher", "overridePublisher": false}}
	settings["authInternalUsers"] = []mediaAuth{mediaUser("any", "", "publish", source.token), mediaUser("any", "", "read", source.token)}
	source.server, err = m.startServer(dir, settings)
	if err != nil {
		return nil, fault(api.ShareMediaUnreachable)
	}
	// Source credentials remain in a private file and never enter arguments.
	input := filepath.Join(dir, "camera.ffconcat")
	list := "ffconcat version 1.0\nfile '" + u.String() + "'\noption rtsp_transport tcp\noption rw_timeout 5000000\n"
	if err = os.WriteFile(input, []byte(list), 0600); err != nil {
		return nil, fault(api.ShareMediaUnreachable)
	}
	source.encoder, err = launchMediaProcess(m.ctx, dir, m.opts.Supervisor, m.opts.FFmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", "file,tcp,rtsp,rtp,udp", "-f", "concat", "-safe", "0", "-i", input,
		"-map", "0:v:0", "-an", "-sn", "-dn", "-vf", "scale=w='min(1280,iw)':h='min(720,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2,fps=30",
		"-c:v", "libx264", "-profile:v", "baseline", "-pix_fmt", "yuv420p", "-preset", "ultrafast", "-tune", "zerolatency",
		"-threads", "2", "-b:v", "2000k", "-maxrate", "2000k", "-bufsize", "4000k", "-bf", "0", "-g", "30",
		"-f", "rtsp", "-rtsp_transport", "tcp", source.url())
	if err != nil {
		return nil, fault(api.ShareMediaUnreachable)
	}
	m.shares[o.Attempt.ShareID] = source
	inited = true
	return source, nil
}

func (s *runtimeSource) url() string { return "rtsp://" + s.address + "/" + s.token }
func (s *runtimeSource) stop() {
	s.encoder.stop()
	s.server.stop()
	_ = os.RemoveAll(s.dir)
}

func (m *MediaRuntime) release(id string, source *runtimeSource) {
	m.mu.Lock()
	source.users--
	stop := source.users == 0
	if stop {
		delete(m.shares, id)
	}
	m.mu.Unlock()
	if stop {
		source.stop()
	}
}

func (m *MediaRuntime) newPeer(ctx context.Context, o MediaOffer, ice []api.ShareICECredentials) (*runtimePeer, error) {
	source, err := m.source(ctx, o)
	if err != nil {
		return nil, err
	}
	peer := &runtimePeer{source: source, scope: o.Attempt, token: handle()}
	success := false
	defer func() {
		if !success {
			m.stop(peer)
		}
	}()
	peer.dir, err = os.MkdirTemp("", "camstream-sharing-peer-")
	if err != nil {
		return nil, err
	}
	peer.address, err = reserveTCP(ctx)
	if err != nil {
		return nil, err
	}
	control, err := reserveTCP(ctx)
	if err != nil {
		return nil, err
	}
	settings := mediaDefaults()
	settings["webrtc"], settings["webrtcAddress"], settings["webrtcLocalTCPAddress"] = true, peer.address, ""
	settings["api"], settings["apiAddress"] = true, control
	settings["webrtcLocalUDPAddress"] = ":0"
	if m.opts.LocalOnly {
		settings["webrtcLocalUDPAddress"] = "127.0.0.1:0"
		settings["webrtcIPsFromInterfaces"] = false
		settings["webrtcAdditionalHosts"] = []string{"127.0.0.1"}
	}
	var servers []map[string]any
	for _, relay := range ice {
		for _, address := range relay.URLs {
			servers = append(servers, map[string]any{"url": address, "username": relay.Username, "password": relay.Credential})
		}
	}
	if len(servers) > 0 {
		settings["webrtcICEServers2"] = servers
	}
	settings["paths"] = map[string]any{"video": map[string]any{"source": source.url(), "rtspTransport": "tcp", "sourceOnDemand": true, "sourceOnDemandStartTimeout": "8s", "maxReaders": 1}}
	settings["authInternalUsers"] = []mediaAuth{mediaUser("gateway", peer.token, "read", "video"), mediaUser("gateway", peer.token, "api", "")}
	peer.worker, err = m.startServer(peer.dir, settings)
	if err != nil {
		return nil, err
	}
	peer.id = control
	if err = m.waitReady(ctx, peer); err != nil {
		return nil, err
	}
	success = true
	return peer, nil
}
