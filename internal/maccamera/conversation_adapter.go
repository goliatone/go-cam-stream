package maccamera

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/app/conversationhost"
	"github.com/goliatone/go-cam-stream/internal/conversations"
)

// conversationSetupError pairs a projected room fault with a sanitized setup
// classification. Only the code and classification cross the owner pipe.
type conversationSetupError struct {
	setup api.ConversationSetup
	err   error
}

func (e conversationSetupError) Error() string { return e.err.Error() }
func (e conversationSetupError) Unwrap() error { return e.err }

func setupFault(setup api.ConversationSetup, code api.RoomErrorCode, cause error) error {
	return conversationSetupError{setup: setup, err: errors.Join(conversations.Fault(code), cause)}
}

func conversationAppError(err error) *AppError {
	if err == nil {
		return nil
	}
	fault := conversations.ProjectError(err)
	reply := &AppError{Code: string(fault.Code), Message: "The conversation request could not be completed.", Retryable: &fault.Retryable}
	if setup, ok := errors.AsType[conversationSetupError](err); ok {
		retryable := setup.setup.Retryable()
		reply.Setup = setup.setup
		reply.Retryable = &retryable
	}
	return reply
}

func (a *appSession) dispatchConversation(ctx context.Context, request AppRequest) (any, *AppError) {
	switch request.Method {
	case api.ConversationNativeOpen:
		value, err := a.openConversationLink(ctx, request.InvitationURL)
		if err == nil {
			_ = a.releaseConversationGuest(ctx, "")
			a.conversationGuest = &value
		}
		return value, conversationAppError(err)
	case api.ConversationNativeLeave:
		err := a.releaseConversationGuest(ctx, request.RoomID)
		return nil, conversationAppError(err)
	case api.ConversationNativeCreate:
		if a.conversation == nil {
			runtime, err := a.startConversationHost(ctx)
			if err != nil {
				return nil, conversationAppError(err)
			}
			a.conversation = runtime
		}
		value, err := a.conversation.Create(ctx)
		return value, conversationAppError(err)
	}
	if a.conversation == nil {
		return nil, conversationAppError(conversations.Fault(api.RoomMediaUnreachable))
	}
	ref := api.RoomRef{RoomID: request.RoomID}
	if request.Method == api.ConversationNativeStatus {
		value, err := a.conversation.Status(ctx, ref)
		return value, conversationAppError(err)
	}
	value, err := a.conversation.End(ctx, ref)
	return value, conversationAppError(err)
}

func (a *appSession) releaseConversationGuest(ctx context.Context, roomID string) error {
	guest := a.conversationGuest
	if guest == nil || (roomID != "" && roomID != guest.Admission.Room.RoomID) {
		return nil
	}
	if err := conversations.ReleaseNativeSeat(ctx, *guest); err != nil {
		return err
	}
	a.conversationGuest = nil
	return nil
}

// conversationSettings reads the private host settings and classifies a
// missing file separately from unreadable, non-private or invalid settings.
func (a *appSession) conversationSettings() (config.Conversations, api.ConversationSetup, error) {
	var settings config.Conversations
	if err := readJSON(filepath.Join(a.service.Dir, "conversation-settings.json"), &settings); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return config.Conversations{}, api.ConversationSetupMissing, err
		}
		return config.Conversations{}, api.ConversationSetupInvalid, err
	}
	if err := settings.Validate(); err != nil {
		return config.Conversations{}, api.ConversationSetupInvalid, err
	}
	return settings, "", nil
}

func (a *appSession) openConversationLink(ctx context.Context, link string) (api.NativeConversationPrejoin, error) {
	settings, setup, err := a.conversationSettings()
	if err != nil {
		return api.NativeConversationPrejoin{}, setupFault(setup, api.RoomPermissionDenied, err)
	}
	trusted := settings.TrustedOrigins
	if settings.Ingress.Mode == config.SharingIngressExternal {
		trusted = append(trusted, settings.Ingress.PublicBaseURL)
	}
	if settings.Loopback {
		return conversations.RedeemLoopbackLink(ctx, link, trusted)
	}
	return conversations.RedeemNativeLink(ctx, link, trusted, nil)
}

func (a *appSession) startConversationHost(ctx context.Context) (*conversationhost.Runtime, error) {
	settings, setup, err := a.conversationSettings()
	if err != nil {
		return nil, setupFault(setup, api.RoomMediaUnreachable, err)
	}
	var relay conversations.RelayIssuer
	mode := (config.Sharing{Relay: settings.Relay}).WithDefaults().Relay
	if mode.Mode != config.SharingRelayDisabled {
		var credentials api.NativeRelayCredentials
		if err := readJSON(filepath.Join(a.service.Dir, "conversation-relay.json"), &credentials); err != nil {
			return nil, setupFault(api.ConversationSetupInvalid, api.RoomMediaUnreachable, err)
		}
		provider, err := nativeRelay(mode, &credentials)
		if err != nil {
			return nil, setupFault(api.ConversationSetupInvalid, api.RoomMediaUnreachable, err)
		}
		relay = conversations.SharedRelay{Provider: provider}
	}
	assets := a.conversationAssets
	if assets == nil {
		assets = bundledGuestAssets("Conversations")
	}
	runtime, err := conversationhost.Start(ctx, settings, relay, assets)
	switch {
	case err == nil:
		return runtime, nil
	case errors.Is(err, conversationhost.ErrSettings):
		return nil, setupFault(api.ConversationSetupInvalid, api.RoomMediaUnreachable, err)
	case errors.Is(err, conversationhost.ErrUnavailable):
		return nil, setupFault(api.ConversationSetupUnavailable, api.RoomMediaUnreachable, err)
	default:
		return nil, setupFault(api.ConversationSetupFailed, api.RoomMediaUnreachable, err)
	}
}
