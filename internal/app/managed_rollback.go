package app

import (
	"context"
	"errors"
	"os"

	"github.com/goliatone/go-cam-stream/config"
	"github.com/goliatone/go-cam-stream/internal/viewer"
)

// PrepareManagedRollback runs offline and acquires the same locks as serve.
func PrepareManagedRollback(ctx context.Context, path string) (removed int, err error) {
	cfg, err := config.Load(ctx, path)
	if err != nil {
		return 0, err
	}
	if err = cfg.Validate(); err != nil {
		return 0, err
	}
	directory, err := managedStateDirectory(cfg)
	if err != nil {
		return 0, err
	}
	lock, err := acquireInstallation(cfg.Backend.RuntimeDir, cfg.InstallationID)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	if _, err = os.Lstat(directory); os.IsNotExist(err) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	store, err := viewer.OpenPrivateState(ctx, directory, cfg.InstallationID)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	return viewer.PrepareManagedRollback(ctx, store)
}
