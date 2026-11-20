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
	"sync"
	"time"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/conversations"
	"github.com/goliatone/go-cam-stream/internal/transport/http/conversationguest"
)

// Start errors keep the projected room fault and add a stage, so the owner can
// report a sanitized setup result without exposing the underlying cause.
var (
	ErrSettings    = errors.New("conversation host settings rejected")
	ErrUnavailable = errors.New("conversation host runtime unavailable")
)

type Runtime struct {
	state  *conversations.Service
	bus    *conversations.Commands
	http   *http.Server
	origin string
	close  sync.Once
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
		return nil, errors.Join(err, ErrUnavailable)
	}
	server := &http.Server{
		Handler: handler, BaseContext: func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 6 * time.Second, WriteTimeout: 15 * time.Second,
		IdleTimeout: 20 * time.Second, MaxHeaderBytes: 32 * 1024, ErrorLog: log.New(io.Discard, "", 0),
	}
	runtime := &Runtime{http: server, state: service, bus: bus, origin: origin}
	go func() {
		_ = server.Serve(listener)
		runtime.Close()
	}()
	return runtime, nil
}

func (r *Runtime) Create(ctx context.Context) (api.NativeConversationPrejoin, error) {
	created, err := conversations.Dispatch[api.CreatedRoom](conversations.WithOwner(ctx), r.bus, conversations.CreateRoom{})
	if err != nil {
		return api.NativeConversationPrejoin{}, err
	}
	return api.NativeConversationPrejoin{
		PageURL: r.origin + "/r/" + created.Admission.Room.RoomID, Admission: created.Admission, InvitationURL: created.InvitationURL,
	}, nil
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
	})
}
