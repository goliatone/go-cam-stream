//go:build camstream_dev

package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"

	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

const developmentLogin = "local-developer"

// NewDevelopment is available only in explicitly tagged development builds.
// It grants local browser requests access to the configured catalog, without
// weakening production containment or application CSRF requirements.
type DevelopmentOptions struct{ Go2RTC, FFmpeg, WebcamDevice string }

func NewDevelopment(cfg config.Config, logger admin.Logger, options ...DevelopmentOptions) (*Controller, error) {
	cfg.Management.Enabled = true
	cfg.Management.Principals = []string{developmentLogin}
	cfg.Access = nil
	catalog, err := config.Prepare(context.Background(), cfg, config.DefaultProviders()...)
	if err != nil {
		return nil, safe(err)
	}
	access := config.Access{Login: developmentLogin}
	for _, camera := range catalog.Cameras {
		streams := make([]cs.StreamID, 0, len(camera.Streams))
		for _, stream := range camera.Streams {
			streams = append(streams, stream.ID)
		}
		access.Grants = append(access.Grants, config.Grant{CameraID: camera.Camera.ID, Streams: streams, View: true, Diagnose: true, Reconnect: true})
	}
	cfg.Access = []config.Access{access}
	var opts DevelopmentOptions
	if len(options) > 0 {
		opts = options[0]
	}
	controller, err := newDevelopmentController(cfg, logger, opts)
	if err != nil {
		return nil, err
	}
	controller.server.Handler = developmentIngress(controller.server.Handler, cfg)
	controller.onListen = func(err error) {
		if logger == nil {
			return
		}
		switch {
		case errors.Is(err, syscall.EADDRINUSE):
			logger.Error(fmt.Sprintf("Cannot start development server: %s is already in use. Choose another port (./taskfile dev:serve 8087) or stop the process using it.", cfg.Listen))
		case err != nil:
			logger.Error(fmt.Sprintf("Cannot bind development server to %s: %v", cfg.Listen, err))
		default:
			logger.Info(fmt.Sprintf("Development server: http://%s (local access without login)", cfg.Listen))
		}
	}
	return controller, nil
}

// Validate the browser-facing boundary before adapting the request to the
// ordinary authenticated ingress. Never accept a caller-selected principal.
func developmentIngress(next http.Handler, cfg config.Config) http.Handler {
	_, port, portErr := net.SplitHostPort(cfg.Listen)
	localhost := net.JoinHostPort("localhost", port)
	origin, originErr := url.Parse(cfg.Origin)
	valid := portErr == nil && originErr == nil && origin != nil
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		allowed := valid && err == nil && ip != nil && ip.IsLoopback() && (r.Host == cfg.Listen || r.Host == localhost)
		if !allowed || !developmentBrowserHeaders(r) {
			http.Error(w, "local development access denied", http.StatusForbidden)
			return
		}
		local := r.Clone(r.Context())
		local.Host = origin.Host
		local.Header.Set(api.ServeLoginHeader, developmentLogin)
		if len(r.Header.Values("Origin")) == 1 {
			local.Header.Set("Origin", cfg.Origin)
		}
		next.ServeHTTP(w, local)
	})
}

func developmentBrowserHeaders(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	sites := r.Header.Values("Sec-Fetch-Site")
	originOK := len(origins) == 0 || (len(origins) == 1 && origins[0] == "http://"+r.Host)
	siteOK := len(sites) == 0 || (len(sites) == 1 && (sites[0] == "same-origin" || sites[0] == "none"))
	return originOK && siteOK
}
