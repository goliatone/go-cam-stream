package maccamera

import (
	"context"
	"crypto/rand"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/sharing"
	httptransport "github.com/goliatone/go-cam-stream/internal/transport/http"
)

const sharedCameraID = "local-camera"
const sharedCredentialRef = "owner-relay"

type appSharingInputs struct {
	assets      fs.FS
	proxyClient *http.Client
	source      func(context.Context, string) (string, error)
	localMedia  bool
	supervisor  string
}

type appSharing struct {
	ctx        context.Context
	owner      *Service
	inputs     appSharingInputs
	tokens     sharing.InvitationTokens
	saved      savedSharing
	state      *sharing.Service
	bus        *admin.CommandBus
	reg        admin.CommandRegistrationHandle
	cancel     context.CancelFunc
	sweepDone  chan struct{}
	relay      sharing.RelayIssuer
	relayReady bool
}

type savedSharing struct {
	Settings    config.Sharing              `json:"settings"`
	Credentials *api.NativeRelayCredentials `json:"credentials,omitempty"`
}

func bundledSharingAssets() fs.FS {
	executable, err := os.Executable()
	if err != nil {
		return nil
	}
	helpers := filepath.Dir(executable)
	contents := filepath.Dir(helpers)
	if filepath.Base(helpers) != "Helpers" || filepath.Base(contents) != "Contents" {
		return nil
	}
	return os.DirFS(filepath.Join(contents, "Resources", "Sharing"))
}

func readSharingKey(dir string) (sharing.InvitationTokens, error) {
	name := filepath.Join(dir, "sharing-key.json")
	var key []byte
	if err := readJSON(name, &key); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		if err = writeJSON(name, key); err != nil {
			return nil, err
		}
	}
	if len(key) != 32 {
		return nil, errors.New("invalid sharing key")
	}
	return sharing.NewInvitationTokens(key)
}

func (a *appSession) startSharing(ctx context.Context) *appSharing {
	inputs := a.sharingInputs
	if inputs.assets == nil {
		inputs.assets = bundledSharingAssets()
		if inputs.assets != nil {
			exe, err := os.Executable()
			if err != nil {
				return nil
			}
			inputs.supervisor = exe
		}
	}
	if inputs.assets == nil {
		return nil
	}
	if _, err := fs.Stat(inputs.assets, api.ShareAssetEntry); err != nil {
		return nil
	}
	resolve := inputs.source
	if resolve == nil {
		resolve = a.sharingSource
	}
	inputs.source = func(ctx context.Context, id string) (string, error) {
		if a.running == nil || id != sharedCameraID {
			return "", sharing.Fault(api.ShareCameraUnavailable)
		}
		return resolve(ctx, id)
	}
	tokens, err := readSharingKey(a.service.Dir)
	if err != nil {
		return nil
	}
	sh := &appSharing{ctx: ctx, owner: a.service, inputs: inputs, tokens: tokens}
	if err = readJSON(sh.settingsPath(), &sh.saved); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err = sh.open(sh.saved); err != nil {
		return nil
	}
	return sh
}

func (a *appSession) sharingSource(ctx context.Context, id string) (string, error) {
	if a.running == nil || id != sharedCameraID {
		return "", sharing.Fault(api.ShareCameraUnavailable)
	}
	status, err := a.service.Status(ctx)
	if err != nil || status.State != "streaming" || status.Frames < 1 {
		return "", sharing.Fault(api.ShareCameraUnavailable)
	}
	c, secret, err := load(a.service.Dir)
	if err != nil {
		return "", sharing.Fault(api.ShareCameraUnavailable)
	}
	u := url.URL{Scheme: "rtsp", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(c.Port)), Path: "/" + c.Path,
		User: url.UserPassword(secret.Username, secret.Password)}
	return u.String(), nil
}

func (a *appSharing) settingsPath() string {
	return filepath.Join(a.owner.Dir, "sharing-settings.json")
}

func (a *appSharing) open(saved savedSharing) error {
	settings := saved.Settings.WithDefaults()
	if settings.Validate() != nil {
		return sharing.Fault(api.ShareInvalidRequest)
	}
	relay, err := a.relayFor(saved)
	if err != nil {
		return err
	}
	bus := admin.NewCommandBus(true)
	handler, err := httptransport.GuestSharing(a.inputs.assets, bus)
	if err != nil {
		return err
	}
	ingress, err := sharing.NewExternalIngress(settings.Ingress, handler, a.inputs.proxyClient)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(a.ctx)
	var state *sharing.Service
	media, err := sharing.NewMediaRuntime(ctx, sharing.MediaRuntimeOptions{
		MediaMTX: a.owner.Tools.MediaMTX, FFmpeg: a.owner.Tools.FFmpeg, LocalOnly: a.inputs.localMedia,
		Supervisor: a.inputs.supervisor,
		OnLive:     func(scope sharing.ViewerAttempt) { state.MediaLive(scope) },
		OnLost:     func(scope sharing.ViewerAttempt) { state.TransportLost(scope) },
	})
	if err != nil {
		cancel()
		return err
	}
	state, err = sharing.NewService(sharing.Options{Source: a.inputs.source, Tokens: a.tokens, Ingress: ingress, Media: media, Relay: relay})
	if err != nil {
		cancel()
		return err
	}
	reg, err := sharing.Register(bus, state)
	if err != nil {
		cancel()
		return err
	}
	done := make(chan struct{})
	go func() { defer close(done); state.Run(ctx) }()
	a.state, a.bus, a.reg, a.cancel, a.sweepDone, a.relay = state, bus, reg, cancel, done, relay
	a.saved = saved
	return nil
}

