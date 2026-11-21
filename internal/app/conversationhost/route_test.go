package conversationhost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/conversations"
)

func TestManagedRouteWaitsForEdgeAfterConnectorRegisters(t *testing.T) {
	server := httptest.NewUnstartedServer(nil)
	route := newRouteVerification("https://" + server.Listener.Addr().String())
	var requests atomic.Int32
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		route.wrap(http.NotFoundHandler()).ServeHTTP(w, r)
	})
	server.StartTLS()
	t.Cleanup(server.Close)
	route.client.Transport = server.Client().Transport
	runtime := Runtime{route: route, connector: &Connector{}}
	if err := runtime.CheckRoute(t.Context()); err != nil || requests.Load() != 2 {
		t.Fatalf("route did not recover after edge registration: %v", err)
	}
}

type failedRelay struct{}

func (failedRelay) Probe(context.Context) error { return errors.New("unreachable") }

func (failedRelay) Issue(context.Context, api.RoomRef, api.RoomGeneration, time.Time) (conversations.RoomRelay, error) {
	panic("no room should use a relay that failed the setup check")
}

func TestRelaySetupFailureStopsBeforeInvitation(t *testing.T) {
	runtime := Runtime{relay: failedRelay{}}
	var stages []api.ConversationSetupStage
	room, err := runtime.Create(t.Context(), func(stage api.ConversationSetupStage) { stages = append(stages, stage) })
	if !errors.Is(err, ErrRelayUnavailable) || room.InvitationURL != "" ||
		!slices.Equal(stages, []api.ConversationSetupStage{api.ConversationSetupRelayStage}) {
		t.Fatalf("relay failure: err=%v stages=%v", err, stages)
	}
}

func TestConversationRouteVerification(t *testing.T) {
	for _, mode := range []string{"matching listener", "HTML from another listener", "wrong proof", "cached proof", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewUnstartedServer(nil)
			route := newRouteVerification("https://" + server.Listener.Addr().String())
			server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case mode == "matching listener" || r.URL.Query().Has("redirected"):
					route.wrap(http.NotFoundHandler()).ServeHTTP(w, r)
				case mode == "HTML from another listener":
					w.WriteHeader(http.StatusOK)
				case mode == "wrong proof":
					w.Header().Set(routeProofHeader, "another listener")
					w.WriteHeader(http.StatusNoContent)
				case mode == "cached proof":
					w.Header().Set(routeProofHeader, route.proof("old nonce"))
					w.WriteHeader(http.StatusNoContent)
				case mode == "redirect":
					http.Redirect(w, r, r.URL.RequestURI()+"&redirected=1", http.StatusFound)
				}
			})
			server.StartTLS()
			t.Cleanup(server.Close)
			route.client.Transport = server.Client().Transport
			if mode == "matching listener" {
				if !route.reachable(t.Context()) {
					t.Fatal("valid listener proof was rejected")
				}
				return
			}
			// The room bus is absent. A failed check must stop before creating a room.
			runtime := Runtime{route: route}
			prejoin, err := runtime.Create(t.Context(), nil)
			if !errors.Is(err, ErrRouteUnreachable) || prejoin.InvitationURL != "" {
				t.Fatalf("unverified route returned an invitation or the wrong fault: %v", err)
			}
		})
	}
}
