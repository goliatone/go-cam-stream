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
// classification. Only the code, classification and an optional port cross the owner pipe.
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
		if busy, ok := errors.AsType[conversationhost.ListenerInUse](err); ok {
			reply.Port = busy.Port
		}
	}
	return reply
}

func (a *appSession) dispatchConversation(ctx context.Context, request AppRequest) (any, *AppError) {
	switch request.Method {
	case api.ConversationNativeConnector, api.ConversationNativeReconnect:
		return a.dispatchCallConnector(ctx, request.Method == api.ConversationNativeReconnect)
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
	case api.ConversationNativeCreate, api.ConversationNativeLocal:
		var progress func(api.ConversationSetupStage)
		if request.SetupProgress && a.encoder != nil {
			progress = func(stage api.ConversationSetupStage) {
				_ = a.encoder.Encode(AppReply{Version: 1, ID: request.ID, Event: "conversation_setup_progress", Result: stage})
			}
		}
		value, err := a.createConversation(ctx, request.Method == api.ConversationNativeLocal, progress)
		if err != nil {
			return nil, conversationAppError(err)
		}
		return value, nil
	}
	ref := api.RoomRef{RoomID: request.RoomID}
	for _, runtime := range []*conversationhost.Runtime{a.conversation, a.localConversation} {
		if runtime == nil {
			continue
		}
		value, err := runtime.Status(ctx, ref)
		if err != nil {
			continue
		}
		switch request.Method {
		case api.ConversationNativeStatus:
			return value, nil
		case api.ConversationNativeCheckRoute:
			if value.State == api.RoomEnded || value.State == api.RoomExpired {
				return nil, conversationAppError(conversations.Fault(api.RoomAccessEnded))
			}
			if err := runtime.CheckRoute(ctx); err != nil {
				return nil, conversationAppError(classifyConversationStart(err))
			}
			return true, nil
		case api.ConversationNativeEnd:
			if value.State == api.RoomEnded || value.State == api.RoomExpired {
				return value, nil
			}
			value, err := runtime.End(ctx, ref)
			return value, conversationAppError(err)
		}
	}
	return nil, conversationAppError(conversations.Fault(api.RoomInvitationInvalid))
}

func (a *appSession) dispatchCallConnector(ctx context.Context, reconnect bool) (any, *AppError) {
	settings, setup, err := a.conversationSettings()
	if err != nil {
		if !reconnect && setup == api.ConversationSetupConnectorConfig {
			return api.CallConnectorSetup, nil
		}
		if !reconnect {
			return api.CallConnectorExternal, nil
		}
		return nil, conversationAppError(setupFault(setup, api.RoomMediaUnreachable, err))
	}
	connector, err := conversationhost.NewConnector(a.service.Dir, settings)
	if err != nil {
		if !reconnect {
			return api.CallConnectorSetup, nil
		}
		return nil, conversationAppError(classifyConversationStart(err))
	}
	if reconnect {
		if err := connector.Reconnect(ctx); err != nil {
			return nil, conversationAppError(classifyConversationStart(err))
		}
		return api.CallConnectorConnected, nil
	}
	return connector.Status(ctx), nil
}

func (a *appSession) createConversation(ctx context.Context, local bool, progress func(api.ConversationSetupStage)) (api.NativeConversationPrejoin, error) {
	slot := &a.conversation
	if local {
		slot = &a.localConversation
	}
	if progress != nil {
		progress(api.ConversationSetupListenerStage)
	}
	if *slot != nil {
		value, err := (*slot).Create(ctx, progress)
		if !errors.Is(err, conversationhost.ErrListenerClosed) {
			return value, classifyConversationStart(err)
		}
		*slot = nil
	}
	runtime, err := a.startConversationHost(ctx, local)
	if err != nil {
		return api.NativeConversationPrejoin{}, err
	}
	value, err := runtime.Create(ctx, progress)
	if err != nil {
		runtime.Close()
		return api.NativeConversationPrejoin{}, classifyConversationStart(err)
	}
	*slot = runtime
	return value, nil
}

func classifyConversationStart(err error) error {
	switch {
	case errors.Is(err, conversationhost.ErrRouteUnreachable):
		return setupFault(api.ConversationSetupUnreachable, api.RoomMediaUnreachable, err)
	case errors.Is(err, conversationhost.ErrRelayUnavailable):
		return setupFault(api.ConversationSetupRelay, api.RoomMediaUnreachable, err)
	case errors.Is(err, conversationhost.ErrConnectorSetup):
		return setupFault(api.ConversationSetupConnectorConfig, api.RoomMediaUnreachable, err)
	case errors.Is(err, conversationhost.ErrConnectorUnavailable):
		return setupFault(api.ConversationSetupConnector, api.RoomMediaUnreachable, err)
	default:
		return err
	}
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
		if settings.Connector != nil && (settings.Loopback || settings.Connector.Validate(settings.Ingress) != nil) {
			return config.Conversations{}, api.ConversationSetupConnectorConfig, err
		}
		relay := (config.Sharing{Relay: settings.Relay}).WithDefaults()
		if relay.Validate() != nil || (settings.RelayOnly && relay.Relay.Mode == config.SharingRelayDisabled) {
			return config.Conversations{}, api.ConversationSetupRelayConfig, err
		}
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

func (a *appSession) startConversationHost(ctx context.Context, local bool) (*conversationhost.Runtime, error) {
	settings := config.Conversations{Loopback: true}
	if !local {
		var setup api.ConversationSetup
		var err error
		settings, setup, err = a.conversationSettings()
		if err != nil {
			return nil, setupFault(setup, api.RoomMediaUnreachable, err)
		}
	}
	var relay conversations.RelayIssuer
	mode := (config.Sharing{Relay: settings.Relay}).WithDefaults().Relay
	if mode.Mode != config.SharingRelayDisabled {
		var credentials api.NativeRelayCredentials
		if err := readJSON(filepath.Join(a.service.Dir, "conversation-relay.json"), &credentials); err != nil {
			return nil, setupFault(api.ConversationSetupRelayConfig, api.RoomMediaUnreachable, err)
		}
		provider, err := nativeRelay(mode, &credentials)
		if err != nil {
			return nil, setupFault(api.ConversationSetupRelayConfig, api.RoomMediaUnreachable, err)
		}
		relay = conversations.SharedRelay{Provider: provider}
	}
	assets := a.conversationAssets
	if assets == nil {
		assets = bundledGuestAssets("Conversations")
	}
	connector, err := conversationhost.NewConnector(a.service.Dir, settings)
	if err != nil {
		return nil, classifyConversationStart(err)
	}
	runtime, err := conversationhost.Start(ctx, settings, relay, assets, connector)
	_, portInUse := errors.AsType[conversationhost.ListenerInUse](err)
	switch {
	case err == nil:
		return runtime, nil
	case portInUse:
		return nil, setupFault(api.ConversationSetupPortInUse, api.RoomMediaUnreachable, err)
	case errors.Is(err, conversationhost.ErrSettings):
		return nil, setupFault(api.ConversationSetupInvalid, api.RoomMediaUnreachable, err)
	case errors.Is(err, conversationhost.ErrAssetsUnavailable):
		return nil, setupFault(api.ConversationSetupAssets, api.RoomMediaUnreachable, err)
	case errors.Is(err, conversationhost.ErrUnavailable):
		return nil, setupFault(api.ConversationSetupUnavailable, api.RoomMediaUnreachable, err)
	default:
		return nil, setupFault(api.ConversationSetupFailed, api.RoomMediaUnreachable, err)
	}
}
