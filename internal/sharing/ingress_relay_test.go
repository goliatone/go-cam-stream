package sharing

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/pion/turn/v5"
)

func TestExternalIngressViaTLSProxy(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := backend.Addr().String()
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse("http://" + address)
	if err != nil {
		t.Fatal(err)
	}
	reverse := httputil.NewSingleHostReverseProxy(target)
	var routeDown atomic.Bool
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if routeDown.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		reverse.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	disabled, err := NewExternalIngress(config.SharingIngress{}, handler, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = disabled.Start(t.Context()); err == nil {
		t.Fatal("disabled ingress started")
	}
	ingress, err := NewExternalIngress(config.SharingIngress{Mode: config.SharingIngressExternal, PublicBaseURL: proxy.URL, GuestListen: address}, handler, proxy.Client())
	if err != nil {
		t.Fatal(err)
	}
	binding, err := ingress.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ingress.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if binding.Origin != proxy.URL {
		t.Fatal("unexpected HTTPS binding")
	}
	for _, tc := range []struct {
		method, path, origin string
		status               int
	}{
		{http.MethodGet, "/s/" + handle(), "", 204},
		{http.MethodPost, "/share-api/v1/shares/" + handle() + "/redeem", proxy.URL, 204},
		{http.MethodPost, "/share-api/v1/shares/" + handle() + "/redeem", "https://elsewhere.example.test", 403},
		{http.MethodGet, "/api/cameras", "", 404},
	} {
		req, err := http.NewRequestWithContext(t.Context(), tc.method, proxy.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", tc.origin)
		response, err := proxy.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != tc.status {
			t.Fatalf("status %d want %d", response.StatusCode, tc.status)
		}
	}
	service, _ := newSharingTest(t, &testMedia{})
	service.opts.Ingress = ingress
	owner := WithOperator(t.Context())
	request := CreateShare{CreateShareRequest: api.CreateShareRequest{CameraID: "camera"}}
	first, err := service.Create(owner, request)
	if err != nil {
		t.Fatal(err)
	}
	routeDown.Store(true)
	service.checkIngress(t.Context())
	if view, err := service.Status(owner, "camera"); err != nil || view.State != api.ShareDegraded {
		t.Fatalf("unreachable route projected as %s: %v", view.State, err)
	}
	routeDown.Store(false)
	service.checkIngress(t.Context())
	if view, err := service.Status(owner, "camera"); err != nil || view.State != api.ShareAvailable {
		t.Fatalf("recovered route projected as %s: %v", view.State, err)
	}
	if _, err := service.Revoke(owner, RevokeShare{ShareRef: api.ShareRef{ShareID: first.Share.ShareID}}); err != nil {
		t.Fatal(err)
	}
	routeDown.Store(true)
	if _, err := service.Create(owner, request); err != fault(api.ShareIngressUnavailable) {
		t.Fatalf("issued a fresh link through an unreachable proxy: %v", err)
	}
	routeDown.Store(false)
	if fresh, err := service.Create(owner, request); err != nil || fresh.Share.ShareID == first.Share.ShareID {
		t.Fatalf("route recovery did not produce a fresh invitation: %v", err)
	}
	if err := ingress.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	check, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("guest listener remained bound", err)
	}
	if err = check.Close(); err != nil {
		t.Fatal(err)
	}
}

func testGrant() RelayGrant {
	return RelayGrant{Attempt: ViewerAttempt{ShareID: handle(), ViewerID: handle(), AttemptID: handle()}, NotAfter: time.Now().Add(time.Minute)}
}

func TestCloudflareRelayUsesSeparateBoundedGrants(t *testing.T) {
	var issued atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TTL int `json:"ttl"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/controlled-key/credentials/generate-ice-servers" || r.Header.Get("Authorization") != "Bearer fixture-token" ||
			json.NewDecoder(r.Body).Decode(&req) != nil || req.TTL < 1 || req.TTL > 56 {
			w.WriteHeader(400)
			return
		}
		username := "host"
		if issued.Add(1) == 2 {
			username = "guest"
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"iceServers": []api.ShareICECredentials{
			{URLs: []string{"stun:stun.example.test"}},
			{URLs: []string{"turns:relay.example.test:443?transport=tcp"}, Username: username, Credential: "opaque-fixture"},
		}})
	}))
	defer provider.Close()
	issuer, err := NewCloudflareRelay("controlled-key", "fixture-token", provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	issuer.base = provider.URL + "/"
	grant := testGrant()
	credentials, err := issuer.Issue(t.Context(), grant)
	if err != nil {
		t.Fatal(err)
	}
	host, guest, err := credentials.ForAttempt(grant, time.Now())
	if err != nil || guest[0].Username != "guest" || issued.Load() != 2 {
		t.Fatal("guest grant missing")
	}
	ice, err := (MediaOffer{Attempt: grant.Attempt, Relay: &host}).HostICE(time.Now())
	if err != nil || ice[0].Username != "host" {
		t.Fatal("host grant missing")
	}
}

func TestCoturnControlledAllocation(t *testing.T) {
	address := os.Getenv("CAMSTREAM_TEST_COTURN")
	if address == "" {
		t.Skip("set CAMSTREAM_TEST_COTURN to a disposable coturn address")
	}
	issuer, err := NewCoturnRelay([]string{"turn:" + address}, "camstream-local-fixture")
	if err != nil {
		t.Fatal(err)
	}
	grant := testGrant()
	credentials, err := issuer.Issue(t.Context(), grant)
	if err != nil {
		t.Fatal(err)
	}
	host, guest, err := credentials.ForAttempt(grant, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	hostIce, err := (MediaOffer{Attempt: grant.Attempt, Relay: &host}).HostICE(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, ice := range []api.ShareICECredentials{guest[0], hostIce[0]} {
		t.Run(ice.Username[len(ice.Username)-4:], func(t *testing.T) {
			conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close() //nolint:errcheck // Disposable socket.
			client, err := turn.NewClient(&turn.ClientConfig{TURNServerAddr: address, Conn: conn, Username: ice.Username, Password: ice.Credential, Realm: "camstream-test"})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if err := client.Listen(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
			defer cancel()
			allocation, err := client.AllocateWithContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := allocation.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
