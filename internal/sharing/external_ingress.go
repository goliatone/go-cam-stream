package sharing

import (
	"context"
	"crypto/subtle"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
)

const ingressProbeHeader = "X-Camstream-Sharing-Probe"

// ExternalIngress owns its loopback HTTP listener. The operator's TLS endpoint
// must forward the guest routes and preserve the public Host.
type ExternalIngress struct {
	mu      sync.Mutex
	cfg     config.SharingIngress
	handler http.Handler
	client  *http.Client
	server  *http.Server
	binding IngressBinding
}

func NewExternalIngress(cfg config.SharingIngress, handler http.Handler, client *http.Client) (*ExternalIngress, error) {
	settings := (config.Sharing{Ingress: cfg}).WithDefaults()
	if settings.Validate() != nil || handler == nil {
		return nil, fault(api.ShareInvalidRequest)
	}
	if client == nil {
		client = http.DefaultClient
	}
	privateClient := *client
	privateClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ExternalIngress{cfg: settings.Ingress, handler: handler, client: &privateClient}, nil
}

func (i *ExternalIngress) Start(ctx context.Context) (IngressBinding, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.server != nil {
		return i.binding, nil
	}
	origin, err := i.cfg.PublicOrigin()
	if err != nil {
		return IngressBinding{}, fault(api.ShareIngressUnavailable)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return IngressBinding{}, fault(api.ShareIngressUnavailable)
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", i.cfg.GuestListen)
	if err != nil {
		return IngressBinding{}, fault(api.ShareIngressUnavailable)
	}

	probePath, challenge := "/s/"+handle(), handle()
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if r.Host != u.Host || (r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") != origin) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if r.Method == http.MethodGet && r.URL.Path == probePath && subtle.ConstantTimeCompare([]byte(r.Header.Get(ingressProbeHeader)), []byte(challenge)) == 1 {
				w.Header().Set(ingressProbeHeader, challenge)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.URL.RawPath != "" || path.Clean(r.URL.Path) != r.URL.Path ||
				!(strings.HasPrefix(r.URL.Path, "/s/") || strings.HasPrefix(r.URL.Path, api.ShareAPIPrefix) || strings.HasPrefix(r.URL.Path, api.ShareAssetPrefix)) {
				http.NotFound(w, r)
				return
			}
			i.handler.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 6 * time.Second, WriteTimeout: 24 * time.Second,
		IdleTimeout: 20 * time.Second, MaxHeaderBytes: 16 * 1024, ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() { _ = server.Serve(listener) }()
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if !i.probe(probeCtx, origin+probePath, challenge) {
		_ = server.Close()
		return IngressBinding{}, fault(api.ShareIngressUnavailable)
	}
	i.server = server
	i.binding = IngressBinding{Origin: origin, Audience: api.SharePublicAudience}
	return i.binding, nil
}

func (i *ExternalIngress) probe(ctx context.Context, target, challenge string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	req.Header.Set(ingressProbeHeader, challenge)
	res, err := i.client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close() //nolint:errcheck // No response body is consumed.
	return res.StatusCode == http.StatusNoContent && subtle.ConstantTimeCompare([]byte(res.Header.Get(ingressProbeHeader)), []byte(challenge)) == 1
}

func (i *ExternalIngress) Stop(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.server == nil {
		return nil
	}
	server := i.server
	i.server, i.binding = nil, IngressBinding{}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		if err = server.Close(); err != nil {
			return fault(api.ShareIngressUnavailable)
		}
	}
	return nil
}
