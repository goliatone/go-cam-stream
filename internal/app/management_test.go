package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/data/api"
	"github.com/goliatone/go-cam-stream/internal/viewer"
)

func TestManagementEmptyHostAdmissionAndPrivateState(t *testing.T) {
	cfg := testConfig(t)
	cfg.Cameras = nil
	cfg.Access = nil
	cfg.Management = config.Management{Enabled: true, Principals: []string{"manager"}}
	backend := newBackend()
	c, err := New(cfg, Dependencies{Backend: backend, Runtime: backend, Providers: config.DefaultProviders(), VerifyClean: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e := c.Close(context.Background()); e != nil {
			t.Error(e)
		}
	})
	c.lock, err = acquireInstallation(cfg.Backend.RuntimeDir, cfg.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.loadManagement(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := func(login, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(context.Background(), "GET", cfg.Origin+path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set(api.ServeLoginHeader, login)
		out := httptest.NewRecorder()
		c.server.Handler.ServeHTTP(out, r)
		return out
	}
	out := request("manager", api.CapabilitiesPath)
	var caps api.Capabilities
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &caps) != nil || !caps.CanManageCameras {
		t.Fatal("empty manager denied", out.Code)
	}
	if out = request("manager", "/api/cameras"); out.Code != 200 {
		t.Fatal("empty catalog denied", out.Code)
	}
	if out = request("other", api.CapabilitiesPath); out.Code != 403 {
		t.Fatal("unconfigured principal admitted", out.Code)
	}
	// Worker retirement cannot touch durable state ownership.
	if err = c.lock.retireWorkers(); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(cfg.Backend.RuntimeDir)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "managed-state")
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("private state directory")
	}
	if _, err = viewer.OpenPrivateState(context.Background(), directory, cfg.InstallationID); err == nil {
		t.Fatal("second owner acquired state")
	}
}

func TestManagementStateCannotLiveInWorkerTree(t *testing.T) {
	cfg := testConfig(t)
	for _, suffix := range []string{"workers", "workers/cameras"} {
		cfg.Management.StateDir = filepath.Join(cfg.Backend.RuntimeDir, suffix)
		if cfg.Validate() == nil {
			t.Fatal("state can be deleted by worker retirement")
		}
	}
}

func TestCorruptManagedStateStopsHostBeforeAdmission(t *testing.T) {
	cfg := testConfig(t)
	cfg.Cameras = nil
	cfg.Access = nil
	cfg.Management = config.Management{Enabled: true, Principals: []string{"manager"}}
	if err := os.MkdirAll(cfg.Backend.RuntimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(cfg.Backend.RuntimeDir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := viewer.OpenPrivateState(context.Background(), filepath.Join(root, "managed-state"), cfg.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "managed-state", "managed-state.json"), []byte("truncated"), 0600); err != nil {
		t.Fatal(err)
	}
	backend := newBackend()
	c, err := New(cfg, Dependencies{Backend: backend, Runtime: backend, Providers: config.DefaultProviders(), VerifyClean: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	bound := false
	c.onListen = func(error) { bound = true }
	if err = c.Run(context.Background()); err == nil || bound || c.ready.Load() {
		t.Fatal("corrupt state admitted host")
	}
	lock, err := acquireInstallation(cfg.Backend.RuntimeDir, cfg.InstallationID)
	if err != nil {
		t.Fatal("startup retained ownership", err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
}
