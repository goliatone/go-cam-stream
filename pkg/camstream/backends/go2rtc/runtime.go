package go2rtc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/goliatone/go-cam-stream/pkg/camstream"
)

func (b *Backend) Start(ctx context.Context) error {
	b.closeMu.Lock()
	defer b.closeMu.Unlock()
	b.mu.Lock()
	used := b.used
	b.mu.Unlock()
	if used {
		return failure(camstream.CodeConflict, nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !supportedPlatform() {
		return failure(camstream.CodeUnsupported, nil)
	}
	if err := b.cfg.VerifyBoundary(ctx); err != nil {
		return failure(camstream.CodeBackendUnavailable, err)
	}
	if err := b.prepareRuntime(); err != nil {
		return err
	}
	b.mu.Lock()
	b.runtime, b.cancel = context.WithCancel(ctx)
	b.running = true
	b.used = true
	b.monitorDone = make(chan struct{})
	b.mu.Unlock()
	go b.monitor()
	go func() {
		<-b.runtime.Done()
		cleanup, cancel := context.WithTimeout(context.Background(), b.cfg.CleanupTimeout)
		defer cancel()
		_ = b.Close(cleanup) //nolint:errcheck // Failed shutdown retains owned records/lock; explicit Close retries and reports the failure.
	}()
	return nil
}
func (b *Backend) prepareRuntime() error {
	if err := os.MkdirAll(b.cfg.RuntimeDir, 0700); err != nil {
		return failure(camstream.CodeBackendUnavailable, err)
	}
	info, err := os.Lstat(b.cfg.RuntimeDir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return failure(camstream.CodeInvalidArgument, err)
	}
	b.lock, err = lockDirectory(b.cfg.RuntimeDir)
	if err != nil {
		return failure(camstream.CodeControllerConflict, err)
	}
	entries, err := os.ReadDir(b.cfg.RuntimeDir)
	if err == nil {
		for _, e := range entries {
			if e.Name() != ".lock" {
				err = failure(camstream.CodeCleanupIncomplete, nil)
				break
			}
		}
	}
	if err != nil {
		return errors.Join(err, b.releaseLock())
	}
	b.dir, err = os.MkdirTemp(b.cfg.RuntimeDir, "generation-")
	if err == nil {
		err = b.copyExecutable()
	}
	if err != nil {
		if cleanupErr := os.RemoveAll(b.dir); cleanupErr != nil {
			return failure(camstream.CodeCleanupIncomplete, errors.Join(err, cleanupErr))
		}
		return failure(camstream.CodeBackendUnavailable, errors.Join(err, b.releaseLock()))
	}
	return nil
}
func (b *Backend) copyExecutable() error {
	src, err := os.Open(b.cfg.Executable)
	if err != nil {
		return err
	}
	defer src.Close() //nolint:errcheck // Read-only descriptor; copy/hash failures are returned.
	b.executable = filepath.Join(b.dir, "go2rtc")
	dst, err := os.OpenFile(b.executable, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0500)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(dst, hash), io.LimitReader(src, 128<<20))
	closeErr := dst.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if hex.EncodeToString(hash.Sum(nil)) != executableSHA256() {
		return failure(camstream.CodeInvalidArgument, nil)
	}
	return nil
}
func (b *Backend) releaseLock() error {
	if b.lock == nil {
		return nil
	}
	err := b.lock.Close()
	b.lock = nil
	return err
}

func (b *Backend) Close(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, b.cfg.CleanupTimeout)
	defer cancel()
	b.closeMu.Lock()
	defer b.closeMu.Unlock()
	b.mu.Lock()
	b.running = false
	if b.cancel != nil {
		b.cancel()
	}
	sources := make([]*source, 0, len(b.sources))
	for _, s := range b.sources {
		sources = append(sources, s)
	}
	for _, v := range b.sessions {
		v.cancel()
	}
	b.mu.Unlock()
	var result error
	for _, s := range sources {
		if err := enter(ctx, s); err != nil {
			return failure(camstream.CodeCleanupIncomplete, err)
		}
		err := b.cleanSource(ctx, s)
		leave(s)
		if err != nil {
			result = err
		}
	}
	if result != nil {
		return result
	}
	if b.monitorDone != nil {
		select {
		case <-b.monitorDone:
		case <-ctx.Done():
			return failure(camstream.CodeCleanupIncomplete, ctx.Err())
		}
	}
	b.client.CloseIdleConnections()
	if b.dir != "" {
		if err := os.RemoveAll(b.dir); err != nil {
			return failure(camstream.CodeCleanupIncomplete, err)
		}
	}
	return b.releaseLock()
}
func (b *Backend) cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), b.cfg.CleanupTimeout)
}
func (b *Backend) isRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running && b.runtime.Err() == nil
}

func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
