// Package conversationhost composes the owner-controlled conversation listener.
package conversationhost

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/conversations"
	"github.com/goliatone/go-cam-stream/internal/transport/http/conversationguest"
)

// Start errors keep the projected room fault and add a stage, so the owner can
// report a sanitized setup result without exposing the underlying cause.
var (
	ErrSettings          = errors.New("conversation host settings rejected")
	ErrUnavailable       = errors.New("conversation host runtime unavailable")
	ErrRouteUnreachable  = errors.New("public conversation route could not be verified")
	ErrListenerClosed    = errors.New("conversation listener closed")
	ErrAssetsUnavailable = errors.New("bundled call page unavailable")
	ErrRelayUnavailable  = errors.New("call relay could not be verified")
)

const listenerGrace = 30 * time.Second

type ListenerInUse struct{ Port uint16 }

func (ListenerInUse) Error() string { return "conversation listener port is in use" }

type Runtime struct {
	state     *conversations.Service
	bus       *conversations.Commands
	http      *http.Server
	origin    string
	route     *routeVerification
	relay     conversations.RelayIssuer
	close     sync.Once
	done      chan struct{}
	hosting   sync.Mutex
	idleSince time.Time
}

func Start(ctx context.Context, settings config.Conversations, relay conversations.RelayIssuer, files fs.FS) (*Runtime, error) {
	if settings.Validate() != nil {
		return nil, errors.Join(api.ErrInvalidConversationRequest, ErrSettings)
	}
	address, origin := settings.Ingress.GuestListen, ""
	if settings.Loopback {
		address = "127.0.0.1:0"
	} else {
		var err error
		origin, err = settings.Ingress.PublicOrigin()
		if err != nil {
			return nil, errors.Join(conversations.Fault(api.RoomMediaUnreachable), ErrSettings)
		}
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) && !settings.Loopback {
			return nil, ListenerInUse{Port: netip.MustParseAddrPort(address).Port()}
		}
		return nil, errors.Join(conversations.Fault(api.RoomMediaUnreachable), ErrUnavailable)
	}
	if settings.Loopback {
		origin = "http://" + listener.Addr().String()
	}
	service, err := conversations.NewService(conversations.Options{
		PublicOrigin: origin, Loopback: settings.Loopback, RelayOnly: settings.RelayOnly, Relay: relay,
	})
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	bus := conversations.NewCommands(service)
	handler, err := conversationguest.NewSite(origin, settings.Loopback, files, service, bus)
	if err != nil {
		bus.Close()
		service.Close()
		_ = listener.Close()
		// The bundled call page is missing or unusable in this build.
		return nil, errors.Join(err, ErrAssetsUnavailable)
	}
	var route *routeVerification
	if !settings.Loopback {
		route = newRouteVerification(origin)
		handler = route.wrap(handler)
	}
	server := &http.Server{
		Handler: handler, BaseContext: func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 6 * time.Second, WriteTimeout: 15 * time.Second,
		IdleTimeout: 20 * time.Second, MaxHeaderBytes: 32 * 1024, ErrorLog: log.New(io.Discard, "", 0),
	}
	runtime := &Runtime{http: server, state: service, bus: bus, origin: origin, route: route, relay: relay, done: make(chan struct{})}
	go func() {
		_ = server.Serve(listener)
		runtime.Close()
	}()
	go runtime.watchIdle(ctx)
	return runtime, nil
}

func (r *Runtime) Create(ctx context.Context, progress func(api.ConversationSetupStage)) (api.NativeConversationPrejoin, error) {
	r.hosting.Lock()
	defer r.hosting.Unlock()
	if r.Closed() {
		return api.NativeConversationPrejoin{}, ErrListenerClosed
	}
	if r.route != nil {
		if progress != nil {
			progress(api.ConversationSetupRouteStage)
		}
		if err := r.CheckRoute(ctx); err != nil {
			return api.NativeConversationPrejoin{}, err
		}
	}
	if relay, ok := r.relay.(interface{ Probe(context.Context) error }); ok {
		if progress != nil {
			progress(api.ConversationSetupRelayStage)
		}
		if err := relay.Probe(ctx); err != nil {
			return api.NativeConversationPrejoin{}, ErrRelayUnavailable
		}
	}
	created, err := conversations.Dispatch[api.CreatedRoom](conversations.WithOwner(ctx), r.bus, conversations.CreateRoom{})
	if err != nil {
		return api.NativeConversationPrejoin{}, err
	}
	r.idleSince = time.Time{}
	return api.NativeConversationPrejoin{
		PageURL: r.origin + "/r/" + created.Admission.Room.RoomID, Admission: created.Admission, InvitationURL: created.InvitationURL,
	}, nil
}

func (r *Runtime) CheckRoute(ctx context.Context) error {
	if r.route != nil && !r.route.reachable(ctx) {
		return ErrRouteUnreachable
	}
	return nil
}

func (r *Runtime) watchIdle(ctx context.Context) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			r.Close()
			return
		case <-r.done:
			return
		case <-tick.C:
			r.reapIdle(time.Now(), listenerGrace)
		}
	}
}

func (r *Runtime) reapIdle(now time.Time, grace time.Duration) {
	r.hosting.Lock()
	defer r.hosting.Unlock()
	if r.Closed() || r.state.HasLiveRooms() {
		return
	}
	if r.idleSince.IsZero() {
		r.idleSince = now
	} else if !now.Before(r.idleSince.Add(grace)) {
		r.Close()
	}
}

func (r *Runtime) Closed() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

func (r *Runtime) Status(ctx context.Context, room api.RoomRef) (api.RoomView, error) {
	return r.state.OwnerStatus(conversations.WithOwner(ctx), room)
}

func (r *Runtime) End(ctx context.Context, room api.RoomRef) (api.RoomView, error) {
	return conversations.Dispatch[api.RoomView](conversations.WithOwner(ctx), r.bus, conversations.EndRoom{RoomRef: room})
}

func (r *Runtime) Close() {
	if r == nil {
		return
	}
	r.close.Do(func() {
		r.state.Close()
		_ = r.http.Close()
		r.bus.Close()
		close(r.done)
	})
}
