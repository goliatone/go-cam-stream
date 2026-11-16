//go:build camstream_dev

package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/goliatone/go-cam-stream/data/api"
)

func TestDevelopmentBrowserAccess(t *testing.T) {
	cfg := testConfig(t)
	c, err := NewDevelopment(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	_, port, portErr := net.SplitHostPort(cfg.Listen)
	if portErr != nil {
		t.Fatal(portErr)
	}
	request := func(method, path, host, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(context.Background(), method, "http://"+host+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Origin", "http://"+host)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		// Neither spoofed nor duplicate client identities can change dev grants.
		if path == "/api/cameras" {
			r.Header.Add(api.ServeLoginHeader, "alice")
			r.Header.Add(api.ServeLoginHeader, "unknown")
		}
		if token != "" {
			r.Header.Set(api.CSRFHeader, token)
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		c.server.Handler.ServeHTTP(w, r)
		return w
	}
	for _, host := range []string{cfg.Listen, net.JoinHostPort("localhost", port)} {
		if w := request("GET", "/", host, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "/assets/") {
			t.Fatal("embedded page", w.Code, w.Body.String())
		}
		w := request("GET", "/api/cameras", host, "", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "room2") || !strings.Contains(w.Body.String(), "hall") {
			t.Fatal("full development catalog", w.Code, w.Body.String())
		}
		w = request("GET", api.CSRFPath, host, "", "")
		var csrf api.CSRFResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &csrf) != nil || csrf.Token == "" {
			t.Fatal("CSRF bootstrap", w.Code, w.Body.String())
		}
		path := "/api/cameras/room1/diagnose"
		if w = request("POST", path, host, "", "{"); w.Code != 403 {
			t.Fatal("mutation without CSRF", w.Code, w.Body.String())
		}
		if w = request("POST", path, host, csrf.Token, "{"); w.Code != 400 {
			t.Fatal("valid CSRF reaches normal request validation", w.Code, w.Body.String())
		}
	}
}

func TestDevelopmentIngressRejectsForeignRequests(t *testing.T) {
	cfg := testConfig(t)
	for name, change := range map[string]func(*http.Request){
		"remote peer":    func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1234" },
		"malformed peer": func(r *http.Request) { r.RemoteAddr = "127.0.0.1" },
		"forwarded peer": func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1234"; r.Header.Set("X-Forwarded-For", "127.0.0.1") },
		"foreign host":   func(r *http.Request) { r.Host = "evil.example" },
		"foreign port":   func(r *http.Request) { r.Host = "localhost:1" },
		"foreign origin": func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") },
		"null origin":    func(r *http.Request) { r.Header.Set("Origin", "null") },
		"duplicate origin": func(r *http.Request) {
			r.Header.Add("Origin", "http://"+cfg.Listen)
			r.Header.Add("Origin", "http://"+cfg.Listen)
		},
		"cross-site browser":     func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"same-site other origin": func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") },
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), "GET", "http://"+cfg.Listen+"/", nil)
			r.RemoteAddr = "127.0.0.1:1234"
			change(r)
			w := httptest.NewRecorder()
			developmentIngress(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request reached application") }), cfg).ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal(w.Code)
			}
		})
	}
	cfg.Listen = "0.0.0.0:8081"
	if c, err := NewDevelopment(cfg, nil); err == nil || c != nil {
		t.Fatal("non-loopback config admitted")
	}
}

func TestDevelopmentDoesNotChangeProductionConstructor(t *testing.T) {
	cfg := testConfig(t)
	c, err := NewDefault(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	u, parseErr := url.Parse(cfg.Origin)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	for _, host := range []string{cfg.Listen, u.Host} {
		r := httptest.NewRequestWithContext(context.Background(), "GET", "http://"+host+"/", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		c.server.Handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("production allowed anonymous access", w.Code)
		}
	}
}
