//go:build linux || darwin

package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	cs "github.com/goliatone/go-cam-stream/pkg/camstream"
)

type installationLock struct {
	file *os.File
	root string
}

func acquireInstallation(root, id string) (*installationLock, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, cs.NewError(cs.CodeBackendUnavailable, nil)
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, cs.NewError(cs.CodeInvalidArgument, nil)
	}
	fd, err := syscall.Open(filepath.Join(root, "controller.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, cs.NewError(cs.CodeControllerConflict, nil)
	}
	file := os.NewFile(uintptr(fd), "controller-lock")
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		closeErr := file.Close()
		return nil, cs.NewError(cs.CodeControllerConflict, closeErr)
	}
	lock := &installationLock{file: file, root: root}
	if err = lock.identity(id); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return lock, nil
}
func (l *installationLock) identity(id string) error {
	path := filepath.Join(l.root, "installation.json")
	body, err := json.Marshal(struct {
		ID string `json:"installation_id"`
	}{id})
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		_, writeErr := file.Write(body)
		return errors.Join(writeErr, file.Close())
	}
	if !os.IsExist(err) {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	existing, err := os.ReadFile(path)
	if err != nil || string(existing) != string(body) {
		return cs.NewError(cs.CodeControllerConflict, nil)
	}
	return nil
}
func (l *installationLock) retireWorkers() error {
	root := filepath.Join(l.root, "workers")
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	for _, e := range entries {
		known := e.Name() == ".lock" && !e.IsDir() || strings.HasPrefix(e.Name(), "generation-") && e.IsDir()
		if e.Type()&os.ModeSymlink != 0 || !known {
			return cs.NewError(cs.CodeCleanupIncomplete, nil)
		}
	}
	// Called only while owning the installation lock, after authoritative boundary
	// verification. Remove only this host's dedicated subtree; never signal a PID.
	if err = os.RemoveAll(root); err != nil {
		return cs.NewError(cs.CodeCleanupIncomplete, nil)
	}
	return nil
}
func (l *installationLock) Close() error { return l.file.Close() }
