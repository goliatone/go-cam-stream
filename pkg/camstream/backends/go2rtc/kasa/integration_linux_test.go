//go:build linux

package kasa_test

import (
	"context"
	"crypto/md5" // The pinned Kasa Digest protocol uses MD5; test verifier only.
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/kasa"
	pion "github.com/pion/webrtc/v4"
)

// This proves real encoder -> backend -> stock go2rtc HTTPS/auth behavior, not
// EC70 media/firmware support. The synthetic camera deliberately supplies no media.
func TestPinnedKasaAuthentication(t *testing.T) {
	if os.Getenv("CAMSTREAM_KASA_INTEGRATION") != "1" {
		t.Skip("requires pinned Linux arm64 executable in an --init container")
	}
	const username = "alex+tag%#@example.test"
	const password = "\xfb\xffpassword:@#%/日本語"
	var basic, digest, bad atomic.Uint32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/https/stream/mixed" {
			bad.Add(1)
		}
		user, pass, ok := r.BasicAuth()
		if ok {
			if user == username && pass == base64.StdEncoding.EncodeToString([]byte(password)) {
				basic.Add(1)
			} else {
				bad.Add(1)
			}
			w.Header().Set("WWW-Authenticate", `Digest realm="fixture", nonce="fixture-nonce"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		ha1 := md5hex(username + ":fixture:" + base64.StdEncoding.EncodeToString([]byte(password)))
		ha2 := md5hex("GET:/https/stream/mixed")
		want := md5hex(ha1 + ":fixture-nonce:" + ha2)
		header := r.Header.Get("Authorization")
		if strings.HasPrefix(header, "Digest ") && strings.Contains(header, `username="`+username+`"`) && strings.Contains(header, `response="`+want+`"`) {
			digest.Add(1)
		} else {
			bad.Add(1)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	b, err := go2rtc.New(go2rtc.Config{Executable: "/usr/local/bin/go2rtc", RuntimeDir: filepath.Join(t.TempDir(), "runtime"),
		PrivatePorts: go2rtc.PortRange{First: 35000, Last: 35099}, MediaPorts: go2rtc.PortRange{First: 35100, Last: 35199},
		MediaAddress: "127.0.0.1", CandidateAddress: "127.0.0.1", VerifyBoundary: go2rtc.ContainerBoundary,
		StartupTimeout: 3 * time.Second, FailureTimeout: time.Second, CleanupTimeout: 2 * time.Second, TermGrace: 100 * time.Millisecond, PollInterval: 100 * time.Millisecond,
	}, kasa.NewEncoder(values(username, password, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = b.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if closeErr := b.Close(cleanup); closeErr != nil {
			t.Error(closeErr)
		}
	})
	s := spec(t, u.Hostname(), uint16(port))
	if _, err = b.EnsureStream(ctx, s); err != nil {
		t.Fatal(err)
	}
	_, err = b.CreateSession(ctx, camstream.SessionSpec{ID: "kasa-fixture", SourceKey: s.Key, Offer: receiveOffer(t)})
	if err == nil {
		t.Fatal("no-media fixture unexpectedly negotiated")
	}
	if basic.Load() == 0 || digest.Load() == 0 || bad.Load() != 0 {
		t.Fatalf("Basic=%d Digest=%d bad=%d", basic.Load(), digest.Load(), bad.Load())
	}
	if err = b.CloseSession(ctx, "kasa-fixture"); err != nil {
		t.Fatal(err)
	}
	t.Logf("pinned Kasa HTTPS path and Basic/Digest credentials verified; Basic=%d Digest=%d bad=%d; no hardware/media claim", basic.Load(), digest.Load(), bad.Load())
}

func md5hex(value string) string {
	digest := md5.Sum([]byte(value))
	return hex.EncodeToString(digest[:])
}

func receiveOffer(t *testing.T) camstream.Offer {
	t.Helper()
	engine := pion.SettingEngine{}
	engine.SetIncludeLoopbackCandidate(true)
	engine.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	pc, err := pion.NewAPI(pion.WithSettingEngine(engine)).NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := pc.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if _, err = pc.AddTransceiverFromKind(pion.RTPCodecTypeVideo, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	done := pion.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ICE gather timeout")
	}
	return camstream.Offer{SDP: pc.LocalDescription().SDP}
}
