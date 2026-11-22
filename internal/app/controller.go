// Package app owns one controller's composition and resource lifetime.
package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goliatone/go-admin/admin"
	lifecycle "github.com/goliatone/go-admin/pkg/go-lifecycle"
	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	httptransport "github.com/goliatone/go-cam-stream/internal/transport/http"
	"github.com/goliatone/go-cam-stream/internal/viewer"
	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc"
	"github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/kasa"
	rtspencoder "github.com/goliatone/go-cam-stream/pkg/camstream/backends/go2rtc/rtsp"
	"github.com/goliatone/go-command"
	"github.com/goliatone/go-secrets/providers/environment"
)

// Dependencies is a composition seam, never a configuration or HTTP option.
// VerifyClean must prove the exclusively owned deployment boundary has no old
// workers before the host retires its private files. The host owns all mutations.
type Dependencies struct {
	Backend     cs.Backend
	Runtime     cs.Runtime
	Providers   []config.Provider
	VerifyClean func(context.Context) error
	Logger      admin.Logger
	Secrets     *viewer.ManagedSecrets
}
type Controller struct {
	cfg                config.Config
	deps               Dependencies
	core               *cs.Service
	bus                *admin.CommandBus
	commands           admin.CommandRegistrationHandle
	managementCommands admin.CommandRegistrationHandle
	management         *viewer.CameraManagementService
	secrets            *viewer.ManagedSecrets
	store              *viewer.PrivateStateStore
	recovery           *viewer.CameraRecoveryService
	server             *http.Server
	runner             *lifecycle.Runner
	lock               *installationLock
	cancel             context.CancelFunc
	ready              atomic.Bool
	started            atomic.Bool
	closeMu            sync.Mutex
	// Optional local development console reporting; production leaves this unset.
	onListen func(error)
}

func NewDefault(cfg config.Config, logger admin.Logger) (*Controller, error) {
	backendConfig := cfg.BackendConfig(verifyCleanDeployment)
	backendConfig.RuntimeDir = filepath.Join(cfg.Backend.RuntimeDir, "workers")
	resolver := viewer.NewManagedSecrets(environment.New(environment.Config{}))
	backend, err := go2rtc.New(backendConfig, kasa.NewEncoder(resolver), rtspencoder.NewEncoder(resolver))
	if err != nil {
		return nil, safe(err)
	}
	return New(cfg, Dependencies{Backend: backend, Runtime: backend, Providers: config.DefaultProviders(), VerifyClean: verifyCleanDeployment, Logger: logger, Secrets: resolver})
}
func New(cfg config.Config, deps Dependencies) (*Controller, error) {
	if deps.Backend == nil || deps.Runtime == nil || deps.VerifyClean == nil {
		return nil, cs.NewError(cs.CodeInvalidArgument, nil)
	}
	catalog, err := config.Prepare(context.Background(), cfg, deps.Providers...)
	if err != nil {
		return nil, safe(err)
	}
	strategies := make([]cs.Strategy, 0, len(deps.Providers))
	for _, p := range deps.Providers {
		strategies = append(strategies, p.Strategy)
	}
	core, err := cs.NewService(deps.Backend, cfg.ServiceOptions(), strategies...)
	if err != nil {
		return nil, safe(err)
	}
	service, err := viewer.NewCameraService(core, catalog, cfg.OperationTimeout)
	if err != nil {
		return nil, safe(err)
	}
	c := &Controller{cfg: cfg, deps: deps, core: core, bus: admin.NewCommandBus(true)}
	c.commands, err = viewer.Register(c.bus, service)
	if err != nil {
		c.bus.Close()
		return nil, safe(err)
	}
	if err = c.configureManagement(service, catalog); err != nil {
		closeErr := c.commands.Close()
		c.bus.Close()
		c.secrets.Close()
		return nil, errors.Join(safe(err), safe(closeErr))
	}
	handler, err := httptransport.New(context.Background(), cfg, service, c.bus, c.ready.Load, deps.Logger, httptransport.ManagementRoutes{Service: c.management, Recovery: c.recovery})
	if err != nil {
		closeErr := errors.Join(c.commands.Close(), c.managementCommands.Close())
		c.bus.Close()
		c.secrets.Close()
		if closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}
	c.server = &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: cfg.OperationTimeout, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	return c, nil
}

