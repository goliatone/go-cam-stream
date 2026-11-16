//go:build linux || darwin

package viewer

import (
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type managedFiles struct {
	dir, lock *os.File
	// Injection seam for filesystem fault tests; production uses File.Sync/Renameat.
	syncDirHook  func() error
	syncFileHook func() error
	renameHook   func() error
}

func privateStat(fd int, directory bool) error {
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Geteuid()) {
		return storageFailure()
	}
	mode := uint32(unix.S_IFREG)
	perm := uint32(0600)
	if directory {
		mode = unix.S_IFDIR
		perm = 0700
	}
	if uint32(stat.Mode)&unix.S_IFMT != mode || uint32(stat.Mode)&07777 != perm || (!directory && stat.Nlink != 1) {
		return storageFailure()
	}
	return nil
}
func openManagedFiles(path string) (*managedFiles, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, storageFailure()
	}
	parent := filepath.Dir(path)
	real, err := filepath.EvalSymlinks(parent)
	if err != nil || real != parent {
		return nil, storageFailure()
	}
	if err = os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	// Sync the parent too: a newly created state directory must survive a crash.
	parentFile, openErr := os.Open(parent)
	if openErr != nil {
		return nil, openErr
	}
	syncErr := parentFile.Sync()
	closeErr := parentFile.Close()
	if syncErr != nil {
		return nil, syncErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := &managedFiles{dir: os.NewFile(uintptr(fd), "managed-directory")}
	fail := func(err error) (*managedFiles, error) { _ = f.close(); return nil, err }
	if err = privateStat(fd, true); err != nil {
		return fail(err)
	}
	lock, err := unix.Openat(fd, "managed.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return fail(err)
	}
	f.lock = os.NewFile(uintptr(lock), "managed-lock")
	if err = privateStat(lock, false); err != nil {
		return fail(err)
	}
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fail(err)
	}
	return f, nil
}
func (f *managedFiles) open(name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(f.dir.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if err = privateStat(fd, false); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
func (f *managedFiles) read(name string) ([]byte, bool, error) {
	file, err := f.open(name, unix.O_RDONLY)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxManagedStateBytes+1))
	if err != nil || len(data) > maxManagedStateBytes {
		clear(data)
		return nil, true, storageFailure()
	}
	return data, true, nil
}
func (f *managedFiles) writeCandidate(body []byte) error {
	file, err := f.open(managedCandidateName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return err
	}
	_, err = file.Write(body)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = f.remove(managedCandidateName)
	}
	return err
}
func (f *managedFiles) syncFile(name string) error {
	if f.syncFileHook != nil {
		if err := f.syncFileHook(); err != nil {
			return err
		}
	}
	file, err := f.open(name, unix.O_RDONLY)
	if err != nil {
		return err
	}
	err = file.Sync()
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func (f *managedFiles) syncDir() error {
	if f.syncDirHook != nil {
		if err := f.syncDirHook(); err != nil {
			return err
		}
	}
	return f.dir.Sync()
}
func (f *managedFiles) rename() error {
	if f.renameHook != nil {
		if err := f.renameHook(); err != nil {
			return err
		}
	}
	return unix.Renameat(int(f.dir.Fd()), managedCandidateName, int(f.dir.Fd()), managedStateName)
}
func (f *managedFiles) remove(name string) error { return unix.Unlinkat(int(f.dir.Fd()), name, 0) }
func (f *managedFiles) close() error {
	var err error
	if f.lock != nil {
		err = f.lock.Close()
	}
	if f.dir != nil {
		if e := f.dir.Close(); err == nil {
			err = e
		}
	}
	return err
}

func (f *managedFiles) bind(installation string) error {
	body, err := io.ReadAll(io.LimitReader(f.lock, 256))
	if err != nil {
		return err
	}
	expected := "camstream-managed-v1:" + installation + "\n"
	if len(body) > 0 {
		if string(body) != expected {
			return storageFailure()
		}
		return nil
	}
	if _, err = f.lock.WriteAt([]byte(expected), 0); err != nil {
		return err
	}
	if err = f.lock.Sync(); err != nil {
		return err
	}
	return f.syncDir()
}
