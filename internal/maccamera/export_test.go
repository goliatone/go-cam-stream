package maccamera

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiveExportPreservesCaptureAndCredentials(t *testing.T) {
	s := testService(t)
	c, secret, err := load(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := s.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer closePreviewIO(lock)
	dir, err := s.runtimeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = privateDir(dir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var frames atomic.Int64
	frames.Store(30)
	audio := &audioProgress{}
	listener, err := s.control(ctx, c, &frames, audio, cancel, dir, nil, func() (Exports, error) {
		if exportErr := s.export(c, secret); exportErr != nil {
			return Exports{}, exportErr
		}
		return s.exportPaths(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closePreviewIO(listener)
	for range 2 {
		result, exportErr := s.Export()
		if exportErr != nil || result != s.exportPaths() {
			t.Fatal("live export failed", exportErr)
		}
		for _, path := range []string{result.Configuration, result.Environment} {
			info, statErr := os.Lstat(path)
			if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
				t.Fatal("export is not private", statErr)
			}
		}
		env, readErr := os.ReadFile(result.Environment)
		if readErr != nil || string(env) != "MAC_CAMERA_USERNAME="+secret.Username+"\nMAC_CAMERA_PASSWORD="+secret.Password+"\n" {
			t.Fatal("credentials changed")
		}
		if err = os.Remove(result.Environment); err != nil {
			t.Fatal(err)
		}
	}
	// An unwritable export target must not stop capture or leak raw diagnostics.
	if err = os.Mkdir(s.exportPaths().Environment, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Export(); err == nil || strings.Contains(err.Error(), secret.Password) {
		t.Fatal("unsafe export failure")
	}
	status, err := s.Status(ctx)
	if err != nil || status.State != "streaming" || status.Frames != 30 || ctx.Err() != nil || status.Exports != nil {
		t.Fatal("export affected capture", err)
	}
	encoded, err := json.Marshal(status)
	if err != nil || strings.Contains(string(encoded), secret.Password) {
		t.Fatal("secret in status")
	}
	saved, after, err := load(s.Dir)
	if err != nil || saved != c || after != secret {
		t.Fatal("export mutated settings or credentials")
	}
}

func TestExportDoesNotBypassConfigurationLock(t *testing.T) {
	s := testService(t)
	lock, err := s.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer closePreviewIO(lock)
	if _, err = s.Export(); err == nil {
		t.Fatal("export bypassed busy configuration without an owner socket")
	}
	if _, err = os.Stat(filepath.Join(s.Dir, "camstream-camera.env")); !os.IsNotExist(err) {
		t.Fatal("export wrote despite busy lock")
	}
}

func TestControlCloseWaitsForExport(t *testing.T) {
	s := testService(t)
	c, _, err := load(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.runtimeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = privateDir(dir); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var frames atomic.Int64
	listener, err := s.control(context.Background(), c, &frames, &audioProgress{}, func() {}, dir, nil, func() (Exports, error) {
		close(entered)
		<-release
		return s.exportPaths(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	requested := make(chan error, 1)
	go func() { _, requestErr := s.request(context.Background(), "export"); requested <- requestErr }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("export did not start")
	}
	closed := make(chan struct{})
	go func() { closePreviewIO(listener); close(closed) }()
	select {
	case <-closed:
		t.Fatal("owner released before export finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("control did not close")
	}
	if requestErr := <-requested; requestErr != nil {
		t.Fatal(requestErr)
	}
}