// Run binds only after acquiring installation ownership. A failed backend start
// keeps authenticated status/diagnostics available, with playback unready.
func (c *Controller) Run(ctx context.Context) error {
	if !c.started.CompareAndSwap(false, true) {
		return cs.NewError(cs.CodeConflict, nil)
	}
	runtimeCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	lock, err := acquireInstallation(c.cfg.Backend.RuntimeDir, c.cfg.InstallationID)
	if err != nil {
		cancel()
		closeErr := errors.Join(c.commands.Close(), c.managementCommands.Close())
		c.managementCommands = nil
		c.secrets.Close()
		c.commands = nil
		c.bus.Close()
		if closeErr != nil {
			return safe(closeErr)
		}
		return safe(err)
	}
	c.lock = lock
	if err = c.loadManagement(runtimeCtx); err != nil {
		return c.failStartup(err)
	}
	listener, err := new(net.ListenConfig).Listen(runtimeCtx, "tcp", c.cfg.Listen)
	if c.onListen != nil {
		c.onListen(err)
	}
	if err != nil {
		return c.failStartup(cs.NewError(cs.CodeServerUnavailable, nil))
	}
	c.server.BaseContext = func(net.Listener) context.Context { return runtimeCtx }
	stopped := make(chan error, 1)
	go func() { stopped <- c.server.Serve(listener) }()
	err = c.startPlayback(runtimeCtx)
	if err == nil {
		select {
		case <-ctx.Done():
		case err = <-stopped:
		}
	}
	cleanup, done := context.WithTimeout(context.Background(), c.cfg.OperationTimeout+c.cfg.Limits.CleanupTimeout+c.cfg.Backend.CleanupTimeout)
	defer done()
	closeErr := c.Close(cleanup)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	if closeErr != nil {
		return closeErr
	}
	return safe(err)
}

// Close retries cleanup after Run returns an error. Cancel the Run context to
// stop a running controller; do not call Close concurrently with startup.
// Unconfirmed cleanup retains the installation lock and command resources.
// Process exit must still be supervised by the boundary.
func (c *Controller) Close(ctx context.Context) error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	c.ready.Store(false)
	if c.cancel != nil {
		c.cancel()
	}
	if err := c.server.Shutdown(ctx); err != nil {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	if err := c.management.Close(ctx); err != nil {
		return err
	}
	if err := c.core.Close(ctx); err != nil {
		return safe(err)
	}
	if err := c.deps.Runtime.Close(ctx); err != nil {
		return safe(err)
	}
	if c.runner != nil {
		if err := c.runner.Shutdown(ctx); err != nil {
			return safe(err)
		}
	}
	return c.closeOwnership()
}
func safe(err error) error {
	if err == nil {
		return nil
	}
	return cs.NewError(cs.DescribeError(err).Code, nil)
}

func (c *Controller) startPlayback(runtimeCtx context.Context) error {
	var err error
	registry := lifecycle.NewRegistry()
	err = registry.Register(lifecycle.Task{Name: "playback", Phase: lifecycle.PhasePostBind, Policy: lifecycle.ErrorPolicyDegraded, Run: func(context.Context) error {
		if e := c.deps.VerifyClean(runtimeCtx); e != nil {
			return safe(e)
		}
		if e := c.lock.retireWorkers(); e != nil {
			return safe(e)
		}
		if e := c.deps.Runtime.Start(runtimeCtx); e != nil {
			return safe(e)
		}
		if e := c.core.Start(runtimeCtx); e != nil {
			return safe(e)
		}
		c.ready.Store(true)
		return nil
	}})
	if err == nil {
		c.runner, err = lifecycle.NewRunner(registry)
	}
	if err == nil {
		c.runner.MarkServing()
		err = c.runner.RunPostBind(runtimeCtx)
	}
	if err == nil && c.ready.Load() {
		err = c.runner.RunReady(runtimeCtx)
	}
	return err
}