func (a *appSharing) relayFor(saved savedSharing) (sharing.RelayIssuer, error) {
	settings := saved.Settings.WithDefaults().Relay
	if settings.Mode == config.SharingRelayDisabled {
		return nil, nil
	}
	secret := saved.Credentials
	if settings.CredentialRef != sharedCredentialRef || secret == nil {
		return nil, sharing.Fault(api.ShareRelayUnavailable)
	}
	switch settings.Mode {
	case config.SharingRelayCloudflare:
		return sharing.NewCloudflareRelay(secret.KeyID, sharing.Secret(secret.APIToken), nil)
	case config.SharingRelayCoturn:
		return sharing.NewCoturnRelay(settings.URLs, sharing.Secret(secret.SharedSecret))
	default:
		return nil, sharing.Fault(api.ShareRelayUnavailable)
	}
}

func (a *appSharing) close() error {
	if a == nil || a.state == nil {
		return nil
	}
	a.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	<-a.sweepDone
	err := errors.Join(a.state.Close(ctx), a.reg.Close())
	a.state = nil
	return err
}

func (a *appSharing) setup() api.ShareSetupView {
	settings := a.saved.Settings.WithDefaults()
	view := api.ShareSetupView{
		IngressModes: []api.ShareIngressCapability{{Mode: string(config.SharingIngressExternal), Audience: api.SharePublicAudience}},
		IngressMode:  string(settings.Ingress.Mode), Ingress: api.ShareSetupNeeded, Relay: api.ShareSetupNeeded,
		Saved: &api.ShareSavedSettings{PublicBaseURL: settings.Ingress.PublicBaseURL, GuestListen: settings.Ingress.GuestListen,
			RelayMode: settings.Relay.Mode, RelayURLs: settings.Relay.URLs, CredentialSet: a.saved.Credentials != nil},
	}
	if settings.Ingress.Mode == config.SharingIngressExternal {
		view.Ingress = api.ShareSetupReady
	}
	if a.relayReady {
		view.Relay = api.ShareSetupReady
	}
	return view
}

func (a *appSharing) configure(ctx context.Context, args api.NativeShareArguments) (api.ShareSetupView, error) {
	status, err := a.state.Status(sharing.WithOperator(ctx), sharedCameraID)
	if err != nil {
		return api.ShareSetupView{}, err
	}
	if status.ShareID != "" && status.State != api.ShareExpired {
		return api.ShareSetupView{}, sharing.Fault(api.ShareAlreadyActive)
	}
	next := savedSharing{Settings: args.Settings.WithDefaults()}
	if next.Settings.Relay.Mode != config.SharingRelayDisabled {
		next.Credentials = args.RelayCredentials
		if next.Credentials == nil && next.Settings.Relay.Mode == a.saved.Settings.Relay.Mode {
			next.Credentials = a.saved.Credentials
		}
		if next.Credentials == nil || (next.Settings.Relay.CredentialRef != "" && next.Settings.Relay.CredentialRef != sharedCredentialRef) {
			return api.ShareSetupView{}, sharing.Fault(api.ShareRelayUnavailable)
		}
		next.Settings.Relay.CredentialRef = sharedCredentialRef
	}
	candidate := &appSharing{ctx: a.ctx, owner: a.owner, inputs: a.inputs, tokens: a.tokens}
	if err = candidate.open(next); err != nil {
		return api.ShareSetupView{}, err
	}
	if err = writeJSON(a.settingsPath(), next); err != nil {
		_ = candidate.close()
		return api.ShareSetupView{}, sharing.Fault(api.ShareIngressUnavailable)
	}
	_ = a.close()
	*a = *candidate
	return a.setup(), nil
}

func (a *appSharing) dispatch(ctx context.Context, r AppRequest) (any, error) {
	ctx = sharing.WithOperator(ctx)
	args := r.Sharing
	switch r.Method {
	case api.ShareNativeSetup:
		return a.setup(), nil
	case api.ShareNativeConfigure:
		return a.configure(ctx, *args)
	case api.ShareNativeTestRelay:
		a.relayReady = sharing.TestRelay(ctx, a.relay) == nil
		if !a.relayReady {
			return nil, sharing.Fault(api.ShareRelayUnavailable)
		}
		return a.setup(), nil
	case api.ShareNativeStatus:
		return a.state.Status(ctx, args.CameraID)
	case api.ShareNativeCreate:
		request := api.CreateShareRequest{CameraID: args.CameraID}
		if args.Options != nil {
			request.Options = *args.Options
		}
		return sharing.Dispatch[api.CreateShareResult](ctx, a.bus, sharing.CreateShare{CreateShareRequest: request})
	case api.ShareNativeCopy:
		return sharing.Dispatch[api.ShareLink](ctx, a.bus, sharing.CopyShareLink{ShareRef: api.ShareRef{ShareID: args.ShareID}})
	case api.ShareNativeRevoke:
		return sharing.Dispatch[api.ShareView](ctx, a.bus, sharing.RevokeShare{ShareRef: api.ShareRef{ShareID: args.ShareID}})
	default:
		return nil, api.ErrInvalidSharingRequest
	}
}
