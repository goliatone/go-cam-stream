// Package conversationguest exposes room operations on a guest-only listener.
package conversationguest

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/conversations"
	"github.com/goliatone/go-command"
	"golang.org/x/time/rate"
)

func New(origin string, service conversations.Workflows, bus *conversations.Commands) (http.Handler, error) {
	return newAPI(origin, false, service, bus)
}

func newAPI(origin string, loopback bool, service conversations.Workflows, bus *conversations.Commands) (http.Handler, error) {
	canonical, err := config.ConversationOrigin(origin, loopback)
	if err != nil || service == nil || bus == nil {
		return nil, api.ErrInvalidConversationRequest
	}
	u, err := url.Parse(canonical)
	if err != nil {
		return nil, api.ErrInvalidConversationRequest
	}
	mux := http.NewServeMux()
	limit := rate.NewLimiter(8, 24)
	path := api.ConversationRoomPath
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		value, err := service.Poll(participant(r).Context(), roomRef(r))
		project(w, value, err)
	})
	mux.HandleFunc("GET "+path+"/host", func(w http.ResponseWriter, r *http.Request) {
		value, err := service.HostView(participant(r).Context(), roomRef(r))
		project(w, value, err)
	})
	mux.HandleFunc("POST "+path+"/browser-stopped", func(w http.ResponseWriter, r *http.Request) {
		project(w, struct{}{}, service.BrowserCaptureStopped(participant(r).Context(), roomRef(r)))
	})
	mux.HandleFunc("POST "+path+"/redeem", func(w http.ResponseWriter, r *http.Request) {
		if !limit.Allow() {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		run[api.RoomRedeemRequest, api.RoomAdmission](w, r, bus, 6144, func(ref api.RoomRef, body api.RoomRedeemRequest) command.Message {
			return conversations.RedeemRoom{RoomRef: ref, RoomRedeemRequest: body}
		})
	})
	mux.HandleFunc("POST "+path+"/join", func(w http.ResponseWriter, r *http.Request) {
		run[api.RoomJoinRequest, api.JoinedRoom](w, r, bus, 1024, func(ref api.RoomRef, body api.RoomJoinRequest) command.Message {
			return conversations.JoinRoom{RoomRef: ref, RoomJoinRequest: body}
		})
	})
	mux.HandleFunc("POST "+path+"/signal", func(w http.ResponseWriter, r *http.Request) {
		run[api.RoomSignal, api.RoomView](w, r, bus, api.ConversationSDPMax*2+1024, func(ref api.RoomRef, body api.RoomSignal) command.Message {
			return conversations.PublishRoomSignal{RoomRef: ref, RoomSignal: body}
		})
	})
	mux.HandleFunc("POST "+path+"/publication", func(w http.ResponseWriter, r *http.Request) {
		run[api.RoomJoinRequest, api.RoomView](w, r, bus, 1024, func(ref api.RoomRef, body api.RoomJoinRequest) command.Message {
			return conversations.SetRoomPublication{RoomRef: ref, RoomJoinRequest: body}
		})
	})
	mux.HandleFunc("POST "+path+"/connection", func(w http.ResponseWriter, r *http.Request) {
		run[api.RoomConnectionRequest, api.RoomView](w, r, bus, 1024, func(ref api.RoomRef, body api.RoomConnectionRequest) command.Message {
			return conversations.SetRoomConnection{RoomRef: ref, RoomConnectionRequest: body}
		})
	})
	mux.HandleFunc("POST "+path+"/leave", func(w http.ResponseWriter, r *http.Request) {
		// Version 1 clients may still include their last observed generation.
		var body struct {
			Generation api.RoomGeneration `json:"generation"`
		}
		if !read(w, r, &body, 1024) {
			project(w, api.RoomView{}, api.ErrInvalidConversationRequest)
			return
		}
		value, err := conversations.Dispatch[api.RoomView](participant(r).Context(), bus,
			conversations.LeaveRoom{RoomRef: roomRef(r)})
		project(w, value, err)
	})
	mux.HandleFunc("POST "+path+"/end", func(w http.ResponseWriter, r *http.Request) {
		value, err := conversations.Dispatch[api.RoomView](participant(r).Context(), bus,
			conversations.EndRoom{RoomRef: roomRef(r)})
		project(w, value, err)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Host != u.Host || (r.Method != http.MethodGet && r.Header.Get("Origin") != canonical) {
			project(w, api.RoomView{}, conversations.Fault(api.RoomPermissionDenied))
			return
		}
		if r.URL.RawPath != "" {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}

func roomRef(r *http.Request) api.RoomRef {
	return api.RoomRef{RoomID: r.PathValue("room_id")}
}

func participant(r *http.Request) *http.Request {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		token = ""
	}
	return r.WithContext(conversations.WithParticipant(r.Context(), token))
}

func read(w http.ResponseWriter, r *http.Request, value any, limit int64) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value) == nil && decoder.Decode(new(any)) == io.EOF
}

func run[B, R any](w http.ResponseWriter, r *http.Request, bus *conversations.Commands, limit int64,
	build func(api.RoomRef, B) command.Message) {
	var body B
	if !read(w, r, &body, limit) {
		project(w, struct{}{}, api.ErrInvalidConversationRequest)
		return
	}
	msg := build(roomRef(r), body)
	result, err := conversations.Dispatch[R](participant(r).Context(), bus, msg)
	project(w, result, err)
}

func project(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		fault := conversations.ProjectError(err)
		status := http.StatusServiceUnavailable
		switch fault.Code {
		case api.RoomPermissionDenied, api.RoomInvitationInvalid:
			status = http.StatusForbidden
		case api.RoomFull, api.RoomStaleGeneration:
			status = http.StatusConflict
		case api.RoomAccessExpired, api.RoomAccessEnded:
			status = http.StatusGone
		case api.RoomIncompatibleClient:
			status = http.StatusUpgradeRequired
		case api.RoomInvalidRequest:
			status = http.StatusBadRequest
		}
		if errors.Is(err, api.ErrInvalidConversationRequest) {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(api.RoomErrorResponse{Error: fault})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}