// Load under both ownership locks before any HTTP request is admitted.
func (c *Controller) loadManagement(ctx context.Context) error {
	directory, err := managedStateDirectory(c.cfg)
	if err != nil {
		return err
	}
	_, statErr := os.Lstat(directory)
	var state viewer.ManagedState
	if c.cfg.Management.Enabled || c.cfg.Management.StateDir != "" || !os.IsNotExist(statErr) {
		store, err := viewer.OpenPrivateState(ctx, directory, c.cfg.InstallationID)
		if err != nil {
			return err
		}
		c.store = store
		state, err = store.Load(ctx)
		if err != nil {
			return err
		}
	}
	defer clear(state.Payload)
	var repository viewer.ManagedStateRepository
	if c.store != nil {
		repository = c.store
	}
	if err := c.management.InitializeManagedState(ctx, state, repository); err != nil {
		return err
	}
	return c.management.Start(ctx)
}

func managedStateDirectory(cfg config.Config) (string, error) {
	root, err := filepath.EvalSymlinks(cfg.Backend.RuntimeDir)
	if err != nil {
		return "", api.ManagementFailure(api.CodeStorageUnavailable)
	}
	directory := cfg.Management.StateDir
	if directory == "" {
		directory = filepath.Join(root, "managed-state")
	}
	rel, err := filepath.Rel(filepath.Join(root, "workers"), directory)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", api.ManagementFailure(api.CodeStorageUnavailable)
	}
	return directory, nil
}

func (c *Controller) closeOwnership() error {
	if c.managementCommands != nil {
		if err := c.managementCommands.Close(); err != nil {
			return safe(err)
		}
		c.managementCommands = nil
	}
	if c.commands != nil {
		if err := c.commands.Close(); err != nil {
			return safe(err)
		}
		c.commands = nil
	}
	c.bus.Close()
	if c.store != nil {
		if err := c.store.Close(); err != nil {
			return err
		}
		c.store = nil
	}
	c.secrets.Close()
	if c.lock != nil {
		if err := c.lock.Close(); err != nil {
			return safe(err)
		}
		c.lock = nil
	}
	return nil
}

func (c *Controller) failStartup(err error) error {
	c.cancel()
	cleanup, done := context.WithTimeout(context.Background(), c.cfg.OperationTimeout)
	defer done()
	return errors.Join(err, c.Close(cleanup))
}

func (c *Controller) configureManagement(service *viewer.CameraService, catalog config.Catalog) error {
	c.secrets = c.deps.Secrets
	if c.secrets == nil {
		c.secrets = viewer.NewManagedSecrets(environment.New(environment.Config{}))
	}
	policy, err := viewer.NewManagementPolicy(c.cfg.Management, c.cfg.InstallationID)
	if err == nil {
		c.management, err = viewer.NewCameraManagementService(viewer.ManagementOptions{Core: c.core, Viewer: service, Policy: policy, Secrets: c.secrets, Base: catalog, CleanupTimeout: c.cfg.Limits.CleanupTimeout, MaxSources: c.cfg.Limits.MaxSources})
	}
	var recovery *viewer.CameraRecoveryService
	if err == nil {
		recovery, err = viewer.NewCameraRecoveryService(service, c.management.RuntimeEpoch(), func(ctx context.Context, ref api.StreamRef) (api.ReconnectResponse, error) {
			out, e := c.bus.DispatchByNameWithOutcome(ctx, api.ReconnectCommand, map[string]any{"camera_id": string(ref.CameraID), "stream_id": string(ref.StreamID)}, nil, command.DispatchOptions{Mode: command.ExecutionModeInline})
			if e != nil {
				return api.ReconnectResponse{}, e
			}
			result, ok := out.Result.(api.ReconnectResponse)
			if !ok {
				return api.ReconnectResponse{}, cs.NewError(cs.CodeInternal, nil)
			}
			return result, nil
		})
	}
	if err == nil {
		c.managementCommands, err = viewer.RegisterManagement(c.bus, c.management, recovery)
	}

	c.recovery = recovery
	return err
}
